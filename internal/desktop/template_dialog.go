package desktop

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

type templateArgument struct {
	text, kind string
	expression bool
}
type templateForm struct {
	open, busy         bool
	doc                *documentEditor
	source             string
	start, end         int
	scope              templateScope
	definitions        []engine.TemplateDefinition
	variables          map[string]string
	search, name, kind string
	definition         engine.TemplateDefinition
	args               map[string]*templateArgument
	error, preview     string
	secureExisting     string
	revision           uint64
}

func (a *App) openTemplateForm(doc *documentEditor, source string, start, end int, value *engine.TemplateExpression) {
	scope := a.templateScope()
	f := &templateForm{open: true, doc: doc, source: source, start: start, end: end, scope: scope, definitions: a.Engine.TemplateDefinitions(), variables: a.templateVariables(scope)}
	if value != nil {
		f.selectExpression(*value)
	}
	a.templateForm = f
	doc.state.Completions("", 0)
}
func (f *templateForm) selectExpression(value engine.TemplateExpression) {
	f.name, f.kind, f.error, f.preview = value.Value, value.Kind, "", ""
	f.args = map[string]*templateArgument{}
	f.definition = engine.TemplateDefinition{Name: value.Value}
	f.secureExisting = ""
	f.revision++
	if value.Kind != "function" {
		return
	}
	for _, definition := range f.definitions {
		if definition.Name == value.Value {
			f.definition = definition
			break
		}
	}
	for _, field := range f.definition.Fields {
		f.args[field.Name] = &templateArgument{text: field.Default, kind: "string"}
		if field.Kind == "boolean" {
			f.args[field.Name].text, f.args[field.Name].kind = "false", "boolean"
		}
	}
	for _, key := range slices.Sorted(maps.Keys(value.Arguments)) {
		arg := value.Arguments[key]
		if f.args[key] == nil {
			f.definition.Fields = append(slices.Clone(f.definition.Fields), engine.TemplateField{Name: key, Label: key, Kind: "text"})
		}
		entry := &templateArgument{text: arg.Value, kind: arg.Kind}
		if arg.Kind == "variable" || arg.Kind == "function" || arg.Kind == "null" {
			entry.text, entry.expression = engine.FormatTemplateExpression(arg), true
		}
		f.args[key] = entry
	}
	if f.name == "secure" {
		if arg, ok := value.Arguments["value"]; ok {
			f.secureExisting = arg.Value
		}
	}
}
func (f *templateForm) expression() (engine.TemplateExpression, error) {
	if f.name == "" {
		return engine.TemplateExpression{}, errors.New("choose a variable or function")
	}
	value := engine.TemplateExpression{Kind: f.kind, Value: f.name}
	if f.kind == "function" {
		for _, field := range f.definition.Fields {
			arg := f.args[field.Name]
			if (field.Kind == "request" || field.Kind == "file") && arg != nil && !arg.expression && strings.TrimSpace(arg.text) == "" {
				return value, fmt.Errorf("choose %s", strings.ToLower(templateFieldLabel(field)))
			}
		}
		value.Arguments = map[string]engine.TemplateExpression{}
		for key, arg := range f.args {
			v := engine.TemplateExpression{Kind: arg.kind, Value: arg.text}
			if arg.expression {
				var err error
				v, err = engine.ParseTemplateExpression(arg.text)
				if err != nil {
					return value, fmt.Errorf("%s: %w", key, err)
				}
			}
			value.Arguments[key] = v
		}
	}
	// Validate variable names and custom arguments before inserting a tag.
	_, err := engine.ParseTemplateExpression(engine.FormatTemplateExpression(value))
	return value, err
}

