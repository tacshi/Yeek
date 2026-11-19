package desktop

import (
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func sidebarFilterApp(t *testing.T) (*App, *ui.Tester, map[string]engine.Object) {
	t.Helper()
	a, _, tt := composerApp(t, "", engine.Object{})
	save := func(m engine.Object) engine.Object {
		t.Helper()
		m["workspaceId"] = a.workspace
		saved, err := a.Engine.Save(t.Context(), m)
		if err != nil {
			t.Fatal(err)
		}
		a.applyModel(saved)
		return saved
	}
	folder := save(engine.Object{"model": "folder", "name": "Accounts"})
	models := map[string]engine.Object{
		"folder": folder,
		"list":   save(engine.Object{"model": "http_request", "folderId": s(folder, "id"), "name": "List users", "method": "GET", "url": "https://api.test/users"}),
		"create": save(engine.Object{"model": "http_request", "name": "My Request", "method": "POST", "url": "https://api.test/orders"}),
		"grpc":   save(engine.Object{"model": "grpc_request", "name": "Greet", "service": "helloworld.Greeter", "method": "SayHello"}),
		"ws":     save(engine.Object{"model": "websocket_request", "name": "Feed", "url": "wss://api.test/feed"}),
	}
	return a, tt, models
}

func TestSidebarFilterLanguage(t *testing.T) {
	a, _, models := sidebarFilterApp(t)
	for query, want := range map[string][]string{
		"":                       {"list", "create", "grpc", "ws"},
		"mr":                     {"create"},
		"users":                  {"list"},
		"@url:*/users*":          {"list"},
		"@method:post":           {"create"},
		"@type:grpc":             {"grpc"},
		"@type:ws OR @type:grpc": {"grpc", "ws"},
		"@folder:accounts":       {"list"},
		"@grpc_service:*Greeter": {"grpc"},
		"-@type:http":            {"grpc", "ws"},
		`@name:"List users"`:     {"list"},
		"orders":                 {"create"},
		"feed":                   {"ws"},
		"rq":                     {},
	} {
		a.search = query
		var got []string
		for _, key := range []string{"list", "create", "grpc", "ws"} {
			if a.treeMatch(models[key], 0) {
				got = append(got, key)
			}
		}
		if len(got) != len(want) {
			t.Errorf("%q matched %v, want %v", query, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%q matched %v, want %v", query, got, want)
				break
			}
		}
	}
	a.search = "@type:http"
	if !a.treeMatch(models["folder"], 0) {
		t.Fatal("a folder with a match stays visible")
	}
	a.search = "accounts"
	if a.treeMatch(models["folder"], 0) {
		t.Fatal("a folder's own name is not a match")
	}
}

func TestSidebarFilterSuggestions(t *testing.T) {
	a, tt, _ := sidebarFilterApp(t)
	if err := tt.Click("Filter requests"); err != nil {
		t.Fatal(err)
	}
	tt.Type("@name:helloworld")
	if !tt.HasText("No results, but found matches for") || !tt.HasText("@grpc_service:helloworld") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("@grpc_service:helloworld"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.search != "@grpc_service:helloworld" || !tt.HasText("Greet") {
		t.Fatal(a.search, tt.Texts())
	}
	a.search = "zzz"
	tt.Frame()
	if !tt.HasText("No results for “zzz”") {
		t.Fatal(tt.Texts())
	}
}
