package desktop

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// Like Yaak's response.body.<id> editor state, a new response opens at its
// first line rather than where the previous response was scrolled to.
func TestNewResponseOpensAtTop(t *testing.T) {
	var rows []string
	for i := range 30 {
		rows = append(rows, fmt.Sprintf(`{"source":"APPELLATION","sourceId":"%d","sourceText":"Lot 1 DP 622701"}`, i))
	}
	long := `{"results":[` + strings.Join(rows, ",") + `]}`
	short := `{"results":[` + strings.Join(rows[:15], ",") + `],"meta":{"a":1}}`
	addResponse := func(a *App, d *Draft, id, body, created string) {
		a.applyModel(engine.Object{"model": "http_response", "id": id, "requestId": d.ID, "state": "closed", "status": float64(200), "contentLength": float64(len(body)), "headers": []any{engine.Object{"name": "Content-Type", "value": "application/json"}}, "createdAt": created})
		a.bodies[id] = body
	}
	// gutterPixels is the line-number gutter, which shows the first visible
	// line; the rest of the editor also shows a scroll bar after a scroll.
	gutterPixels := func(tt *ui.Tester) []byte {
		t.Helper()
		r, ok := tt.Find("Response body")
		if !ok {
			t.Fatal("no response editor")
		}
		img := tt.Image()
		scale := float32(img.Bounds().Dx()) / 1360 // the tester renders at the display scale
		x0, y0, w, h := int(r.X*scale), int(r.Y*scale), int(36*scale), int(r.H*scale)
		var out []byte
		for y := y0; y < y0+h; y++ {
			start := img.PixOffset(x0, y)
			out = append(out, img.Pix[start:start+w*4]...)
		}
		return out
	}

	// Scroll a long response to its end, then receive a shorter one.
	a, d, tt := viewerApp(t, "application/json", long, engine.Object{"createdAt": "2026-01-01T00:00:00"})
	tt.Frame()
	r, _ := tt.Find("Response body")
	tt.Scroll(r.X+100, r.Y+100, 0, 2000)
	tt.Frame()
	addResponse(a, d, "rs_next", short, "2026-01-02T00:00:00")
	tt.Frame()
	got := gutterPixels(tt)

	// Scrolled explicitly to its top, it looks the same: it was already there.
	tt.Scroll(r.X+100, r.Y+100, 0, -100000)
	tt.Frame()
	if !bytes.Equal(got, gutterPixels(tt)) {
		t.Fatal("the new response did not open at its first line")
	}
}