func (a *App) templateDialog(c *ui.Context, p colors) {
	f := a.templateForm
	if f == nil {
		return
	}
	ui.DialogBase(c, &f.open, func(back, panel *ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .4))
		panel.Key("template-dialog").Width(600).MaxWidthPercent(92).MaxHeightPercent(90).Padding(0).Gap(0).Radius(9).Border(1, p.border).Background(p.background)
		ui.Row(c).Padding(14, 18).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			ui.Text(c, "Variable or Function").FontSize(16).FontWeight(600).Grow(1)
			if iconButton(c, "close", "Close template editor").Clicked() {
				f.open = false
			}
		})
		ui.Scroll(c).MaxHeight(500).Padding(18).Gap(12).Children(func() {
			if f.name == "" {
				ui.TextInput(c, &f.search).Label("Find variable or function").Placeholder("Find variable or function").AutoFocus().FillWidth()
				ui.Scroll(c).Height(300).Gap(2).Children(func() {
					options := templateOptions("", f.variables, f.definitions)
					for i, option := range options {
						if !strings.Contains(strings.ToLower(option.name), strings.ToLower(f.search)) {
							continue
						}
						ui.Row(c).Key(i).Children(func() {
							if ui.Button(c, option.name).Label("Use " + option.name).FillWidth().Font("monospace").FontSize(12).Clicked() {
								value := engine.TemplateExpression{Kind: "variable", Value: option.name}
								if option.function != nil {
									value.Kind = "function"
								}
								f.selectExpression(value)
							}
						})
					}
				})
			} else {
				ui.Row(c).Gap(10).Children(func() {
					ui.Text(c, f.name).Font("monospace").FontSize(14).Grow(1)
					if ui.Button(c, "Change").FontSize(11).Clicked() {
						f.name, f.preview, f.error = "", "", ""
						f.revision++
					}
				})
				if f.definition.Description != "" && f.kind == "function" {
					ui.Text(c, f.definition.Description).TextColor(p.muted).FontSize(12)
				}
				for _, field := range f.definition.Fields {
					a.templateArgument(c, p, f, field)
				}
				if f.kind == "variable" {
					ui.Text(c, "Value").FontSize(11).TextColor(p.muted)
					ui.Text(c, f.variables[f.name]).Font("monospace").FontSize(12).MaxLines(5).Selectable()
				}
			}
			if f.error != "" {
				ui.Text(c, f.error).TextColor(p.red).FontSize(12).MaxLines(5).Selectable()
			}
			if f.preview != "" {
				ui.Text(c, "Preview").FontSize(11).TextColor(p.muted)
				ui.Text(c, f.preview).Font("monospace").FontSize(12).MaxLines(8).Selectable()
			}
		})
		ui.Row(c).Padding(12, 18).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
			if f.name != "" && (f.kind == "variable" || f.definition.Preview) {
				if ui.Button(c, "Preview").Disabled(f.busy).Clicked() {
					a.previewTemplate(f)
				}
			}
			ui.Spacer(c)
			if ui.Button(c, "Cancel").Clicked() {
				f.open = false
			}
			if ui.PrimaryButton(c, "Insert").Disabled(f.name == "" || f.busy).Clicked() {
				a.insertTemplate(f)
			}
		})
	})
	if !f.open {
		a.templateForm = nil
		f.doc.state.Focus()
	}
}

