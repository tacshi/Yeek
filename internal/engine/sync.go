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
	"sync/atomic"
	"time"

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
		if syncedLocal(m, workspace) {
			local[str(m, "id")] = m
		}
		if str(m, "model") == "sync_state" && str(m, "workspaceId") == workspace && str(m, "syncDir") == dir {
			base[str(m, "modelId")] = str(m, "checksum")
			paths[str(m, "modelId")] = str(m, "relPath")
		}
	}
	// tracked are the files of models synced before that the workspace
	// still has. Yaak's get_fs_candidates (#725): such a file that can't be
	// read as its model stops the sync, where skipping it would make the
	// model look deleted and delete it.
	tracked := map[string]Object{}
	for id, rel := range paths {
		if l := local[id]; l != nil && base[id] != "" {
			tracked[rel] = l
		}
	}
	for _, entry := range entries {
		previous := tracked[entry.Name()]
		invalid := func(reason string) error {
			return fmt.Errorf("Invalid sync file: %s: %s", filepath.Join(dir, entry.Name()), reason) //nolint:staticcheck // Yaak's message, shown as it is
		}
		if !entry.Type().IsRegular() {
			if previous != nil {
				return nil, invalid("expected a regular sync file")
			}
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}
		data, err := root.ReadFile(entry.Name())
		if err != nil {
			if previous != nil {
				return nil, invalid(err.Error())
			}
			continue
		}
		var m Object
		if err = yaml.Unmarshal(data, &m); err != nil {
			if previous != nil {
				return nil, invalid(err.Error())
			}
			continue
		}
		if !syncKind(str(m, "model")) || str(m, "id") == "" {
			if previous != nil {
				return nil, invalid("file does not contain a Yaak model")
			}
			continue
		}
		if previous != nil {
			switch {
			case str(m, "id") != str(previous, "id"):
				return nil, invalid(fmt.Sprintf("expected model ID %s, found %s", str(previous, "id"), str(m, "id")))
			case modelWorkspace(m) != workspace:
				return nil, invalid(fmt.Sprintf("expected workspace ID %s, found %s", workspace, modelWorkspace(m)))
			case str(m, "model") != str(previous, "model"):
				return nil, invalid("model type does not match the tracked model")
			}
		}
		if modelWorkspace(m) != workspace {
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

// ErrSyncStale is ApplySync's answer when the workspace or its directory
// changed after the plan was made: the plan has to be made, and
// reviewed, again. Yaak's apply_sync_ops returns false then (#739).
var ErrSyncStale = errors.New("Files changed while syncing. Review the changes again.") //nolint:staticcheck // Yaak's toast, shown as is

// syncedLocal reports whether a model of the database syncs with the
// workspace's directory: a model of the workspace, and an environment only
// when it is shared.
func syncedLocal(m Object, workspace string) bool {
	if !syncKind(str(m, "model")) || str(m, "id") != workspace && str(m, "workspaceId") != workspace {
		return false
	}
	return str(m, "model") != "environment" || boolean(m, "public")
}

// sameSyncPlan reports whether two plans would change the same models from
// the same copies.
func sameSyncPlan(a, b []SyncChange) bool {
	effective := func(plan []SyncChange) map[string]SyncChange {
		m := map[string]SyncChange{}
		for _, c := range plan {
			if c.Direction != "none" || c.Conflict {
				m[c.ID] = c
			}
		}
		return m
	}
	ea, eb := effective(a), effective(b)
	if len(ea) != len(eb) {
		return false
	}
	for id, x := range ea {
		y, ok := eb[id]
		if !ok || x.LocalHash != y.LocalHash || x.RemoteHash != y.RemoteHash {
			return false
		}
	}
	return true
}

// ApplySync applies a plan PlanSync made, once reviewed, while its inputs
// still match, as Yaak's apply_sync_ops does: it returns ErrSyncStale when
// the plan has to be made again. Files are written first, so the write
// transaction never waits on the filesystem; inbound changes are then
// checked against a fresh scan, and the database again inside the
// transaction, in case another writer changed it meanwhile.
func (e *Engine) ApplySync(ctx context.Context, workspace, dir string, plan []SyncChange, resolve string) error {
	current, err := e.PlanSync(ctx, workspace, dir)
	if err != nil {
		return err
	}
	if !sameSyncPlan(plan, current) {
		return ErrSyncStale
	}
	for _, c := range plan {
		if c.Conflict && resolve != "push" && resolve != "pull" && resolve != SyncNewest {
			return fmt.Errorf("%s changed both locally and on disk; choose which copy to keep", c.Path)
		}
		if filepath.Base(c.Path) != c.Path {
			return errors.New("sync filename must stay in its directory")
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	plan = slices.Clone(plan)
	inbound := false
	for i := range plan {
		if plan[i].Conflict {
			plan[i].Direction = conflictDirection(plan[i], resolve)
		}
		inbound = inbound || plan[i].Direction == "pull"
	}
	for _, change := range plan {
		if change.Direction != "push" {
			continue
		}
		path := filepath.Join(dir, change.Path)
		if change.Local == nil {
			if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		data, err := yaml.Marshal(change.Local)
		if err != nil {
			return err
		}
		// Sync files are created as any file is, as Yaak's are.
		if err = atomicWrite(path, data, 0o666); err != nil {
			return err
		}
	}
	// Inbound changes need a fresh scan: a file edited since the plan was
	// reviewed must not be imported unseen. Files just written now match
	// the database, so only their database side has to be unchanged.
	if inbound {
		current, err = e.PlanSync(ctx, workspace, dir)
		if err != nil {
			return err
		}
		written := map[string]SyncChange{}
		for _, c := range current {
			written[c.ID] = c
		}
		for _, c := range plan {
			now := written[c.ID]
			switch c.Direction {
			case "push":
				if now.LocalHash != c.LocalHash {
					return ErrSyncStale
				}
				now.Direction, now.Conflict = "none", false
			case "pull":
				if now.LocalHash != c.LocalHash || now.RemoteHash != c.RemoteHash {
					return ErrSyncStale
				}
				now.Direction, now.Conflict = "none", false
			}
			written[c.ID] = now
		}
		for _, c := range written {
			if c.Direction != "none" || c.Conflict {
				return ErrSyncStale
			}
		}
	}
	pending := []SyncChange{}
	for _, change := range plan {
		if change.Direction == "pull" && change.Remote != nil {
			pending = append(pending, change)
		}
	}
	if err = e.Store.Write(ctx, Object{"type": "sync"}, func(t *modelTx) error {
		// The files were checked above; only the database is read again.
		for _, change := range plan {
			m, err := t.get(ctx, change.ID)
			if err != nil || !syncedLocal(m, workspace) {
				m = nil
			}
			if modelHash(m) != change.LocalHash {
				return ErrSyncStale
			}
		}
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
		model := change.Local
		if change.Direction == "pull" {
			model = change.Remote
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

// modelWorkspace is the workspace a synced model belongs to: a workspace's
// own ID, else its workspaceId.
func modelWorkspace(m Object) string {
	if str(m, "model") == "workspace" {
		return str(m, "id")
	}
	return str(m, "workspaceId")
}

// atomicWrite replaces path with data through a temporary file beside it,
// so a failed write leaves the previous file whole and readers never see
// part of it (Yaak's write_sync_file). It keeps an existing file's
// permissions and won't replace a read-only one; a new file gets perm,
// less the umask.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	var keep os.FileMode
	if existing, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil { // #nosec G302 G304 -- the caller's file.
		info, err := existing.Stat()
		_ = existing.Close()
		if err != nil {
			return err
		}
		keep = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.%d-%d.tmp", filepath.Base(path), os.Getpid(), atomicWrites.Add(1)))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm) // #nosec G302 G304 -- the caller's file, beside it.
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	if keep != 0 {
		if err = f.Chmod(keep); err != nil {
			_ = f.Close()
			return err
		}
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
	return os.Rename(tmp, path)
}

var atomicWrites atomic.Int64

// DirWatcher watches the files of dir, not of the directories in it,
// until ctx is done, calling changed after files changed, from a
// goroutine of its own, and onError when watching fails. It reports no
// change of the files SyncTempFile names, the temporary files of
// atomicWrite.
type DirWatcher func(ctx context.Context, dir string, changed func(), onError func(error)) error

// SyncTempFile reports whether a file name is that of a temporary file
// atomicWrite makes beside the file it writes.
func SyncTempFile(name string) bool {
	return strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".tmp")
}

// WatchSync calls changed, from a goroutine of its own, once the
// workspace's directory or its synced models have changed and settled, so
// the app syncs them as Yaak's initSync does; onError reports a watch that
// failed. It returns the function that stops watching.
func (e *Engine) WatchSync(ctx context.Context, workspace, dir string, changed func(), onError func(error)) (func(), error) {
	if e.WatchDir == nil {
		return nil, errors.New("no file watcher")
	}
	ctx, cancel := context.WithCancel(ctx)
	files := make(chan struct{}, 1)
	fileChanged := func() {
		select {
		case files <- struct{}{}:
		default:
		}
	}
	if err := e.WatchDir(ctx, dir, fileChanged, onError); err != nil {
		cancel()
		return nil, err
	}
	changes, unsubscribe := e.Subscribe()
	go func() {
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
			case <-files:
				schedule()
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
				changed()
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
