package desktop

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestExportNativeWorkspaceAndPrivacySelection(t *testing.T) {
	a, e := cookieApp(t)
	other, err := e.Save(t.Context(), engine.Object{"model": "workspace", "name": "Other API"})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(other)
	a.exportFile()
	tt := ui.NewTester(a.View, 1360, 860)
	options := exportOptions(a.exports, a.list("workspace"))
	if len(options.WorkspaceIDs) != 1 || options.WorkspaceIDs[0] != a.workspace || options.IncludePrivateEnvironments {
		t.Fatal(options)
	}
	if err = tt.Click("Export Other API"); err != nil {
		t.Fatal(err)
	}
	if err = tt.Click("Include private environments"); err != nil {
		t.Fatal(err)
	}
	options = exportOptions(a.exports, a.list("workspace"))
	if len(options.WorkspaceIDs) != 2 || !options.IncludePrivateEnvironments || !tt.HasText("Export 2 Workspaces…") {
		t.Fatal(options)
	}
	if err = tt.Click("All Workspaces"); err != nil {
		t.Fatal(err)
	}
	options = exportOptions(a.exports, a.list("workspace"))
	if len(options.WorkspaceIDs) != 0 {
		t.Fatal("toggle all did not clear selection", options)
	}
	if err = tt.Click("All Workspaces"); err != nil {
		t.Fatal(err)
	}
	if len(exportOptions(a.exports, a.list("workspace")).WorkspaceIDs) != 2 {
		t.Fatal("toggle all did not select every workspace")
	}
}

func TestExportSavesQueuedRequestEdits(t *testing.T) {
	a, e := cookieApp(t)
	request, err := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Read", "url": "https://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(request)
	a.openRequest(s(request, "id"))
	tt := ui.NewTester(a.View, 1360, 860)
	if err = tt.Click("Request URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Batch(func() {
		tt.Type("https://example.test/complete-value")
		if err := tt.Click("Cookie tests"); err != nil {
			t.Fatal(err)
		}
	})
	if err := tt.ChooseMenuItem("Export…"); err != nil {
		t.Fatal(err)
	}
	if a.exports == nil {
		t.Fatal("export dialog did not open")
	}
	data, err := e.ExportWorkspaces(t.Context(), exportOptions(a.exports, a.list("workspace")))
	if err != nil {
		t.Fatal(err)
	}
	var document engine.Object
	if err = json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	requests := oslice(o(document, "resources"), "httpRequests")
	if len(requests) != 1 || !strings.HasSuffix(s(requests[0], "url"), "/complete-value") {
		t.Fatal(requests)
	}
}
