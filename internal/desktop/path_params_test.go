package desktop

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// The cases of Yaak's pathPlaceholders.test.ts.
func TestExtractPathPlaceholders(t *testing.T) {
	for url, want := range map[string][]string{
		"/users/:id":                           {":id"},
		"/users/:id/posts/:postId":             {":id", ":postId"},
		"/tasks/:id:cancel":                    {":id"},
		"/users/abc:def":                       {},
		"https://example.com:8080/users/:id":   {":id"},
		"https://example.com/foo/bar?q=1#hash": {},
	} {
		if got := extractPathPlaceholders(url); !slices.Equal(got, want) {
			t.Errorf("%s: %v", url, got)
		}
	}
}

func TestRenamePathPlaceholder(t *testing.T) {
	for _, tc := range []struct{ url, old, name, want string }{
		{"/pets/:petId", ":petId", ":animalId", "/pets/:animalId"},
		{"/a/:id/b/:id", ":id", ":key", "/a/:key/b/:key"},
		{"/a/:id/:id", ":id", ":key", "/a/:key/:key"},
		{"/pets/:petId", ":petId", "animalId", "/pets/:animalId"},
		{"/tasks/:id:cancel", ":id", ":taskId", "/tasks/:taskId:cancel"},
		{"/a/:id/b/:idx", ":id", ":key", "/a/:key/b/:idx"},
		{"/id/:id?x=:id", ":id", ":key", "/id/:key?x=:id"},
		{"/a/:i.d/b/:iXd", ":i.d", ":key", "/a/:key/b/:iXd"},
		{"/pets/:petId", ":petId", ":petId", "/pets/:petId"},
	} {
		if got, ok := renamePathPlaceholder(tc.url, tc.old, tc.name); !ok || got != tc.want {
			t.Errorf("%s %s→%s = %q %v", tc.url, tc.old, tc.name, got, ok)
		}
	}
	for _, name := range []string{"", ":", ":a/b", ":a?b", ":a#b", ":a:b", ":a b"} {
		if _, ok := renamePathPlaceholder("/pets/:petId", ":petId", name); ok {
			t.Errorf("accepted %q", name)
		}
	}
	if _, ok := renamePathPlaceholder("/pets/:petId/:ownerId", ":petId", ":ownerId"); ok {
		t.Error("accepted a name another placeholder has")
	}
	if _, ok := renamePathPlaceholder("/pets/:petId", ":other", ":animalId"); ok {
		t.Error("renamed a placeholder not in the URL")
	}
}

func TestPathPlaceholderRowsInParams(t *testing.T) {
	a, d, tt := composerApp(t, "", engine.Object{})
	d.URL = "https://api.test/users/:id/posts/:postId?q=1"
	d.Tab = 1
	tt.Frame()
	names := []string{}
	for _, row := range d.Parameters {
		names = append(names, row.Name)
	}
	if !slices.Contains(names, ":id") || !slices.Contains(names, ":postId") {
		t.Fatalf("rows = %v", names)
	}
	// Unfilled placeholder rows are not saved.
	if params := oslice(d.object(), "urlParameters"); len(params) != 0 {
		t.Fatalf("saved %v", params)
	}
	// Renaming a row renames the placeholder in the URL.
	if err := tt.Click("Parameter 1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("userId")
	tt.Frame()
	if d.URL != "https://api.test/users/:userId/posts/:postId?q=1" {
		t.Fatalf("url = %s", d.URL)
	}
	// A filled row is saved, and the row of a removed placeholder goes away.
	if err := tt.Click("Parameter value 1"); err != nil {
		t.Fatal(err)
	}
	tt.Type("42")
	d.URL = "https://api.test/users/:userId"
	tt.Frame()
	names = names[:0]
	for _, row := range d.Parameters {
		names = append(names, row.Name)
	}
	if slices.Contains(names, ":postId") || !slices.Contains(names, ":userId") {
		t.Fatalf("rows after URL edit = %v", names)
	}
	if params := oslice(d.object(), "urlParameters"); len(params) != 1 || s(params[0], "value") != "42" {
		t.Fatalf("saved %v", params)
	}
	_ = a
}
