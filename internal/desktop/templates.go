package desktop

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

type templateScope struct{ workspace, folder, environment, request, cookieJar string }
type templateVariableCache struct {
	version   uint64
	scope     templateScope
	variables map[string]string
}
type templateOption struct {
	name     string
	function *engine.TemplateDefinition
	// A preset (genericOptions) or a message field (jsonSchemaOptions)
	// replaces from up to end with apply; detail, when set, says what it is.
	generic     bool
	apply, info string
	detail      string
	from, end   int
}
type templateCompletion struct {
	analyzedSource  string
	analyzedCaret   int
	analyzedVersion uint64
	analyzedScope   templateScope
	analyzed, valid bool
	signature       string
	options         []templateOption
	start, end      int
	suppressed      string
	suppressedCaret int
	isSuppressed    bool
	explicit        bool
}

func (a *App) templateScope() templateScope {
	scope := templateScope{workspace: a.workspace, environment: a.environment, request: a.active, cookieJar: a.cookieJar}
	if d := a.drafts[a.active]; d != nil {
		scope.folder = s(d.Model, "folderId")
	}
	if a.dialogOpen {
		switch a.dialog {
		case "environments":
			scope.environment, scope.folder, scope.request = a.dialogID, "", ""
		case "scope":
			if a.scopeDraft != nil {
				scope.request = ""
				if a.scopeDraft.Kind == "folder" {
					scope.folder = a.scopeDraft.ID
				} else {
					scope.folder = ""
				}
			}
		}
	}
	return scope
}

func (a *App) templateVariables(scope templateScope) map[string]string {
	cache := &a.templateCache
	if cache.variables == nil || cache.version != a.modelVersion || cache.scope != scope {
		vars, _ := engine.VariablesFromModels(slices.Collect(maps.Values(a.models)), scope.workspace, scope.folder, scope.environment)
		*cache = templateVariableCache{version: a.modelVersion, scope: scope, variables: vars}
	}
	return cache.variables
}

func templateWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_.-", r)
}

// completionRange includes only an adjacent opener/closer, never a neighboring
// template or existing function arguments. All offsets belong to the native
// editor's rune-based selection model.
func completionRange(source string, caret int, explicit bool) (start, end int, prefix string, ok bool) {
	r := []rune(source)
	caret = max(0, min(caret, len(r)))
	start, end = caret, caret
	for start > 0 && templateWord(r[start-1]) {
		start--
	}
	for end < len(r) && templateWord(r[end]) {
		end++
	}
	prefix = string(r[start:caret])
	at := start
	for at > 0 && unicode.IsSpace(r[at-1]) {
		at--
	}
	opener := at >= 3 && string(r[at-3:at]) == "${["
	if !opener && !explicit && start > 0 && strings.ContainsRune("/:?&=#%@\\", r[start-1]) {
		return 0, 0, "", false
	}
	if !explicit && prefix == "" && !opener {
		return 0, 0, "", false
	}
	if _, found := templateTagAt(source, caret); found && !opener {
		return 0, 0, "", false
	}
	if opener {
		start = at - 3
		if end+2 <= len(r) && string(r[end:end+2]) == "()" {
			end += 2
		}
		close := end
		for close < len(r) && unicode.IsSpace(r[close]) {
			close++
		}
		if close+2 <= len(r) && string(r[close:close+2]) == "]}" {
			end = close + 2
		}
	}
	return start, end, prefix, true
}

func templateTags(source string) []ui.TextRange {
	r := []rune(source)
	var out []ui.TextRange
	for i := 0; i+2 < len(r); i++ {
		if string(r[i:i+3]) != "${[" {
			continue
		}
		quote, escaped := rune(0), false
		closed := false
		for j := i + 3; j+1 < len(r); j++ {
			if escaped {
				escaped = false
				continue
			}
			if quote != 0 {
				switch r[j] {
				case '\\':
					escaped = true
				case quote:
					quote = 0
				}
				continue
			}
			if r[j] == '\'' || r[j] == '"' {
				quote = r[j]
				continue
			}
			if r[j] == ']' && r[j+1] == '}' {
				out = append(out, ui.TextRange{Start: i, End: j + 2})
				closed = true
				i = j + 1
				break
			}
		}
		if !closed {
			out = append(out, ui.TextRange{Start: i, End: len(r)})
			break
		}
	}
	return out
}
func templateTagAt(source string, caret int) (ui.TextRange, bool) {
	for _, tag := range templateTags(source) {
		if caret >= tag.Start && caret <= tag.End {
			return tag, true
		}
	}
	return ui.TextRange{}, false
}
func templateOptions(prefix string, variables map[string]string, definitions []engine.TemplateDefinition) []templateOption {
	options := []templateOption{}
	prefix = strings.ToLower(prefix)
	for name := range variables {
		if name != "" && strings.HasPrefix(strings.ToLower(name), prefix) {
			options = append(options, templateOption{name: name})
		}
	}
	slices.SortFunc(options, func(a, b templateOption) int { return strings.Compare(a.name, b.name) })
	for _, d := range definitions {
		if strings.HasPrefix(strings.ToLower(d.Name), prefix) {
			options = append(options, templateOption{name: d.Name, function: &d})
		}
	}
	return options
}

