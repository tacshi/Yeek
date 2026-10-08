package desktop

import (
	"fmt"

	"github.com/egoist/mygo/yeekui"
)

// askInput is a field of an ask dialog.
type askInput struct {
	label, description, value string
	password                  bool
}

// askState is Yaak's showConfirm, showPrompt and showPromptForm: a
// description, the fields to fill, and what confirming does.
type askState struct {
	description, confirm string
	danger               bool
	inputs               []askInput
	done                 func(values []string)
}

// ask opens a dialog that asks the user to confirm, or to fill inputs.
func (a *App) ask(title, description, confirm string, danger bool, inputs []askInput, done func(values []string)) {
	a.asking = &askState{description: description, confirm: confirm, danger: danger, inputs: inputs, done: done}
	a.prompt("ask", title, "", "")
}

// confirmDialog is Yaak's showConfirm.
func (a *App) confirmDialog(title, description, confirm string, danger bool, done func()) {
	a.ask(title, description, confirm, danger, nil, func([]string) { done() })
}

func (a *App) askDialog(c *ui.Context, p colors) {
	s := a.asking
	if s == nil {
		return
	}
	submit := func() {
		values := make([]string, len(s.inputs))
		for i, input := range s.inputs {
			values[i] = input.value
		}
		a.dialogOpen, a.asking = false, nil
		s.done(values)
	}
	ui.Column(c).Padding(20).Gap(16).Children(func() {
		if s.description != "" {
			ui.Text(c, s.description).FontSize(13).MaxLines(8).Selectable()
		}
		for i := range s.inputs {
			input := &s.inputs[i]
			ui.Column(c).Key(i).Gap(6).Children(func() {
				ui.Text(c, input.label).FontSize(12).TextColor(p.muted)
				field := ui.TextInput(c, &input.value).Label(input.label).FillWidth()
				if input.password {
					field.Password()
				}
				if i == 0 {
					field.AutoFocus()
				}
				if field.Submitted() {
					submit()
				}
				if input.description != "" {
					ui.Text(c, input.description).FontSize(11).TextColor(p.muted).MaxLines(3)
				}
			})
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				a.dialogOpen, a.asking = false, nil
			}
			confirm := ui.PrimaryButton(c, s.confirm).Label(fmt.Sprintf("Confirm %s", s.confirm))
			if s.danger {
				confirm.Background(p.red)
			}
			if confirm.Clicked() {
				submit()
			}
		})
	})
}
