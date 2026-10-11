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

// templateArgument is a value of Yaak's DynamicForm: text, which may hold
// template tags, or a boolean.
type templateArgument struct {
	text, kind string
}
type templateForm struct {
	open, busy     bool
	doc            *documentEditor
	source         string
	start, end     int
	scope          templateScope
	definitions    []engine.TemplateDefinition
	variables      map[string]string
	name, kind     string
	definition     engine.TemplateDefinition
	args           map[string]*templateArgument
	error          string
	secureExisting string
	revision       uint64
	// advanced opens Yaak's Advanced accordion; rendered is the values
	// with their tags rendered, for the banner, as of renderedAt.
	advanced   bool
	rendered   map[string]string
	renderedAt uint64
	// dynamic is the options of selects that list them for the other values.
	dynamic map[string]dynamicOptions
	// Yaak's Rendered Preview: the revision rendered, when it last changed,
	// whether a refresh asked for one, and whether to show secrets in it.
	previewRevision  uint64
	changedAt        time.Time
	refresh, secrets bool
	rendering        bool
	previewText      string
	// focusFirst focuses the first argument once, as AutoFocus does.
	focusFirst bool
}

type dynamicOptions struct{ options, labels []string }

// shown reports whether a field shows for the form's values.
func (f *templateForm) shown(field engine.TemplateField, values map[string]string) bool {
	if field.Visible != nil && !field.Visible(values) {
		return false
	}
	if field.Describe != nil {
		if _, _, _, visible := field.Describe(values); !visible {
			return false
		}
	}
	return field.Dynamic == nil || len(f.dynamic[field.Name].options) > 0
}

// values are the form's arguments as entered.
func (f *templateForm) values() map[string]string {
	values := map[string]string{}
	for name, arg := range f.args {
		values[name] = arg.text
	}
	return values
}

// openTemplateForm is Yaak's TemplateFunctionDialog.show, for a function's
// tag between start and end.
func (a *App) openTemplateForm(doc *documentEditor, source string, start, end int, value *engine.TemplateExpression) {
	if value == nil || value.Kind != "function" {
		return
	}
	scope := a.templateScope()
	f := &templateForm{open: true, doc: doc, source: source, start: start, end: end, scope: scope, definitions: a.Engine.TemplateDefinitions(), variables: a.templateVariables(scope)}
	f.selectExpression(*value)
	f.previewRevision = ^uint64(0)
	f.focusFirst = true
	a.templateForm = f
	doc.state.Completions("", 0)
	if f.name == "secure" && f.secureExisting != "" {
		// Yaak's dialog shows secure()'s value decrypted, to edit.
		encrypted, workspace := f.secureExisting, scope.workspace
		a.run(func() (func(), error) {
			plain, err := a.Engine.DecryptValue(a.ctx, workspace, encrypted)
			return func() {
				if err == nil && a.templateForm == f {
					f.secureExisting = plain
					f.args["value"].text = plain
				}
			}, err
		})
	}
}

// editVariable is Yaak's onClickVariable and onClickMissingVariable: the
// environment that has the variable, or the active one with it added, in
// the environment editor.
func (a *App) editVariable(name string) {
	base, subs := a.environmentsBreakdown()
	var candidates []engine.Object
	if env := a.models[a.environment]; env != nil {
		candidates = append(candidates, env)
	}
	if base != nil {
		candidates = append(candidates, base)
	}
	candidates = append(candidates, subs...)
	for _, env := range candidates {
		for _, row := range kvRows(env, "variables") {
			if row.Name == name {
				a.openEnvironmentsAt(s(env, "id"))
				return
			}
		}
	}
	target := a.models[a.environment]
	if target == nil {
		target = base
	}
	if target == nil {
		return
	}
	a.openEnvironmentsAt(s(target, "id"))
	a.envDraft = append(a.envDraft, KV{Name: name, Enabled: true})
}
func (f *templateForm) selectExpression(value engine.TemplateExpression) {
	f.name, f.kind, f.error = value.Value, value.Kind, ""
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
		switch arg.Kind {
		case "variable", "function":
			// A nested value is text with its tag, which renders the same.
			entry.text, entry.kind = engine.FormatTemplateTag(arg), "string"
		case "null":
			entry.text, entry.kind = f.args[key].text, "string"
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
			if (field.Kind == "request" || field.Kind == "file") && arg != nil && strings.TrimSpace(arg.text) == "" {
				return value, fmt.Errorf("choose %s", strings.ToLower(templateFieldLabel(field)))
			}
		}
		value.Arguments = map[string]engine.TemplateExpression{}
		for key, arg := range f.args {
			value.Arguments[key] = engine.TemplateExpression{Kind: arg.kind, Value: arg.text}
		}
	}
	// Validate variable names and custom arguments before inserting a tag.
	_, err := engine.ParseTemplateExpression(engine.FormatTemplateExpression(value))
	return value, err
}