// templateDoc is the editor state of the template input with key, in the open dialog's context.
func (a *App) templateDoc(key string) *documentEditor {
	context := ""
	if a.dialogOpen {
		context = a.dialog + ":" + a.dialogID
	}
	return a.editorDocument("input:" + context + ":" + key)
}

// revealRow shows or masks one obscured value.
func (a *App) revealRow(id string, on bool) {
	if a.revealed == nil {
		a.revealed = map[string]bool{}
	}
	a.revealed[id] = on
}

func (a *App) templateInput(c *ui.Context, p colors, value *string, key, label, placeholder string, readOnly bool) ui.Element {
	doc := a.templateDoc(key)
	input := ui.CodeEditor(c, value, &doc.state, ui.CodeEditorOptions{SingleLine: true, Font: "monospace", FontSize: 12, ReadOnly: readOnly}).Label(label).Placeholder(placeholder)
	palette := fmt.Sprint(p.accent)
	if doc.source != *value || doc.palette != palette {
		doc.source, doc.palette = *value, palette
		doc.spans = nil
		for _, tag := range templateTags(*value) {
			doc.spans = append(doc.spans, ui.CodeSpan{Start: tag.Start, End: tag.End, Color: p.accent})
		}
	}
	input.Syntax(doc.spans)
	input.ContextMenu(func(menu *ui.Menu) {
		if menu.Item("Undo").Disabled(readOnly).Shortcut(ui.Cmd, ui.KeyZ).Chosen() {
			doc.state.Command("undo")
		}
		if menu.Item("Redo").Disabled(readOnly).Shortcut(ui.Cmd|ui.Shift, ui.KeyZ).Chosen() {
			doc.state.Command("redo")
		}
		menu.Separator()
		for _, action := range []struct{ label, command string }{{"Cut", "cut"}, {"Copy", "copy"}, {"Paste", "paste"}, {"Select All", "selectAll"}} {
			if menu.Item(action.label).Disabled(readOnly && (action.command == "cut" || action.command == "paste")).Chosen() {
				doc.state.Command(action.command)
			}
		}
		if !readOnly {
			a.templateMenu(menu, doc, *value)
		}
	})
	if !readOnly {
		a.templateCompletion(c, p, input, doc, *value)
	}
	return input
}

func (a *App) templateMenu(menu *ui.Menu, doc *documentEditor, source string) {
	menu.Separator()
	if menu.Item("Insert Variable or Function…").Chosen() {
		a.openTemplateForm(doc, source, min(doc.state.Anchor, doc.state.Caret), max(doc.state.Anchor, doc.state.Caret), nil)
	}
	if tag, ok := templateTagAt(source, doc.state.Caret); ok && menu.Item("Edit Template…").Chosen() {
		expression, err := engine.ParseTemplateExpression(string([]rune(source)[tag.Start:tag.End]))
		if err == nil {
			a.openTemplateForm(doc, source, tag.Start, tag.End, &expression)
		} else {
			a.errorMessage = err.Error()
		}
	}
}

