package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// SyncNewest resolves each conflict with the copy updated last, as Yaak's
// sync does.
const SyncNewest = "newest"

type SyncChange struct {
	ID, Path, Direction, LocalHash, RemoteHash string
	Local, Remote                              Object
	Conflict                                   bool
}

func syncKind(kind string) bool {
	return slices.Contains([]string{"workspace", "folder", "environment", "http_request", "grpc_request", "websocket_request"}, kind)
}
func modelHash(m Object) string {
	if m == nil {
		return ""
	}
	m = clone(m)
	delete(m, "createdAt")
	delete(m, "updatedAt")
	data, _ := json.Marshal(m, json.Deterministic(true))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (e *Engine) PlanSync(ctx context.Context, workspace, dir string) ([]SyncChange, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	local, remote, paths, base := map[string]Object{}, map[string]Object{}, map[string]string{}, map[string]string{}
	models, err := e.Store.List(ctx, "", "")
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		if syncKind(str(m, "model")) && (str(m, "id") == workspace || str(m, "workspaceId") == workspace) {
			if str(m, "model") == "environment" && !boolean(m, "public") {
				continue
			}
			local[str(m, "id")] = m
		}
		if str(m, "model") == "sync_state" && str(m, "workspaceId") == workspace && str(m, "syncDir") == dir {
			base[str(m, "modelId")] = str(m, "checksum")
			paths[str(m, "modelId")] = str(m, "relPath")
		}
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}
		data, err := root.ReadFile(entry.Name())
		if err != nil {
			return nil, err
		}
		var m Object
		if err = yaml.Unmarshal(data, &m); err != nil {
			if strings.HasPrefix(entry.Name(), "yaak.") {
				return nil, fmt.Errorf("%s: %w", entry.Name(), err)
			}
			continue
		}
		if !syncKind(str(m, "model")) || str(m, "id") == "" {
			continue
		}
		if str(m, "id") != workspace && str(m, "workspaceId") != workspace {
			continue
		}
		defaults, err := defaultModel(str(m, "model"))
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			defaults[k] = v
		}
		m = defaults
		id := str(m, "id")
		if remote[id] != nil {
			return nil, fmt.Errorf("multiple files contain model %s", id)
		}
		remote[id] = m
		paths[id] = entry.Name()
	}
	ids := map[string]bool{}
	for id := range local {
		ids[id] = true
	}
	for id := range remote {
		ids[id] = true
	}
	for id := range base {
		ids[id] = true
	}
	result := []SyncChange{}
	for id := range ids {
		l, r := local[id], remote[id]
		lh, rh := modelHash(l), modelHash(r)
		path := paths[id]
		if path == "" {
			path = "yaak." + id + ".yaml"
		}
		change := SyncChange{ID: id, Path: path, Local: l, Remote: r, LocalHash: lh, RemoteHash: rh}
		switch {
		case lh == rh:
			change.Direction = "none"
		case base[id] == "":
			if l == nil {
				change.Direction = "pull"
			} else if r == nil {
				change.Direction = "push"
			} else {
				change.Conflict = true
			}
		case lh == base[id]:
			change.Direction = "pull"
		case rh == base[id]:
			change.Direction = "push"
		default:
			change.Conflict = true
		}
		result = append(result, change)
	}
	slices.SortFunc(result, func(a, b SyncChange) int {
		ak, bk := "", ""
		if a.Local != nil {
			ak = str(a.Local, "model")
		} else {
			ak = str(a.Remote, "model")
		}
		if b.Local != nil {
			bk = str(b.Local, "model")
		} else {
			bk = str(b.Remote, "model")
		}
		rank := func(kind string) int {
			switch kind {
			case "workspace":
				return 0
			case "folder":
				return 1
			case "environment":
				return 2
			default:
				return 3
			}
		}
		if rank(ak) != rank(bk) {
			return rank(ak) - rank(bk)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return result, nil
}
func (e *Engine) ApplySync(ctx context.Context, workspace, dir string, plan []SyncChange, resolve string) error {
	current, err := e.PlanSync(ctx, workspace, dir)
	if err != nil {
		return err
	}
	byID := map[string]SyncChange{}
	for _, c := range current {
		byID[c.ID] = c
	}
	for _, c := range plan {
		v := byID[c.ID]
		if c.LocalHash != v.LocalHash || c.RemoteHash != v.RemoteHash {
			return errors.New("workspace changed during sync; refresh the preview")
		}
		if c.Conflict && resolve != "push" && resolve != "pull" && resolve != SyncNewest {
			return fmt.Errorf("%s changed both locally and on disk; choose which copy to keep", c.Path)
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	pending := []SyncChange{}
	for _, change := range plan {
		if change.Conflict {
			change.Direction = conflictDirection(change, resolve)
		}
		if change.Direction == "pull" && change.Remote != nil {
			pending = append(pending, change)
		}
	}
	if err = e.Store.Write(ctx, Object{"type": "sync"}, func(t *modelTx) error {
		for len(pending) > 0 {
			next := []SyncChange{}
			progress := false
			for _, change := range pending {
				if id := str(change.Remote, "folderId"); id != "" {
					if _, err := t.get(ctx, id); err != nil {
						next = append(next, change)
						continue
					}
				}
				if _, err := t.upsert(ctx, change.Remote); err != nil {
					return err
				}
				progress = true
			}
			if !progress {
				return errors.New("sync files contain missing or circular folders")
			}
			pending = next
		}
		for _, change := range plan {
			if change.Conflict {
				change.Direction = conflictDirection(change, resolve)
			}
			if change.Direction == "pull" && change.Remote == nil && change.Local != nil {
				if err := t.delete(ctx, change.ID); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	states, err := e.Store.List(ctx, "sync_state", workspace)
	if err != nil {
		return err
	}
	for _, change := range plan {
		if change.Conflict {
			change.Direction = conflictDirection(change, resolve)
		}
		if filepath.Base(change.Path) != change.Path {
			return errors.New("sync filename must stay in its directory")
		}
		path := filepath.Join(dir, change.Path)
		model := change.Local
		if change.Direction == "pull" {
			model = change.Remote
		}
		if change.Direction == "push" {
			if model == nil {
				if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			} else {
				data, err := yaml.Marshal(model)
				if err != nil {
					return err
				}
				if err = atomicWrite(path, data); err != nil {
					return err
				}
			}
		}
		state := Object{"model": "sync_state", "workspaceId": workspace, "modelId": change.ID, "checksum": modelHash(model), "relPath": change.Path, "syncDir": dir, "flushedAt": now()}
		for _, s := range states {
			if str(s, "modelId") == change.ID && str(s, "syncDir") == dir {
				state["id"] = s["id"]
			}
		}
		if _, err = e.Save(ctx, state); err != nil {
			return err
		}
	}
	if err = e.Store.EnsureWorkspace(ctx, workspace); err != nil {
		return err
	}
	metas, err := e.Store.List(ctx, "workspace_meta", workspace)
	if err != nil {
		return err
	}
	if len(metas) > 0 {
		meta := metas[0]
		meta["settingSyncDir"] = dir
		_, err = e.Save(ctx, meta)
	}
	return err
}
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".yeek-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (e *Engine) WatchSync(ctx context.Context, workspace, dir string, onError func(error)) (func(), error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err = watcher.Add(dir); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	changes, unsubscribe := e.Subscribe()
	go func() {
		defer func() { _ = watcher.Close() }()
		defer unsubscribe()
		var timer *time.Timer
		var tick <-chan time.Time
		schedule := func() {
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(400 * time.Millisecond)
			tick = timer.C
		}
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if !strings.HasPrefix(filepath.Base(ev.Name), ".yeek-") {
					schedule()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				onError(err)
			case batch := <-changes:
				for _, change := range batch {
					m := obj(change, "model")
					if str(obj(change, "updateSource"), "type") != "sync" && syncKind(str(m, "model")) && (str(m, "id") == workspace || str(m, "workspaceId") == workspace) {
						schedule()
						break
					}
				}
			case <-tick:
				tick = nil
				plan, err := e.PlanSync(ctx, workspace, dir)
				if err == nil {
					err = e.ApplySync(ctx, workspace, dir, plan, SyncNewest)
				}
				if err != nil {
					onError(err)
				}
			}
		}
	}()
	return cancel, nil
}

// conflictDirection is how a conflict resolves: as chosen, or for
// SyncNewest with the copy updated last, as Yaak's sync does.
func conflictDirection(change SyncChange, resolve string) string {
	if resolve != SyncNewest {
		return resolve
	}
	if str(change.Remote, "updatedAt") > str(change.Local, "updatedAt") {
		return "pull"
	}
	return "push"
}

// SyncDirWorkspace is the workspace whose files are in dir, as Yaak's
// openWorkspaceFromSyncDir finds it.
func (e *Engine) SyncDirWorkspace(dir string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if entry.IsDir() || ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}
		data, err := root.ReadFile(entry.Name())
		if err != nil {
			continue
		}
		var m Object
		if yaml.Unmarshal(data, &m) == nil && str(m, "model") == "workspace" && str(m, "id") != "" {
			return str(m, "id"), nil
		}
	}
	return "", errors.New("No workspace found in directory") //nolint:staticcheck // Yaak's wording, shown as is.
}