// templateDialog is Yaak's TemplateFunctionDialog: the function's form,
// its Rendered Preview, and Save.
func (a *App) templateDialog(c *ui.Context, p colors) {
	f := a.templateForm
	if f == nil {
		return
	}
	ui.DialogBase(c, &f.open, func(back, panel ui.Element) {
		dialogEnter(back, panel)
		back.Background(ui.RGBA(0, 0, 0, .4))
		// Yaak's md dialog, h-240.
		panel.Key("template-dialog").Width(800).HeightPercent(90).MaxHeight(960).MaxWidthPercent(92).Padding(0).Gap(0).Radius(9).Border(1, p.border).Background(p.background)
		ui.Column(c).Padding(16, 24, 8, 24).Gap(8).Children(func() {
			ui.Row(c).Gap(10).Children(func() {
				label := f.name + "()"
				if len(f.definition.Fields) > 0 {
					label = f.name + "(…)"
				}
				ui.Text(c, label).Font("monospace").FontSize(14).Padding(1, 6).Radius(4).Background(p.border.Alpha(.3)).Border(1, p.border)
				ui.Box(c).Grow(1)
				if iconButton(c, "close", "Close dialog").Clicked() {
					f.open = false
				}
			})
			if f.definition.Description != "" {
				ui.Text(c, f.definition.Description).FontSize(13).TextColor(p.muted).MaxLines(4)
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(8, 24).Gap(12).Children(func() {
			if f.name == "secure" {
				// Yaak's secure() form: one password input.
				arg := f.args["value"]
				if arg == nil {
					arg = &templateArgument{kind: "string"}
					f.args["value"] = arg
				}
				ui.Column(c).Gap(5).Children(func() {
					ui.Text(c, "Value").FontSize(12).TextColor(p.muted)
					field := ui.TextInput(c, &arg.text).Label("Value").Placeholder("••••••••••••").Password().AutoFocus().FillWidth()
					if field.Changed() {
						f.revision++
					}
					if field.Submitted() {
						a.insertTemplate(f)
					}
				})
			} else {
				values := f.values()
				hasAdvanced := false
				for _, field := range f.definition.Fields {
					if field.Advanced {
						hasAdvanced = true
					} else if f.shown(field, values) {
						a.templateArgument(c, p, f, field)
					}
				}
				a.renderTemplateValues(f)
				a.templateBanner(c, p, f)
				if hasAdvanced {
					// Yaak's Advanced accordion.
					toggle := ui.ButtonBase(c).Label("Advanced").Gap(6).AlignSelf(ui.Start)
					toggle.Children(func() {
						disclosure(c, f.advanced, 11, p.subtle)
						ui.Text(c, "Advanced").FontSize(13)
					})
					if toggle.Clicked() {
						f.advanced = !f.advanced
					}
					if f.advanced {
						for _, field := range f.definition.Fields {
							if field.Advanced && f.shown(field, values) {
								a.templateArgument(c, p, f, field)
							}
						}
					}
				}
			}
		})
		a.templatePreview(c, p, f)
		dialogFooter(c, p, nil, func() {
			if f.name == "secure" && ui.Button(c, "Reveal Encryption Key").Clicked() {
				// Yaak's setupOrConfigureEncryption.
				f.open = false
				a.prompt("workspace_encryption", "Workspace Encryption", "", f.scope.workspace)
			}
			if ui.PrimaryButton(c, "Save").Disabled(f.busy).Clicked() {
				a.insertTemplate(f)
			}
		})
	})
	if !f.open {
		a.templateForm = nil
		f.doc.state.Focus()
	}
}

// templatePreviewType is Yaak's previewType: prompt.text renders when asked
// ("click"), the rest as their values change ("live").
func templatePreviewType(name string) string {
	if strings.HasPrefix(name, "prompt.") {
		return "click"
	}
	return "live"
}

// templatePreview is Yaak's Rendered Preview: the tag rendered as its
// values change (400ms after), hiding values typed into password fields
// until the lock shows them.
func (a *App) templatePreview(c *ui.Context, p colors, f *templateForm) {
	kind := templatePreviewType(f.name)
	if f.revision != f.previewRevision {
		if f.changedAt.IsZero() {
			f.changedAt = time.Now()
		}
		wait := 400 * time.Millisecond
		if a.testMode {
			wait = 0
		}
		if elapsed := time.Since(f.changedAt); elapsed < wait {
			c.After(wait - elapsed)
		} else if kind == "live" || f.refresh {
			f.previewRevision, f.changedAt, f.refresh = f.revision, time.Time{}, false
			// A "click" preview renders as sending does (Yaak's purpose "send"), prompting.
			a.previewTemplate(f, kind == "click")
		}
	}
	secret := false
	for _, field := range f.definition.Fields {
		if arg := f.args[field.Name]; field.Secret && arg != nil && arg.text != "" && strings.Contains(f.previewText, arg.text) {
			secret = true
		}
	}
	if f.name == "secure" {
		if arg := f.args["value"]; arg != nil && arg.text != "" && strings.Contains(f.previewText, arg.text) {
			secret = true
		}
	}
	ui.Column(c).Padding(12, 24, 12, 24).Gap(6).BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, "Rendered Preview").FontSize(13).TextColor(p.muted)
			if f.rendering {
				ui.Spinner(c).Size(10, 10).TextColor(p.subtle)
			}
			ui.Box(c).Grow(1)
			label, glyph := "Hide preview", "lockOpen"
			if f.secrets {
				label, glyph = "Show preview", "lock"
			}
			lock := smallIconButton(c, glyph, label).TextColor(p.subtle)
			if !secret {
				lock.Opacity(0).Disabled(true)
			}
			if lock.Clicked() {
				f.secrets = !f.secrets
			}
		})
		ui.Row(c).FillWidth().Radius(4).Border(1, p.subtle.Alpha(.6)).Background(p.border.Alpha(.25)).Padding(4, 4, 4, 8).Gap(6).Children(func() {
			ui.Scroll(c).Grow(1).MinWidth(0).MaxHeight(160).Children(func() {
				content, italic, color := f.previewText, false, p.text
				switch {
				case f.error != "":
					content, italic, color = strings.TrimPrefix(f.error, "Render Error: "), true, p.red
				case secret && !f.secrets:
					content, italic, color = "------ sensitive values hidden ------", true, p.muted
				case len(f.previewText) > 10000:
					content, italic, color = "too large to preview", true, p.red
				}
				text := ui.Text(c, content+" ").Font("monospace").FontSize(12).TextColor(color).Selectable()
				if italic {
					text.Italic()
				}
			})
			if smallIconButton(c, "refresh", "Refresh preview").TextColor(p.muted).AlignSelf(ui.Center).Clicked() {
				// Render now, whatever the preview type.
				f.refresh = true
				f.previewRevision = ^uint64(0)
				f.changedAt = time.Unix(0, 0)
			}
		})
	})
}