func (a *App) templateCompletion(c *ui.Context, p colors, editor ui.Element, doc *documentEditor, source string) {
	doc.completionSource = source
	v := &doc.completion
	requested, dismissed, chosen := doc.state.CompletionEvent()
	if dismissed {
		v.suppressed, v.suppressedCaret, v.explicit = source, doc.state.Caret, false
		v.isSuppressed = true
	}
	if chosen >= 0 && chosen < len(v.options) {
		option := v.options[chosen]
		if source != v.analyzedSource || doc.state.Caret != v.analyzedCaret {
			options, start, end, _ := a.completionOptions(doc, source, doc.state.Caret, v.explicit, a.templateScope())
			if len(options) == 0 {
				doc.state.Completions("", 0)
				return
			}
			option, v.start, v.end = options[0], start, end
		}
		a.acceptTemplate(doc, source, option)
		return
	}
	if requested {
		v.explicit = true
		v.isSuppressed = false
	}
	if !doc.state.Focused || editor.Composing() || a.templateForm != nil || doc.state.Anchor != doc.state.Caret || (v.isSuppressed && v.suppressed == source && v.suppressedCaret == doc.state.Caret) {
		doc.state.Completions("", 0)
		return
	}
	scope := a.templateScope()
	if !v.analyzed || requested || source != v.analyzedSource || doc.state.Caret != v.analyzedCaret || a.modelVersion != v.analyzedVersion || scope != v.analyzedScope {
		options, start, end, ok := a.completionOptions(doc, source, doc.state.Caret, v.explicit, scope)
		v.analyzed, v.valid = true, ok
		v.analyzedSource, v.analyzedCaret, v.analyzedVersion, v.analyzedScope = source, doc.state.Caret, a.modelVersion, scope
		v.start, v.end = start, end
		v.options = options
		v.signature = fmt.Sprintf("%d:%d:%d:%d", start, end, doc.state.Caret, len(v.options))
	}
	if !v.valid {
		v.explicit = false
		doc.state.Completions("", 0)
		return
	}
	doc.state.Completions(v.signature, len(v.options))
	if len(v.options) == 0 {
		return
	}
	ui.Overlay(c, func() {
		panel := ui.Column(c).Key(fmt.Sprintf("templates:%d", editor.ID())).EditorSuggestions(editor).Width(370).MaxWidthPercent(90).Padding(4).Radius(6).Background(p.background).Border(1, p.border).Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, .22)).Gap(1)
		panel.Children(func() {
			selected := doc.state.CompletionIndex()
			first := max(0, min(selected-5, len(v.options)-8))
			for i := first; i < min(first+8, len(v.options)); i++ {
				option := v.options[i]
				row := ui.Row(c).Key(i).Height(31).Padding(4, 8).Gap(10).Radius(3).Label("Complete "+option.name).EditorSuggestion(editor, i, len(v.options), i == selected)
				if i == selected || row.Hovered() {
					row.Background(p.accent.Alpha(.16))
				}
				row.Children(func() {
					ui.Text(c, option.name).Font("monospace").FontSize(12).Grow(1).MaxLines(1)
					kind := "variable"
					switch {
					case option.detail != "":
						kind = option.detail
					case option.generic:
						kind = "constant"
					case option.function != nil:
						kind = "function"
					}
					ui.Text(c, kind).TextColor(p.muted).FontSize(10)
				})
				if row.Clicked() {
					a.acceptTemplate(doc, source, option)
				}
			}
			if len(v.options) > 8 {
				ui.Text(c, fmt.Sprintf("%d / %d", selected+1, len(v.options))).FontSize(10).TextColor(p.muted).Padding(3, 8)
			}
		})
		if panel.PressedOutside() {
			v.suppressed, v.suppressedCaret, v.explicit = source, doc.state.Caret, false
			v.isSuppressed = true
			doc.state.Completions("", 0)
		}
	})
}

func (a *App) acceptTemplate(doc *documentEditor, source string, option templateOption) {
	v := &doc.completion
	if option.generic {
		doc.state.Replace(option.from, option.end, option.apply)
		doc.state.Focus()
		v.options, v.explicit = nil, false
		// Until the field changes again, as the caret ends after the preset.
		r := []rune(source)
		v.suppressed = string(r[:option.from]) + option.apply + string(r[option.end:])
		v.suppressedCaret = option.from + utf8.RuneCountInString(option.apply)
		v.isSuppressed = true
		return
	}
	expression := engine.TemplateExpression{Kind: "variable", Value: option.name}
	if option.function != nil {
		expression.Kind = "function"
		if len(option.function.Fields) > 0 {
			a.openTemplateForm(doc, source, v.start, v.end, &expression)
		} else {
			doc.state.Replace(v.start, v.end, engine.FormatTemplateTag(expression))
			doc.state.Focus()
		}
	} else {
		doc.state.Replace(v.start, v.end, engine.FormatTemplateTag(expression))
		doc.state.Focus()
	}
	v.options, v.explicit = nil, false
	v.suppressed, v.suppressedCaret = source, doc.state.Caret
	v.isSuppressed = true
	doc.state.Completions("", 0)
}

func templateFieldLabel(field engine.TemplateField) string { return cmp.Or(field.Label, field.Name) }

func templateSyntax(spans []ui.CodeSpan, source string, color ui.Color) []ui.CodeSpan {
	tags := templateTags(source)
	if len(tags) == 0 {
		return spans
	}
	out := make([]ui.CodeSpan, 0, len(spans)+len(tags)*2)
	first := 0
	for _, span := range spans {
		for first < len(tags) && tags[first].End <= span.Start {
			first++
		}
		at := span.Start
		for i := first; i < len(tags) && tags[i].Start < span.End; i++ {
			tag := tags[i]
			if at < tag.Start {
				part := span
				part.Start, part.End = at, tag.Start
				out = append(out, part)
			}
			out = append(out, ui.CodeSpan{Start: max(at, tag.Start), End: min(span.End, tag.End), Color: color, String: true})
			at = min(span.End, tag.End)
		}
		if at < span.End {
			span.Start = at
			out = append(out, span)
		}
	}
	return out
}

// completionOptions lists a field's presets, matching all of it, before
// the variables and functions matching the word at the caret.
func (a *App) completionOptions(doc *documentEditor, source string, caret int, explicit bool, scope templateScope) (options []templateOption, start, end int, ok bool) {
	options = genericOptions(doc.presets, doc.presetMin, source, caret, explicit)
	if doc.jsonSchema != nil {
		options = append(options, jsonSchemaOptions(doc.jsonSchema, source, caret)...)
	}
	start, end, prefix, inWord := completionRange(source, caret, explicit)
	if inWord {
		options = append(options, templateOptions(prefix, a.templateVariables(scope), a.Engine.TemplateDefinitions())...)
	}
	return options, start, end, inWord || len(options) > 0
}
