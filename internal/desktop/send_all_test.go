package desktop

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"yeek/internal/engine"
)

func TestSendAllSendsFolderRequestsInSidebarOrder(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/fail" {
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	e, err := engine.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	save := func(m engine.Object) string {
		t.Helper()
		saved, err := e.Save(t.Context(), m)
		if err != nil {
			t.Fatal(err)
		}
		return s(saved, "id")
	}
	wid := save(engine.Object{"model": "workspace", "name": "Batch"})
	folder := save(engine.Object{"model": "folder", "workspaceId": wid, "name": "Users"})
	nested := save(engine.Object{"model": "folder", "workspaceId": wid, "folderId": folder, "name": "Nested", "sortPriority": 2})
	second := save(engine.Object{"model": "http_request", "workspaceId": wid, "folderId": folder, "url": server.URL + "/second", "sortPriority": 1})
	first := save(engine.Object{"model": "http_request", "workspaceId": wid, "folderId": folder, "url": server.URL + "/first", "sortPriority": 0})
	inner := save(engine.Object{"model": "http_request", "workspaceId": wid, "folderId": nested, "url": server.URL + "/fail", "sortPriority": 0})
	save(engine.Object{"model": "websocket_request", "workspaceId": wid, "folderId": folder, "url": "ws://127.0.0.1:1/skip"})
	outside := save(engine.Object{"model": "http_request", "workspaceId": wid, "url": server.URL + "/outside"})

	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	a.testMode = true
	if got := a.folderRequests(folder); !slices.Equal(got, []string{first, second, inner}) {
		t.Fatalf("order = %v", got)
	}
	a.sendFolder(folder)
	if !slices.Equal(paths, []string{"/first", "/second", "/fail"}) {
		t.Fatalf("sent %v", paths)
	}
	for _, id := range []string{first, second, inner} {
		if a.running[id] {
			t.Fatalf("%s still running", id)
		}
		if a.responses[id] == nil {
			t.Fatalf("no response for %s", id)
		}
	}
	if n(a.responses[inner], "status") != 500 {
		t.Fatalf("status = %v", a.responses[inner]["status"])
	}
	if a.responses[outside] != nil {
		t.Fatal("request outside the folder was sent")
	}
}

func TestSendAllErrorSummary(t *testing.T) {
	if sendAllError(0, 3, nil) != nil {
		t.Fatal("unexpected error")
	}
	err := sendAllError(1, 3, http.ErrHandlerTimeout)
	if err == nil || err.Error() != "1 of 3 requests failed: http: Handler timeout" {
		t.Fatal(err)
	}
}
