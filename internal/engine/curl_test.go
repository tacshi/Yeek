package engine

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runExportedCurl(t *testing.T, command string) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is unavailable for the wire comparison")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("a POSIX shell is unavailable for the quoting check")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command = strings.Replace(command, "curl ", "curl --silent --show-error --fail ", 1)
	cmd := exec.CommandContext(ctx, sh, "-c", command) // #nosec G204 -- exercises exported shell quoting using only synthetic data and loopback destinations.
	cmd.Env = append(os.Environ(), "NO_PROXY=*", "no_proxy=*")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("exported cURL failed: %v\n%s", err, output)
	}
}

func TestCurlFileAndFormExportOnWire(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, `a; b"c.txt`)
	if err := os.WriteFile(path, []byte("file contents"), 0600); err != nil {
		t.Fatal(err)
	}
	forms := make(chan []Object, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		rows := []Object{}
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
			rows = append(rows, Object{"name": part.FormName(), "filename": part.FileName(), "value": string(data), "contentType": part.Header.Get("Content-Type"), "method": r.Method})
		}
		forms <- rows
		w.WriteHeader(204)
	}))
	defer server.Close()
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	literal := `@/not-a-file;type=text/other $(printf substituted) 'quoted'`
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "X'$VALUE", "url": server.URL, "bodyType": "multipart/form-data", "body": Object{"form": []any{
		Object{"name": "upload", "file": path, "filename": `custom; "name".txt`, "contentType": "text/plain; charset=utf-8"},
		Object{"name": "literal", "value": literal},
		Object{"name": "typed", "value": literal, "contentType": "application/x-fixture; charset=utf-8"},
	}}})
	command, err := e.Curl(t.Context(), str(r, "id"), "")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ParseCurl(command)
	if err != nil {
		t.Fatal(err)
	}
	fields := objects(array(obj(imported, "body"), "form"))
	if str(imported, "bodyType") != "multipart/form-data" || str(imported, "method") != "X'$VALUE" || len(fields) != 3 || str(fields[0], "file") != path || str(fields[0], "filename") != `custom; "name".txt` || str(fields[1], "value") != literal || str(fields[2], "contentType") != "application/x-fixture; charset=utf-8" {
		t.Fatal(imported)
	}
	runExportedCurl(t, command)
	rows := <-forms
	if len(rows) != 3 || str(rows[0], "filename") != `custom; "name".txt` || str(rows[0], "value") != "file contents" || str(rows[0], "contentType") != "text/plain; charset=utf-8" || str(rows[0], "method") != "X'$VALUE" || str(rows[1], "value") != literal || str(rows[2], "value") != literal || str(rows[2], "contentType") != "application/x-fixture; charset=utf-8" {
		t.Fatal(rows)
	}
}

func TestCurlBinaryExportDoesNotEmbedFileData(t *testing.T) {
	data := []byte{0, 1, 2, 255, 0, 4}
	path := filepath.Join(t.TempDir(), "binary payload.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies <- body
		w.WriteHeader(204)
	}))
	defer server.Close()
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "POST", "url": server.URL, "bodyType": "binary", "body": Object{"filePath": path}})
	command, err := e.Curl(t.Context(), str(r, "id"), "")
	if err != nil || !strings.Contains(command, "--data-binary") || strings.ContainsRune(command, 0) {
		t.Fatal(command, err)
	}
	imported, err := ParseCurl(command)
	if err != nil || str(imported, "bodyType") != "binary" || str(obj(imported, "body"), "filePath") != path {
		t.Fatal(imported, err)
	}
	runExportedCurl(t, command)
	if got := <-bodies; !bytes.Equal(got, data) {
		t.Fatal(got)
	}
	// A copied file command also works when the file will only exist on the target machine.
	obj(r, "body")["filePath"] = filepath.Join(t.TempDir(), "not-created.bin")
	saveTest(t, e, r)
	if _, err = e.Curl(t.Context(), str(r, "id"), ""); err != nil {
		t.Fatal("copy read the file", err)
	}
}