func (a *App) templateArgument(c *ui.Context, p colors, f *templateForm, field engine.TemplateField) {
	arg := f.args[field.Name]
	if arg == nil {
		return
	}
	description := ""
	if field.Describe != nil {
		label, about, secret, _ := field.Describe(f.values())
		field.Label, description, field.Secret = label, about, secret
	}
	if d, ok := f.dynamic[field.Name]; ok && field.Dynamic != nil {
		field.Options, field.OptionLabels = d.options, d.labels
	}
	ui.Column(c).Key(field.Name).Gap(5).Children(func() {
		defer func() {
			if description != "" {
				ui.Text(c, description).FontSize(11).TextColor(p.muted).MaxLines(4)
			}
		}()
		ui.Row(c).Gap(8).Children(func() {
			label := templateFieldLabel(field)
			if f.name == "secure" && f.secureExisting != "" {
				label = "Encrypted value (replace to change)"
			}
			ui.Text(c, label).FontSize(12).TextColor(p.muted).Grow(1)
		})
		changed := false
		{
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
				labels := slices.Clone(options)
				for i := range min(len(field.OptionLabels), len(labels)) {
					labels[i] = field.OptionLabels[i]
				}
				chosen := labels[max(0, slices.Index(options, arg.text))]
				if ui.Select(c, &chosen, labels).Label(templateFieldLabel(field)).FillWidth().Changed() {
					arg.text, changed = options[slices.Index(labels, chosen)], true
				}
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
									f.revision++
								}
							}, err
						})
					}
				})
			default:
				// Yaak's DynamicForm text inputs take template tags, with
				// their autocompletion.
				before := arg.text
				label := templateFieldLabel(field) + " argument"
				first := len(f.definition.Fields) > 0 && f.definition.Fields[0].Name == field.Name
				switch {
				case field.Secret:
					entry := ui.TextInput(c, &arg.text).Label(label).Placeholder(field.Placeholder).Password().FillWidth()
					if first {
						entry.AutoFocus()
					}
				case field.Kind == "multiline":
					ui.Box(c).Height(90).FillWidth().Radius(4).Border(1, p.border).Children(func() {
						a.nativeEditor(c, p, &arg.text, label, "text", false)
					})
					if first && f.focusFirst {
						f.focusFirst = false
						a.editorDocument(label).state.Focus()
					}
				default:
					entry := a.templateInput(c, p, &arg.text, "template:"+f.name+":"+field.Name, label, field.Placeholder, false).FillWidth().Height(30).Radius(4).Border(1, p.border)
					if first && f.focusFirst {
						f.focusFirst = false
						a.templateDoc("template:" + f.name + ":" + field.Name).state.Focus()
					}
					_ = entry
				}
				changed = arg.text != before
			}
		}
		if changed {
			if field.Kind != "boolean" {
				arg.kind = "string"
			}
			f.error = ""
			f.revision++
		}
	})
}

