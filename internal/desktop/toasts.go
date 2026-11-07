package desktop

import (
	"cmp"
	"time"

	"github.com/egoist/mygo/yeekui"
	"yeek/locales"
)

type toastAction struct {
	label string
	run   func()
}

func (a *App) toast(message string) { a.showToast("", message, "success", 5*time.Second) }

func (a *App) showToastAction(id, message, color string, timeout time.Duration, label string, action func()) {
	a.showToastActions(id, "", message, color, timeout, toastAction{label, action})
}

func (a *App) showToast(id, message, color string, timeout time.Duration) {
	a.showToastActions(id, "", message, color, timeout)
}

func (a *App) showToastActions(id, title, message, color string, timeout time.Duration, actions ...toastAction) {
	t := ui.Toast{ID: id, Title: cmp.Or(title, message), Type: color, Timeout: timeout, Data: actions}
	if title != "" {
		t.Description = message
	}
	if timeout == 0 {
		t.Timeout = -1
	}
	a.toastQueue = append(a.toastQueue, t)
}

var toastMotion = ui.ElementTransition{
	Duration: 200 * time.Millisecond,
	Colors:   true,
	Enter:    &ui.Motion{X: 41},
	Exit:     &ui.Motion{X: 416},
}

func (a *App) toastsView(c *ui.Context, p colors) {
	for _, t := range a.toastQueue {
		c.AddToast(t)
	}
	a.toastQueue = nil
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		a.toastsShown = len(toasts)
		viewport.Padding(0, 8, 8, 8).AlignItems(ui.End).Gap(8)
		held := viewport.Hovered() || viewport.FocusWithin()
		for _, t := range toasts {
			a.toastCard(c, p, t, held)
		}
	})
}

func (a *App) toastCard(c *ui.Context, p colors, t ui.Toast, held bool) {
	toast := ui.ToastBase(c, t)
	card := toast.Root.Row().Width(400).Clip().Padding(12, 40, 12, 12).Gap(8).Radius(8).Border(1, p.border).Background(p.background).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, .18)).AlignItems(ui.Start).Transition(toastMotion)
	card.Children(func() {
		glyph, color := "info", p.blue
		switch t.Type {
		case "success":
			glyph, color = "checkCircle", p.green
		case "danger", "warning", "notice":
			glyph, color = "alert", map[string]ui.Color{"danger": p.red, "warning": p.orange, "notice": p.notice}[t.Type]
		}
		icon(c, glyph).FontSize(16).TextColor(color).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
			if t.Description != "" {
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, t.Title).FontSize(13).FontWeight(600).MaxLines(2).Selectable()
					ui.Text(c, t.Description).FontSize(12).TextColor(p.muted).MaxLines(6).Selectable()
				})
			} else {
				ui.Text(c, t.Title).FontSize(13).MaxLines(6).Selectable()
			}
			actions, _ := t.Data.([]toastAction)
			if len(actions) > 0 {
				ui.Row(c).Gap(6).Children(func() {
					for i, action := range actions {
						if ui.Button(c, action.label).Key(i).FontSize(12).Padding(3, 10).Clicked() {
							c.CloseToast(t.ID)
							a.afterInput = append(a.afterInput, action.run)
						}
					}
				})
			}
		})
		if smallIconButton(c, "close", locales.Dismiss()).Absolute().Top(8).Right(8).Opacity(.6).Clicked() {
			c.CloseToast(t.ID)
		}
		if left := toast.Left(); left > 0 && !held {
			end, timeout, color := c.Now().Add(left), t.Timeout, p.border
			ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(3).Draw(func(pt *ui.Painter, r ui.Rect) {
				done := min(max(1-float32(end.Sub(pt.Now()))/float32(timeout), 0), 1)
				pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * (1 - done), H: r.H}, color.Alpha(1-.8*done), 0)
				if done < 1 {
					pt.AnimationFrame()
				}
			})
		}
	})
}
