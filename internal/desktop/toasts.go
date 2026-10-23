package desktop

import (
	"time"

	"github.com/egoist/mygo/yeekui"
)

type toastItem struct {
	id, message, color string
	shown              time.Time
	timeout            time.Duration
	held               bool
	title              string
	actions            []toastAction
}

type toastAction struct {
	label string
	run   func()
}

func (a *App) toast(message string) { a.showToast("", message, "success", 5*time.Second) }

func (a *App) showToastAction(id, message, color string, timeout time.Duration, label string, action func()) {
	a.showToastActions(id, "", message, color, timeout, toastAction{label, action})
}

func (a *App) showToastActions(id, title, message, color string, timeout time.Duration, actions ...toastAction) {
	a.showToast(id, message, color, timeout)
	last := &a.toasts[len(a.toasts)-1]
	last.title, last.actions = title, actions
}

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

var toastMotion = ui.ElementTransition{
	Duration: 200 * time.Millisecond,
	Colors:   true,
	Enter:    &ui.Motion{X: 41},
	Exit:     &ui.Motion{X: 416},
}

func (a *App) toastsView(c *ui.Context, p colors) {
	now := c.Now()
	for i := range a.toasts {
		if a.toasts[i].shown.IsZero() {
			a.toasts[i].shown = now
		}
	}
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
		if dismissed != "" {
			a.hideToast(dismissed)
		}
	}()
	ui.Overlay(c, func() {
		ui.Column(c).Key("toasts").Absolute().Right(0).Bottom(0).Children(func() {
			for i := range a.toasts {
				t := &a.toasts[i]
				slot := ui.Box(c).Key(t.id).Padding(8, 8, 0, 8).Transition(toastMotion)
				if i == len(a.toasts)-1 {
					slot.Padding(8)
				}
				slot.Children(func() { a.toastCard(c, p, t, &dismissed) })
			}
		})
	})
}

func (a *App) toastCard(c *ui.Context, p colors, t *toastItem, dismissed *string) {
	card := ui.Row(c).Width(400).Clip().Padding(12, 40, 12, 12).Gap(8).Radius(8).Border(1, p.border).Background(p.background).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, .18)).AlignItems(ui.Start)
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
			if t.title != "" {
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, t.title).FontSize(13).FontWeight(600).MaxLines(2).Selectable()
					ui.Text(c, t.message).FontSize(12).TextColor(p.muted).MaxLines(6).Selectable()
				})
			} else {
				ui.Text(c, t.message).FontSize(13).MaxLines(6).Selectable()
			}
			if len(t.actions) > 0 {
				ui.Row(c).Gap(6).Children(func() {
					for i, action := range t.actions {
						if ui.Button(c, action.label).Key(i).FontSize(12).Padding(3, 10).Clicked() {
							*dismissed = t.id
							a.afterInput = append(a.afterInput, action.run)
						}
					}
				})
			}
		})
		if smallIconButton(c, "close", "Dismiss").Absolute().Top(8).Right(8).Opacity(.6).Clicked() {
			*dismissed = t.id
		}
		if t.timeout > 0 && !t.held {
			shown, timeout, color := t.shown, t.timeout, p.border
			ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(3).Draw(func(pt *ui.Painter, r ui.Rect) {
				done := min(max(float32(pt.Now().Sub(shown))/float32(timeout), 0), 1)
				pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * (1 - done), H: r.H}, color.Alpha(1-.8*done), 0)
				if done < 1 {
					pt.AnimationFrame()
				}
			})
		}
	})
}
