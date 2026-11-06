package desktop

import (
	"fmt"
	"testing"
)

func TestDrawnMenuOverPreview(t *testing.T) {
	for _, scale := range []float64{1, 1.5} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			a, d, tt := viewerApp(t, "text/html", "<b>page</b>", nil)
			a.settings["interfaceScale"] = scale
			tt.Frame()
			if err := tt.Click("Response"); err != nil {
				t.Fatal(err)
			}
			item, ok := tt.Find("Copy Body")
			if !ok {
				t.Fatalf("menu = %v", tt.Menu())
			}
			x, y := item.X+item.W/2, item.Y+item.H/2
			if tt.WebViewAt(x, y) != nil {
				t.Fatal("the preview takes the pointer over the menu")
			}
			tt.CloseMenu()
			if tt.Menu() != nil {
				t.Fatal("the menu stayed open")
			}
			if tt.WebViewAt(x, y) == nil {
				t.Fatal("the menu was not over the preview")
			}
			if err := tt.Click("Response"); err != nil {
				t.Fatal(err)
			}
			if err := tt.ChooseMenuItem("Response (Raw)"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if d.ResponseMode != "Raw" {
				t.Fatalf("mode = %q", d.ResponseMode)
			}
		})
	}
}