func (a *App) templateArgument(c *ui.Context, p colors, f *templateForm, field engine.TemplateField) {
	arg := f.args[field.Name]
	if arg == nil {
		return
	}
	ui.Column(c).Key(field.Name).Gap(5).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			label := templateFieldLabel(field)
			if f.name == "secure" && f.secureExisting != "" {
				label = "Encrypted value (replace to change)"
			}
			ui.Text(c, label).FontSize(12).TextColor(p.muted).Grow(1)
			if f.name != "secure" {
				mode := "Value"
				if arg.expression {
					mode = "Expression"
				}
				if ui.Select(c, &mode, []string{"Value", "Expression"}).Label(field.Name + " argument mode").Width(110).Height(26).FontSize(11).Changed() {
					if mode == "Expression" {
						arg.text = engine.FormatTemplateExpression(engine.TemplateExpression{Kind: arg.kind, Value: arg.text})
					}
					if mode == "Value" {
						if v, err := engine.ParseTemplateExpression(arg.text); err == nil && v.Kind == "string" {
							arg.text = v.Value
						}
						arg.kind = "string"
					}
					arg.expression = mode == "Expression"
					f.preview = ""
					f.revision++
				}
			}
		})
		changed := false
		if arg.expression {
			changed = ui.TextInput(c, &arg.text).Label(field.Name + " expression").Placeholder("variable or function(argument=…) ").Font("monospace").FillWidth().Changed()
		} else {
			switch field.Kind {
			case "boolean":
				on := arg.text == "true"
				changed = ui.Checkbox(c, &on, templateFieldLabel(field)).Changed()
				if changed {
					arg.text, arg.kind = fmt.Sprint(on), "boolean"
				}
			case "select":
				options := slices.Clone(field.Options)
				if arg.text != "" && !slices.Contains(options, arg.text) {
					options = append(options, arg.text)
				}
				changed = ui.Select(c, &arg.text, options).Label(templateFieldLabel(field)).FillWidth().Changed()
			case "request":
				requests := a.list("http_request")
				ids := []string{""}
				for _, request := range requests {
					ids = append(ids, s(request, "id"))
				}
				if arg.text != "" && !slices.Contains(ids, arg.text) {
					ids = append(ids, arg.text)
				}
				label := "Choose request"
				if arg.text != "" {
					label = arg.text
				}
				if m := a.models[arg.text]; m != nil {
					label = requestName(m)
				}
				ui.MenuButton(c, label, func(menu *ui.Menu) {
					for _, id := range ids {
						name := "Choose request"
						if id != "" {
							name = id
						}
						if m := a.models[id]; m != nil {
							name = requestName(m)
						}
						if menu.Item(name).Checked(arg.text == id).Chosen() {
							arg.text, changed = id, true
						}
					}
				}).Label(templateFieldLabel(field)).FillWidth()
			case "file":
				ui.Row(c).Gap(8).Children(func() {
					changed = ui.TextInput(c, &arg.text).Label(templateFieldLabel(field)).Grow(1).MinWidth(0).Changed()
					if ui.Button(c, "Browse…").Clicked() {
						a.background(func() (func(), error) {
							paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "Template File"})
							return func() {
								if f.open && len(paths) > 0 {
									arg.text = paths[0]
									f.preview = ""
									f.revision++
								}
							}, err
						})
					}
				})
			default:
				if field.Kind == "multiline" && !field.Secret {
					entry := ui.TextArea(c, &arg.text).Label(templateFieldLabel(field) + " argument").Height(90).Font("monospace").FontSize(12).FillWidth()
					if len(f.definition.Fields) > 0 && f.definition.Fields[0].Name == field.Name {
						entry.AutoFocus()
					}
					changed = entry.Changed()
				} else {
					entry := ui.TextInput(c, &arg.text).Label(templateFieldLabel(field) + " argument").FillWidth()
					if len(f.definition.Fields) > 0 && f.definition.Fields[0].Name == field.Name {
						entry.AutoFocus()
					}
					if field.Secret {
						entry.Password()
					}
					changed = entry.Changed()
				}
			}
		}
		if changed {
			if !arg.expression && field.Kind != "boolean" {
				arg.kind = "string"
			}
			f.preview, f.error = "", ""
			f.revision++
		}
	})
}

func (a *App) previewTemplate(f *templateForm) {
	value, err := f.expression()
	if err != nil {
		f.error = err.Error()
		return
	}
	f.busy, f.error, f.preview = true, "", ""
	source, scope, revision := engine.FormatTemplateTag(value), f.scope, f.revision
	a.run(func() (func(), error) {
		ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
		defer cancel()
		preview, err := a.Engine.PreviewTemplate(ctx, source, scope.workspace, scope.folder, scope.environment, scope.request, scope.cookieJar)
		return func() {
			f.busy = false
			if !f.open || f.revision != revision {
				return
			}
			if err != nil {
				f.error = err.Error()
			} else {
				text := []rune(preview)
				if len(text) > 4096 {
					preview = string(text[:4096]) + "…"
				}
				if preview == "" {
					preview = "(empty value)"
				}
				f.preview = preview
			}
		}, nil
	})
}

func (a *App) insertTemplate(f *templateForm) {
	value, err := f.expression()
	if err != nil {
		f.error = err.Error()
		return
	}
	if f.name == "secure" && (f.secureExisting == "" || f.args["value"].text != f.secureExisting) {
		plain, scope, revision := f.args["value"].text, f.scope, f.revision
		f.busy = true
		a.run(func() (func(), error) {
			tag, err := a.Engine.SecureValue(a.ctx, scope.workspace, plain)
			return func() {
				f.busy = false
				if !f.open || f.revision != revision {
					return
				}
				if err != nil {
					f.error = err.Error()
				} else {
					f.insert(tag)
				}
			}, nil
		})
		return
	}
	f.insert(engine.FormatTemplateTag(value))
}
func (f *templateForm) insert(tag string) {
	if f.doc.completionSource != f.source {
		f.error = "The field changed. Close this dialog and select the template again."
		return
	}
	f.doc.state.Replace(f.start, f.end, tag)
	f.doc.state.Focus()
	f.open = false
}
