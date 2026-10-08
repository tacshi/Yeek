package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMultipartMetadataAndCustomBoundary(t *testing.T) {
	file := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(file, []byte("upload bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	var received []Object
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "multipart/form-data" || params["boundary"] != "custom-boundary" {
			t.Error(r.Header, err)
			w.WriteHeader(400)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				break
			}
			data, err := io.ReadAll(part)
			_ = part.Close()
			if err != nil {
				t.Error(err)
			}
			received = append(received, Object{"name": part.FormName(), "filename": part.FileName(), "contentType": part.Header.Get("Content-Type"), "value": string(data)})
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "POST", "url": server.URL, "headers": []any{Object{"name": "Content-Type", "value": `multipart/form-data; boundary="custom-boundary"`}}, "bodyType": "multipart/form-data", "body": Object{"form": []any{
		Object{"name": "data", "value": "{\n  \"ok\": true\n}", "contentType": "application/json"},
		Object{"name": "upload", "file": file, "type": "file", "filename": `custom "résumé".dat`, "contentType": "application/x-fixture"},
		Object{"name": "ignored", "file": "", "type": "file", "enabled": false},
	}}})
	response, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(received) != 2 || str(received[0], "contentType") != "application/json" || str(received[1], "filename") != `custom "résumé".dat` || str(received[1], "contentType") != "application/x-fixture" || str(received[1], "value") != "upload bytes" {
		t.Fatal(received)
	}
	snapshot, err := e.bodies.ReadFile(str(response, "id") + ".request")
	if err != nil || !strings.Contains(string(snapshot), "upload bytes") || number(response, "requestContentLength") != float64(len(snapshot)) {
		t.Fatal(err, response)
	}
}

func TestRequestFilesFailBeforeNetwork(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	cases := []struct {
		kind string
		body Object
		want string
	}{
		{"multipart/form-data", Object{"form": []any{Object{"name": "upload", "file": "", "type": "file"}}}, "upload: choose a file"},
		{"multipart/form-data", Object{"form": []any{Object{"name": "upload", "file": "/missing-upload", "type": "file"}}}, "open request file"},
		{"multipart/form-data", Object{"form": []any{Object{"name": "data", "value": "x", "contentType": "text/plain\r\nInjected: yes"}}}, "Content-Type"},
		{"binary", Object{"filePath": ""}, "choose a file"},
		{"binary", Object{"filePath": t.TempDir()}, "regular file"},
	}
	for _, tc := range cases {
		r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "POST", "url": server.URL, "bodyType": tc.kind, "body": tc.body})
		response, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatal(err, tc.want)
		}
		if _, err = e.bodies.Stat(str(response, "id") + ".request.tmp"); !os.IsNotExist(err) {
			t.Fatal("partial upload left behind", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid body reached the network")
	}
}

func TestUploadSnapshotSurvivesSourceChangeAndRedirect(t *testing.T) {
	e := testEngine(t)
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, []byte("original payload"), 0600); err != nil {
		t.Fatal(err)
	}
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies = append(bodies, string(data))
		if r.URL.Path == "/first" {
			if err = os.WriteFile(path, []byte("changed by another program"), 0600); err != nil {
				t.Error(err)
			}
			http.Redirect(w, r, "/second", http.StatusTemporaryRedirect)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	w := saveTest(t, e, Object{"model": "workspace"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "PUT", "url": server.URL + "/first", "bodyType": "binary", "body": Object{"filePath": path}})
	response, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{})
	if err != nil || len(bodies) != 2 || bodies[0] != "original payload" || bodies[1] != bodies[0] {
		t.Fatal(bodies, err)
	}
	snapshot, err := e.bodies.ReadFile(str(response, "id") + ".request")
	if err != nil || string(snapshot) != "original payload" {
		t.Fatal(string(snapshot), err)
	}
}

func TestLargeUploadUsesBoundedMemory(t *testing.T) {
	e := testEngine(t)
	const size = 64 << 20
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	file := filepath.Join(dir, "large.bin")
	f, err := root.OpenFile("large.bin", os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	var received atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			t.Error(err)
		}
		received.Store(count)
		w.WriteHeader(204)
	}))
	defer server.Close()
	w := saveTest(t, e, Object{"model": "workspace"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "PUT", "url": server.URL, "bodyType": "binary", "body": Object{"filePath": file}})
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	response, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{})
	runtime.ReadMemStats(&after)
	if err != nil || received.Load() != size || number(response, "requestContentLength") != size {
		t.Fatal(received.Load(), response, err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
		t.Fatalf("64 MiB upload allocated %d bytes on the Go heap", allocated)
	}
}

func TestRequestPreparationCancellation(t *testing.T) {
	e := testEngine(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.prepareRequestBody(ctx, "cancelled", Object{"bodyType": "binary", "body": Object{"filePath": "/missing"}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := e.bodies.Stat("cancelled.request.tmp"); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestJSONCommentsAndGraphQLGETOnWire(t *testing.T) {
	var requests []Object
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		requests = append(requests, Object{"method": r.Method, "body": string(data), "query": r.URL.Query().Get("query"), "variables": r.URL.Query().Get("variables"), "operationName": r.URL.Query().Get("operationName"), "count": len(r.URL.Query()["query"])})
		w.WriteHeader(204)
	}))
	defer server.Close()
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	text := "{\n// note\n\"url\":\"http://example.test\",\n\"values\":[1,2,],\n}"
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "POST", "url": server.URL, "bodyType": "application/json", "body": Object{"text": text}})
	if _, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{}); err != nil {
		t.Fatal(err)
	}
	var parsed Object
	if err := json.Unmarshal([]byte(str(requests[0], "body")), &parsed); err != nil || str(parsed, "url") != "http://example.test" {
		t.Fatal(requests, err)
	}
	obj(r, "body")["sendJsonComments"] = true
	saveTest(t, e, r)
	if _, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if str(requests[1], "body") != text {
		t.Fatal("opt-in body was changed", requests[1])
	}
	r = saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "GET", "url": server.URL + "?query=old&variables=old&operationName=old", "bodyType": "graphql", "body": Object{"query": "query Read($id: ID!) { item(id: $id) { name } }", "variables": "{\"id\": 1, /* comment */}", "operationName": "Read"}})
	if _, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{}); err != nil {
		t.Fatal(err)
	}
	got := requests[2]
	if str(got, "body") != "" || str(got, "query") != str(obj(r, "body"), "query") || str(got, "operationName") != "Read" || number(got, "count") != 1 {
		t.Fatal(got)
	}
	if err := json.Unmarshal([]byte(str(got, "variables")), &parsed); err != nil || number(parsed, "id") != 1 {
		t.Fatal(got, err)
	}
	if FixJSONBody("1/**/2") != "1/**/2" || FixJSONBody(`{"invalid": }`) != `{"invalid": }` {
		t.Fatal("invalid JSON was silently changed")
	}
}
