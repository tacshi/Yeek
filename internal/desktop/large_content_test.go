package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestSizeTextMatchesYaakFormatSize(t *testing.T) {
	for bytes, want := range map[float64]string{999: "999 B", 1000: "1000 B", 1500: "1.5 KB", 2048: "2 KB", 2_000_000: "2 MB", 2_500_000_000: "2.5 GB"} {
		if got := sizeText(bytes); got != want {
			t.Errorf("sizeText(%v) = %q, want %q", bytes, got, want)
		}
	}
}

func TestLargeResponseAsksBeforeShowing(t *testing.T) {
	a, _, ids := treeApp(t)
	a.openRequest(ids["Alpha"])
	response := engine.Object{"id": "rs_large", "model": "http_response", "requestId": ids["Alpha"], "state": "closed", "status": 200.0, "contentLength": 3_000_000.0,
		"headers": []any{engine.Object{"name": "Content-Type", "value": "application/json"}}}
	a.responses[ids["Alpha"]] = response
	a.bodies["rs_large"] = `{"big":true}`
	tt := ui.NewTester(a.View, 1360, 860)
	if !tt.HasText("Showing responses over") || !tt.HasText("2 MB") || !tt.HasText("Reveal Response") || !tt.HasText("Copy") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Reveal Response"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Reveal Response") || !a.revealedLarge["rs_large"] {
		t.Fatal(tt.Texts())
	}
}

func TestLargeRequestBodyCanBeDeleted(t *testing.T) {
	a, _, ids := treeApp(t)
	a.openRequest(ids["Alpha"])
	d := a.drafts[ids["Alpha"]]
	d.Tab, d.BodyType, d.Body = 0, "text/plain", strings.Repeat("x", largeBytes+1)
	tt := ui.NewTester(a.View, 1360, 860)
	if !tt.HasText("Rendering content over") || !tt.HasText("Working With Large Values") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Delete Body"); err != nil {
		t.Fatal(err)
	}
	if !a.dialogOpen || a.dialogTitle != "Delete Body Text" {
		t.Fatal(a.dialog)
	}
	if err := tt.Click("Confirm Delete Body"); err != nil {
		t.Fatal(err)
	}
	if d.Body != "" || !d.Dirty || tt.HasText("Rendering content over") {
		t.Fatal(len(d.Body))
	}
}