func (a *App) previewTemplate(f *templateForm, send bool) {
	value, err := f.expression()
	if err != nil {
		f.error = err.Error()
		return
	}
	f.rendering, f.error = true, ""
	source, scope, revision := engine.FormatTemplateTag(value), f.scope, f.revision
	a.background(func() (func(), error) {
		var preview string
		var err error
		if send {
			preview, err = a.Engine.Render(engine.WithTemplatePrompter(a.ctx, a.templatePrompter), source, scope.workspace, scope.folder, scope.environment)
		} else {
			ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
			defer cancel()
			preview, err = a.Engine.PreviewTemplate(ctx, source, scope.workspace, scope.folder, scope.environment, scope.request, scope.cookieJar)
		}
		return func() {
			f.rendering = false
			if !f.open || f.revision != revision {
				return
			}
			f.previewText = ""
			if err != nil {
				f.error = err.Error()
			} else {
				f.previewText = preview
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

// renderTemplateValues renders the tags in a function's values once they
// change, for its banner and the options its selects list.
func (a *App) renderTemplateValues(f *templateForm) {
	needed := f.definition.Banner != nil || slices.ContainsFunc(f.definition.Fields, func(field engine.TemplateField) bool { return field.Dynamic != nil })
	if !needed || f.renderedAt == f.revision {
		return
	}
	f.renderedAt = f.revision
	values, scope, revision := f.values(), f.scope, f.revision
	a.run(func() (func(), error) {
		ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
		defer cancel()
		for name, value := range values {
			if strings.Contains(value, "${[") {
				if rendered, err := a.Engine.PreviewTemplate(ctx, value, scope.workspace, scope.folder, scope.environment, scope.request, scope.cookieJar); err == nil {
					values[name] = rendered
				}
			}
		}
		return func() {
			if f.revision != revision {
				return
			}
			f.rendered = values
			a.loadDynamicOptions(f, values)
		}, nil
	})
}

// loadDynamicOptions lists the options of selects such as 1password.item's
// vaults, which fail quietly, as Yaak hides them.
func (a *App) loadDynamicOptions(f *templateForm, values map[string]string) {
	for _, field := range f.definition.Fields {
		if field.Dynamic == nil {
			continue
		}
		load, name, revision := field.Dynamic, field.Name, f.revision
		a.background(func() (func(), error) {
			ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
			defer cancel()
			options, labels, err := load(ctx, values)
			return func() {
				if f.revision != revision {
					return
				}
				if f.dynamic == nil {
					f.dynamic = map[string]dynamicOptions{}
				}
				if err != nil {
					options, labels = nil, nil
				}
				f.dynamic[name] = dynamicOptions{options, labels}
			}, nil
		})
	}
}

// templateBanner shows a function's banner, as for where prompt.text
// stores its value, with the values' tags rendered.
func (a *App) templateBanner(c *ui.Context, p colors, f *templateForm) {
	if f.definition.Banner == nil {
		return
	}
	values := f.rendered
	if values == nil {
		values = f.values()
	}
	text, danger := f.definition.Banner(values)
	if text == "" {
		return
	}
	color := p.blue
	if danger {
		color = p.red
	}
	ui.Text(c, text).FontSize(12).Padding(8, 10).Radius(5).Border(1, color.Alpha(.45)).Background(color.Alpha(.08)).TextColor(color).MaxLines(4).Selectable().FillWidth()
}
