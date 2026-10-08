package engine

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	mu   sync.Mutex
	emit func([]Object)
}

type modelTx struct {
	tx      *sql.Tx
	changes []Object
	source  Object
}

func OpenStore(dir string, emit func([]Object)) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "yeek.sqlite")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS models (id TEXT PRIMARY KEY, kind TEXT NOT NULL, workspace TEXT NOT NULL DEFAULT '', data TEXT NOT NULL CHECK(json_valid(data)));
CREATE INDEX IF NOT EXISTS models_workspace ON models(workspace,kind);
CREATE TABLE IF NOT EXISTS plugin_values (plugin TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY(plugin,key));
CREATE TABLE IF NOT EXISTS import_links (source TEXT NOT NULL, source_key TEXT NOT NULL, model_id TEXT NOT NULL, content TEXT NOT NULL, PRIMARY KEY(source,source_key));`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = os.Chmod(file, 0600); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, emit: emit}
	if _, err = s.Get(context.Background(), "default"); errors.Is(err, sql.ErrNoRows) {
		_, err = s.Upsert(context.Background(), Object{"model": "settings"}, Object{"type": "background"})
		if err != nil {
			_ = db.Close()
			return nil, err
		}
	} else if err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func readModel(row *sql.Row) (Object, error) {
	var data string
	if err := row.Scan(&data); err != nil {
		return nil, err
	}
	var m Object
	err := json.Unmarshal([]byte(data), &m)
	return m, err
}
func (s *Store) Get(ctx context.Context, id string) (Object, error) {
	return readModel(s.db.QueryRowContext(ctx, "SELECT data FROM models WHERE id=?", id))
}
func readModels(rows *sql.Rows) ([]Object, error) {
	defer func() { _ = rows.Close() }()
	result := []Object{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var m Object
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
func (s *Store) List(ctx context.Context, kind, workspace string) ([]Object, error) {
	query := "SELECT data FROM models WHERE (?='' OR kind=?) AND (?='' OR workspace=?) ORDER BY json_extract(data,'$.sortPriority'),json_extract(data,'$.createdAt'),id"
	rows, err := s.db.QueryContext(ctx, query, kind, kind, workspace, workspace)
	if err != nil {
		return nil, err
	}
	return readModels(rows)
}
func (s *Store) Find(ctx context.Context, kind, key, value string) ([]Object, error) {
	all, err := s.List(ctx, kind, "")
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(m Object) bool { return str(m, key) != value }), nil
}
func (s *Store) Write(ctx context.Context, source Object, fn func(*modelTx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	t := &modelTx{tx: tx, source: source}
	if err = fn(t); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if s.emit != nil && len(t.changes) > 0 {
		s.emit(t.changes)
	}
	return nil
}
func (t *modelTx) get(ctx context.Context, id string) (Object, error) {
	return readModel(t.tx.QueryRowContext(ctx, "SELECT data FROM models WHERE id=?", id))
}
func (t *modelTx) all(ctx context.Context) ([]Object, error) {
	r, e := t.tx.QueryContext(ctx, "SELECT data FROM models")
	if e != nil {
		return nil, e
	}
	return readModels(r)
}
func (t *modelTx) upsert(ctx context.Context, patch Object) (Object, error) {
	kind := str(patch, "model")
	m, err := defaultModel(kind)
	if err != nil {
		return nil, err
	}
	id := str(patch, "id")
	if id == "" {
		id = str(m, "id")
	}
	if id == "" && kind == "key_value" {
		_ = t.tx.QueryRowContext(ctx, "SELECT id FROM models WHERE kind='key_value' AND json_extract(data,'$.namespace')=? AND json_extract(data,'$.key')=?", str(patch, "namespace"), str(patch, "key")).Scan(&id)
	}
	created := true
	if id != "" {
		prev, e := t.get(ctx, id)
		if e == nil {
			if str(prev, "model") != kind {
				return nil, errors.New("model kind cannot change")
			}
			maps.Copy(m, prev)
			created = false
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	} else {
		id = newID(kind)
	}
	if id == "__proto__" || id == "constructor" || id == "prototype" {
		return nil, errors.New("invalid model ID")
	}
	createdAt := m["createdAt"]
	maps.Copy(m, patch)
	m["id"] = id
	m["createdAt"] = createdAt
	m["updatedAt"] = now()
	if workspace := str(m, "workspaceId"); workspace != "" {
		parent, e := t.get(ctx, workspace)
		if e != nil || str(parent, "model") != "workspace" {
			return nil, fmt.Errorf("workspace %q does not exist", workspace)
		}
	}
	if parentID := str(m, "folderId"); parentID != "" {
		parent, e := t.get(ctx, parentID)
		if e != nil || str(parent, "model") != "folder" || str(parent, "workspaceId") != str(m, "workspaceId") {
			return nil, errors.New("folder must belong to this workspace")
		}
		seen := map[string]bool{id: true}
		for parent != nil {
			p := str(parent, "id")
			if seen[p] {
				return nil, errors.New("folder cannot contain itself")
			}
			seen[p] = true
			p = str(parent, "folderId")
			if p == "" {
				break
			}
			parent, e = t.get(ctx, p)
			if e != nil {
				return nil, e
			}
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	_, err = t.tx.ExecContext(ctx, "INSERT INTO models(id,kind,workspace,data) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET workspace=excluded.workspace,data=excluded.data", id, kind, str(m, "workspaceId"), string(data))
	if err != nil {
		return nil, err
	}
	t.changes = append(t.changes, Object{"model": clone(m), "change": Object{"type": "upsert", "created": created}, "updateSource": t.source})
	return m, nil
}
func (s *Store) Upsert(ctx context.Context, patch, source Object) (m Object, err error) {
	err = s.Write(ctx, source, func(t *modelTx) error { m, err = t.upsert(ctx, patch); return err })
	return
}
func (s *Store) EnsureWorkspace(ctx context.Context, id string) error {
	return s.Write(ctx, Object{"type": "background"}, func(t *modelTx) error { return t.ensureWorkspace(ctx, id) })
}
func (t *modelTx) ensureWorkspace(ctx context.Context, id string) error {
	w, err := t.get(ctx, id)
	if err != nil {
		return err
	}
	if str(w, "model") != "workspace" {
		return errors.New("not a workspace")
	}
	all, err := t.all(ctx)
	if err != nil {
		return err
	}
	for _, kind := range []string{"environment", "cookie_jar", "workspace_meta"} {
		exists := slices.ContainsFunc(all, func(m Object) bool {
			return str(m, "model") == kind && str(m, "workspaceId") == id && (kind != "environment" || str(m, "parentModel") == "workspace")
		})
		if exists {
			continue
		}
		m := Object{"model": kind, "workspaceId": id}
		if kind == "environment" {
			maps.Copy(m, Object{"name": "Global Variables", "parentModel": "workspace", "parentId": id})
		}
		if _, err = t.upsert(ctx, m); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) WorkspaceModels(ctx context.Context, id string) ([]Object, error) {
	if id != "" {
		if err := s.EnsureWorkspace(ctx, id); err != nil {
			return nil, err
		}
	}
	all, err := s.List(ctx, "", "")
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(m Object) bool {
		k := str(m, "model")
		return strings.HasSuffix(k, "_event") || k == "sync_state" || k == "import_source_resource" || k == "oauth_token" || (str(m, "workspaceId") != "" && str(m, "workspaceId") != id)
	}), nil
}
func (s *Store) Delete(ctx context.Context, id string, source Object) error {
	return s.Write(ctx, source, func(t *modelTx) error { return t.delete(ctx, id) })
}
func (t *modelTx) delete(ctx context.Context, id string) error {
	root, err := t.get(ctx, id)
	if err != nil {
		return err
	}
	if str(root, "model") == "settings" {
		return errors.New("settings cannot be deleted")
	}
	all, err := t.all(ctx)
	if err != nil {
		return err
	}
	removed := map[string]bool{id: true}
	for change := true; change; {
		change = false
		for _, m := range all {
			mid := str(m, "id")
			if removed[mid] {
				continue
			}
			for _, key := range []string{"workspaceId", "folderId", "parentId", "requestId", "responseId", "connectionId", "importSourceId"} {
				if removed[str(m, key)] {
					removed[mid] = true
					change = true
					break
				}
			}
		}
	}
	for _, m := range all {
		if !removed[str(m, "id")] {
			continue
		}
		if _, err = t.tx.ExecContext(ctx, "DELETE FROM models WHERE id=?", str(m, "id")); err != nil {
			return err
		}
		t.changes = append(t.changes, Object{"model": m, "change": Object{"type": "delete"}, "updateSource": t.source})
	}
	return nil
}
func (s *Store) Duplicate(ctx context.Context, id string, source Object) (result string, err error) {
	err = s.Write(ctx, source, func(t *modelTx) error {
		root, e := t.get(ctx, id)
		if e != nil {
			return e
		}
		all, e := t.all(ctx)
		if e != nil {
			return e
		}
		allowed := []string{"folder", "environment", "http_request", "grpc_request", "websocket_request", "workspace"}
		if !slices.Contains(allowed, str(root, "model")) {
			return errors.New("this model cannot be duplicated")
		}
		name := str(root, "name") + " Copy"
		base := name
		for n := 2; slices.ContainsFunc(all, func(m Object) bool { return str(m, "model") == str(root, "model") && str(m, "name") == name }); n++ {
			name = fmt.Sprintf("%s %d", base, n)
		}
		ids := map[string]string{id: newID(str(root, "model"))}
		pending := []Object{root}
		for i := 0; i < len(pending); i++ {
			parent := str(pending[i], "id")
			for _, m := range all {
				mid := str(m, "id")
				if _, ok := ids[mid]; ok {
					continue
				}
				if !slices.Contains(allowed, str(m, "model")) {
					continue
				}
				if str(m, "folderId") == parent || str(m, "parentId") == parent || str(m, "workspaceId") == parent {
					ids[mid] = newID(str(m, "model"))
					pending = append(pending, m)
				}
			}
		}
		for _, m := range pending {
			copy := clone(m)
			old := str(copy, "id")
			copy["id"] = ids[old]
			if old == id {
				copy["name"] = name
				copy["sortPriority"] = number(copy, "sortPriority") + 0.001
			}
			for _, key := range []string{"folderId", "parentId", "workspaceId"} {
				if mapped, ok := ids[str(copy, key)]; ok {
					copy[key] = mapped
				}
			}
			if _, e = t.upsert(ctx, copy); e != nil {
				return e
			}
		}
		result = ids[id]
		return nil
	})
	return
}
