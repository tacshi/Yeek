package desktop

import (
	"context"
	"errors"
	"slices"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// valuePrompt is a pending prompt.text question from a request being sent.
type valuePrompt struct {
	engine.TemplatePrompt
	value string
	open  bool
	reply chan valuePromptResult
}
type valuePromptResult struct {
	value string
	err   error
}

// templatePrompter shows a prompt.text dialog and blocks the sending goroutine until it is answered.
func (a *App) templatePrompter(ctx context.Context, prompt engine.TemplatePrompt) (string, error) {
	if a.Window == nil || a.Window.IsDestroyed() {
		return "", errors.New("prompt.text needs an open Yeek window")
	}
	request := &valuePrompt{TemplatePrompt: prompt, value: prompt.Default, open: true, reply: make(chan valuePromptResult, 1)}
	a.Window.Update(func() { a.valuePrompts = append(a.valuePrompts, request) })
	select {
	case result := <-request.reply:
		return result.value, result.err
	case <-ctx.Done():
		a.Window.Update(func() { a.finishValuePrompt(request, "", ctx.Err()) })
		return "", ctx.Err()
	}
}

func (a *App) finishValuePrompt(request *valuePrompt, value string, err error) {
	index := slices.Index(a.valuePrompts, request)
	if index < 0 {
		return
	}
	a.valuePrompts = slices.Delete(a.valuePrompts, index, index+1)
	request.reply <- valuePromptResult{value, err}
}

func (a *App) valuePromptDialog(c *ui.Context, p colors) {
	if len(a.valuePrompts) == 0 {
		return
	}
	request := a.valuePrompts[0]
	ui.DialogBase(c, &request.open, func(backdrop, panel *ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .48))
		panel.Key(request).Width(480).MaxWidthPercent(90).Padding(0).Radius(10).Background(p.background).Border(1, p.border).Gap(0)
		ui.Row(c).Padding(16, 20).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			ui.Text(c, request.Title).FontSize(16).FontWeight(600).Grow(1)
		})
		ui.Column(c).Padding(20).Gap(16).Children(func() {
			ui.Text(c, request.Label).FontSize(12).TextColor(p.muted)
			entry := ui.TextInput(c, &request.value).Label(request.Label).Placeholder(request.Placeholder).AutoFocus().FillWidth()
			if request.Password {
				entry.Password()
			}
			submit := entry.Submitted()
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					request.open = false
				}
				if ui.PrimaryButton(c, "Send").Clicked() || submit {
					a.finishValuePrompt(request, request.value, nil)
				}
			})
		})
	})
	if !request.open {
		a.finishValuePrompt(request, "", engine.ErrPromptCancelled)
	}
}
