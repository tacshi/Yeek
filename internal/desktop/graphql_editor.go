package desktop

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

type graphQLCompletionState struct {
	signature                 string
	source, dismissedSource   string
	caret, dismissedCaret     int
	index                     *engine.GraphQLIndex
	result                    engine.GraphQLCompletion
	analyzed, open, dismissed bool
}

func (a *App) graphQLCompletion(c *ui.Context, p colors, editor *ui.Element, doc *documentEditor, source string) {
	doc.completionSource = source
	provider := "graphql"
	if _, ok := templateTagAt(source, doc.state.Caret); ok {
		provider = "template"
	}
	if provider != doc.completionProvider {
		doc.state.CompletionEvent()
		doc.state.Completions("", 0)
		doc.completionProvider = provider
	}
	if provider == "template" {
		a.templateCompletion(c, p, editor, doc, source)
		return
	}
	v := &doc.graphqlCompletion
	requested, dismissed, chosen := doc.state.CompletionEvent()
	if dismissed {
		v.open = false
		v.dismissed = true
		v.dismissedSource, v.dismissedCaret = source, doc.state.Caret
	}
	if requested {
		v.open = true
		v.dismissed = false
	}
	if !doc.state.Focused || editor.Composing() || a.templateForm != nil || doc.state.Anchor != doc.state.Caret {
		doc.state.Completions("", 0)
		return
	}
	var index *engine.GraphQLIndex
	if d := a.drafts[a.active]; d != nil {
		index = a.graphQLIndex(d)
	}
	changed := v.source != source || v.caret != doc.state.Caret || v.index != index || !v.analyzed
	if changed {
		v.analyzed = true
		v.source, v.caret, v.index = source, doc.state.Caret, index
		v.result = index.Complete(source, doc.state.Caret)
		v.signature = fmt.Sprintf("graphql:%d:%d:%s", v.result.Start, v.result.End, string([]rune(source)[v.result.Start:v.result.End]))
		if chosen >= 0 {
			chosen = 0
		}
	}
	if chosen >= 0 && chosen < len(v.result.Options) {
		acceptGraphQL(doc, v.result.Options[chosen])
		return
	}
	if editor.Changed() {
		at := min(doc.state.Caret, utf8.RuneCountInString(source))
		r := []rune(source)
		before := strings.TrimRight(string(r[:at]), " \t\r\n")
		trigger := v.result.Start < at
		if before != "" && strings.ContainsAny(before[len(before)-1:], "{(:@$.") {
			trigger = true
		}
		v.open = trigger
	}
	if !v.open || v.dismissed && v.dismissedSource == source && v.dismissedCaret == doc.state.Caret {
		doc.state.Completions("", 0)
		return
	}
	options := v.result.Options
	doc.state.Completions(v.signature, len(options))
	if len(options) == 0 {
		return
	}
	ui.Overlay(c, func() {
		panel := ui.Column(c).Key(fmt.Sprintf("graphql-completion:%d", editor.ID())).EditorSuggestions(editor).Width(410).MaxWidthPercent(92).Padding(4).Radius(6).Background(p.background).Border(1, p.border).Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, .2))
		panel.Children(func() {
			selected := doc.state.CompletionIndex()
			first := max(0, min(selected-5, len(options)-8))
			for i := first; i < min(first+8, len(options)); i++ {
				option := options[i]
				row := ui.Row(c).Key(i).Height(30).Padding(4, 8).Gap(10).Radius(3).Label("Complete GraphQL "+option.Label).EditorSuggestion(editor, i, len(options), i == selected)
				if i == selected || row.Hovered() {
					row.Background(p.accent.Alpha(.16))
				}
				row.Children(func() {
					ui.Text(c, option.Label).Font("monospace").FontSize(12).Grow(1).SingleLine()
					detail := option.Detail
					if option.Deprecated {
						detail += " · deprecated"
					}
					ui.Text(c, detail).FontSize(10).TextColor(p.muted).SingleLine()
				})
				if row.Clicked() {
					acceptGraphQL(doc, option)
				}
			}
			option := options[min(selected, len(options)-1)]
			if option.Description != "" {
				ui.Text(c, option.Description).FontSize(11).TextColor(p.muted).MaxLines(4).Padding(8)
			}
			if option.ParentType != "" {
				if ui.Button(c, "Show in Documentation").FontSize(10).Clicked() {
					a.openGraphQLDocs(a.active, option.ParentType)
					v.open = false
				}
			}
		})
		if panel.PressedOutside() {
			v.open = false
			v.dismissed = true
			v.dismissedSource, v.dismissedCaret = source, doc.state.Caret
			doc.state.Completions("", 0)
		}
	})
}

func acceptGraphQL(doc *documentEditor, option engine.GraphQLSuggestion) {
	v := &doc.graphqlCompletion
	doc.state.Replace(v.result.Start, v.result.End, option.Insert)
	if option.Caret >= 0 {
		caret := v.result.Start + option.Caret
		doc.state.Select(caret, caret)
	}
	doc.state.Focus()
	doc.state.Completions("", 0)
	v.open = false
	v.dismissed = true
	v.dismissedSource = string([]rune(v.source)[:v.result.Start]) + option.Insert + string([]rune(v.source)[v.result.End:])
	v.dismissedCaret = v.result.Start + utf8.RuneCountInString(option.Insert)
	if option.Caret >= 0 {
		v.dismissedCaret = v.result.Start + option.Caret
	}
}

func (a *App) graphQLMarks(label string, p colors) []ui.CodeMark {
	if label != "GraphQL query" {
		return nil
	}
	state := a.graphQLState(a.active)
	marks := []ui.CodeMark{}
	for _, issue := range state.diagnostics {
		if !issue.Variables && issue.End > issue.Start {
			marks = append(marks, ui.CodeMark{Start: issue.Start, End: issue.End, Color: p.red})
		}
	}
	slices.SortFunc(marks, func(a, b ui.CodeMark) int { return cmp.Compare(a.Start, b.Start) })
	return marks
}
