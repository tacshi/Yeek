package desktop

import (
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestAuthTabMenuChoosesAuthentication(t *testing.T) {
	_, d, tt := composerApp(t, "", engine.Object{})
	if err := tt.Click("Auth"); err != nil {
		t.Fatal(err)
	}
	if d.Tab != 3 {
		t.Fatalf("tab = %d", d.Tab)
	}
	if err := tt.Click("Auth"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Bearer Token"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d.AuthType != "bearer" || !tt.HasText("Bearer") {
		t.Fatal(d.AuthType, tt.Texts())
	}
}

func TestSidebarFilterMatchesFolderContents(t *testing.T) {
	a, _, tt := composerApp(t, "", engine.Object{})
	folder, err := a.Engine.Save(t.Context(), engine.Object{"model": "folder", "workspaceId": a.workspace, "name": "Accounts"})
	if err != nil {
		t.Fatal(err)
	}
	inner, err := a.Engine.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "folderId": s(folder, "id"), "name": "Delete account", "method": "DELETE"})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(folder)
	a.applyModel(inner)
	if err := tt.Click("Filter requests"); err != nil {
		t.Fatal(err)
	}
	tt.Type("delete")
	if !tt.HasText("Accounts") || !tt.HasText("Delete account") || a.treeMatch(a.models[a.active], 0) {
		t.Fatal(tt.Texts())
	}
	tt.Type("zzz")
	if !tt.HasText("No results for “deletezzz”") {
		t.Fatal(tt.Texts())
	}
}

func TestYaakMethodAndStatusLabels(t *testing.T) {
	for method, want := range map[string]string{"get": "GET ", "DELETE": "DELE", "PATCH": "PTCH", "PROPFIND": "PROP", "graphql": "GQL "} {
		if got := shortMethod(method); got != want {
			t.Errorf("shortMethod(%q) = %q, want %q", method, got, want)
		}
	}
	if got := requestMethod(engine.Object{"model": "http_request", "bodyType": "graphql", "method": "POST"}); got != "GRAPHQL" {
		t.Error(got)
	}
	ok := engine.Object{"status": float64(201), "statusReason": "Created", "state": "closed"}
	failed := engine.Object{"status": float64(0), "state": "closed"}
	if statusLabel(ok, false) != "201 Created" || statusLabel(ok, true) != "201" || statusLabel(failed, true) != "ERR" {
		t.Error(statusLabel(ok, false), statusLabel(failed, true))
	}
	p := colors{green: ui.Hex("#00ff00"), red: ui.Hex("#ff0000")}
	if statusColor(ok, p) != p.green || statusColor(failed, p) != p.red {
		t.Error("status colors")
	}
}
