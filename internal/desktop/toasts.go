package desktop

import (
	"time"

	"github.com/egoist/mygo/yeekui"
)

// toastItem is one of Yaak's toasts.
type toastItem struct {
	id, message, color string
	shown              time.Time
	timeout            time.Duration // zero stays until dismissed
	held               bool          // touched, so it no longer hides itself
	// action, when set, is a button under the message, as Yaak's toast action.
	actionLabel string
	action      func()
}

// toast shows a message in the bottom-right corner for five seconds, as Yaak's showToast does.
func (a *App) toast(message string) { a.showToast("", message, "success", 5*time.Second) }

// showToastAction is showToast with a button that runs action.
func (a *App) showToastAction(id, message, color string, timeout time.Duration, label string, action func()) {
	a.showToast(id, message, color, timeout)
	last := &a.toasts[len(a.toasts)-1]
	last.actionLabel, last.action = label, action
}

// showToast replaces any toast with the same id.
func (a *App) showToast(id, message, color string, timeout time.Duration) {
	if id != "" {
		a.hideToast(id)
	} else {
		a.toastSerial++
		id = "toast-" + time.Now().Format("150405.000000") + string(rune('a'+a.toastSerial%26))
	}
	a.toasts = append(a.toasts, toastItem{id: id, message: message, color: color, timeout: timeout})
}

func (a *App) hideToast(id string) {
	for i, t := range a.toasts {
		if t.id == id {
			a.toasts = append(a.toasts[:i], a.toasts[i+1:]...)
			return
		}
	}
}

func (a *App) toastsView(c *ui.Context, p colors) {
	if len(a.toasts) == 0 {
		return
	}
	now := c.Now()
	for i := range a.toasts {
		if a.toasts[i].shown.IsZero() {
			a.toasts[i].shown = now
		}
	}
	// Hide the ones whose time is up; ask for a frame when the next one is due.
	kept := a.toasts[:0]
	var next time.Duration
	for _, t := range a.toasts {
		if t.timeout > 0 && !t.held {
			left := t.timeout - now.Sub(t.shown)
			if left <= 0 {
				continue
			}
			if next == 0 || left < next {
				next = left
			}
		}
		kept = append(kept, t)
	}
	a.toasts = kept
	if next > 0 {
		c.After(next)
	}
	dismissed := ""
	defer func() {
		// Removed after drawing, so the list is not changed while it is drawn.
		if dismissed != "" {
			a.hideToast(dismissed)
		}
	}()
	ui.Overlay(c, func() {
		ui.Column(c).Absolute().Right(0).Bottom(0).Padding(8).Gap(8).Children(func() {
			for i := range a.toasts {
				t := &a.toasts[i]
				card := ui.Row(c).Key(t.id).Width(400).Padding(12, 40, 12, 12).Gap(8).Radius(8).Border(1, p.border).Background(p.background).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, .18)).AlignItems(ui.Start)
				if card.Pressed() || card.Hovered() {
					t.held = true
				}
				card.Children(func() {
					glyph, color := "info", p.blue
					switch t.color {
					case "success":
						glyph, color = "checkCircle", p.green
					case "danger", "warning", "notice":
						glyph, color = "alert", map[string]ui.Color{"danger": p.red, "warning": p.orange, "notice": p.notice}[t.color]
					}
					icon(c, glyph).FontSize(16).TextColor(color).Shrink(0)
					ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
						ui.Text(c, t.message).FontSize(13).MaxLines(6).Selectable()
						if t.action != nil && ui.Button(c, t.actionLabel).FontSize(12).Padding(3, 10).AlignSelf(ui.Start).Clicked() {
							action := t.action
							dismissed = t.id
							a.afterInput = append(a.afterInput, action)
						}
					})
					if smallIconButton(c, "close", "Dismiss").Absolute().Top(8).Right(8).Opacity(.6).Clicked() {
						dismissed = t.id
					}
				})
			}
		})
	})
}
