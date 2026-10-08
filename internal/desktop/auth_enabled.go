package desktop

import (
	"context"
	"time"

	"github.com/egoist/mygo/yeekui"
)

// authConditionPreview is the rendered state of an "Enabled when..."
// template, for the badge beside it.
type authConditionPreview struct {
	source                    string
	started, pending, enabled bool
}

// authEnabledControl is the Enabled / Disabled / Enabled when... switch at
// the top of Yaak's HttpAuthenticationEditor.
func (a *App) authEnabledControl(c *ui.Context, p colors, d *Draft) {
	selected := 0
	switch disabled := d.AuthDisabled.(type) {
	case bool:
		if disabled {
			selected = 1
		}
	case string:
		selected = 2
	}
	ui.Column(c).Key("auth-enabled").Gap(12).Children(func() {
		ui.Row(c).Children(func() {
			if ui.Segmented(c, &selected, "Enabled", "Disabled", "Enabled when...").Label("Authentication enabled").Changed() {
				value := []any{false, true, ""}[selected]
				a.deferUntilInputs(func() { d.AuthDisabled = value; d.Dirty = true })
			}
			ui.Spacer(c)
		})
		condition, ok := d.AuthDisabled.(string)
		if !ok {
			return
		}
		a.previewAuthCondition(d, condition)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			entry := a.templateInput(c, p, &condition, d.ID+":auth:disabled", "Dynamic Disabled", "Enabled when this renders a non-empty value", false).Grow(1).Height(28).Border(1, p.border).Radius(4)
			if entry.Changed() {
				d.AuthDisabled = condition
				d.Dirty = true
			}
			badge := "disabled"
			switch {
			case d.authPreview.pending:
				badge = "loading"
			case d.authPreview.enabled:
				badge = "enabled"
			}
			ui.Text(c, badge).FontSize(11).TextColor(p.subtle).Padding(2, 6).Radius(999).Background(p.subtle.Alpha(.15)).SingleLine()
		})
	})
}

// previewAuthCondition renders the condition once per change, like Yaak's
// useRenderTemplate with purpose "preview".
func (a *App) previewAuthCondition(d *Draft, condition string) {
	if d.authPreview.started && d.authPreview.source == condition {
		return
	}
	d.authPreview = authConditionPreview{source: condition, started: true, pending: condition != ""}
	if condition == "" {
		return
	}
	scope := a.templateScope()
	a.run(func() (func(), error) {
		ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
		defer cancel()
		rendered, err := a.Engine.PreviewTemplate(ctx, condition, scope.workspace, scope.folder, scope.environment, scope.request, scope.cookieJar)
		return func() {
			if d.authPreview.source == condition {
				d.authPreview = authConditionPreview{source: condition, started: true, enabled: err == nil && rendered != ""}
			}
		}, nil
	})
}
