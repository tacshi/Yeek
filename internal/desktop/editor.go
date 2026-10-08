package desktop

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/egoist/mygo/yeekui"
)

type documentEditor struct {
	completionProvider                                     string
	graphqlCompletion                                      graphQLCompletionState
	completionSource                                       string
	completion                                             templateCompletion
	state                                                  ui.CodeEditorState
	find, replace, goTo                                    bool
	query, replacement, lineTarget                         string
	matchCase, regex                                       bool
	source, syntaxLanguage, palette, findSource, findQuery string
	spans                                                  []ui.CodeSpan
	matches                                                []ui.TextRange
	captures                                               [][]int
	pattern                                                *regexp.Regexp
	matchIndex                                             int
	findError                                              string
	findCase, findRegex                                    bool
}

func (a *App) editorDocument(label string) *documentEditor {
	if a.editors == nil {
		a.editors = map[string]*documentEditor{}
	}
	key := a.active + ":" + label
	doc := a.editors[key]
	if doc == nil {
		doc = &documentEditor{matchIndex: -1}
		a.editors[key] = doc
	}
	return doc
}
func (a *App) nativeEditor(c *ui.Context, p colors, value *string, label, language string, readOnly bool) bool {
	doc := a.editorDocument(label)
	changed := false
	scope := ui.Column(c).Key(a.active + ":" + label).Grow(1).MinHeight(0).MinWidth(0)
	scope.Children(func() {
		if scope.Shortcut(ui.Cmd, ui.KeyF) {
			doc.find = true
		}
		if scope.Shortcut(ui.Cmd|ui.Alt, ui.KeyF) && !readOnly {
			doc.find = true
			doc.replace = true
		}
		if scope.Shortcut(ui.Ctrl, ui.KeyG) {
			doc.goTo = true
		}
		if scope.Shortcut(0, ui.KeyF3) || scope.Shortcut(ui.Cmd, ui.KeyG) {
			doc.nextMatch(1)
		}
		if scope.Shortcut(ui.Shift, ui.KeyF3) || scope.Shortcut(ui.Cmd|ui.Shift, ui.KeyG) {
			doc.nextMatch(-1)
		}
		if scope.Shortcut(0, ui.KeyEscape) {
			doc.find = false
			doc.goTo = false
			doc.state.Focus()
		}
		if doc.find {
			a.editorFindBar(c, p, doc, value, label, readOnly)
		}
		if doc.goTo {
			ui.Row(c).Padding(5, 10).Gap(8).Children(func() {
				ui.Text(c, "Go to line").FontSize(11).TextColor(p.muted)
				input := ui.TextInput(c, &doc.lineTarget).Label("Line and column").Placeholder("line:column").Width(130).AutoFocus()
				goTo := input.Submitted()
				if ui.Button(c, "Go").FontSize(11).Clicked() {
					goTo = true
				}
				if iconButton(c, "close", "Close line navigation").Clicked() {
					doc.goTo = false
					doc.state.Focus()
				}
				if goTo {
					line, column, _ := strings.Cut(doc.lineTarget, ":")
					n, err := strconv.Atoi(line)
					col := 1
					if column != "" {
						col, err = strconv.Atoi(column)
					}
					if err == nil && n > 0 && col > 0 {
						doc.state.GoTo(n, col)
						doc.goTo = false
					}
				}
			})
		}
		font := cmp.Or(s(a.settings, "editorFont"), "monospace")
		size := float32(n(a.settings, "editorFontSize"))
		if size <= 0 {
			size = 12
		}
		options := ui.CodeEditorOptions{Font: font, FontSize: size, Wrap: b(a.settings, "editorSoftWrap"), ReadOnly: readOnly, LineNumbers: language != "markdown", TabSize: 2, AutoIndent: !readOnly, CloseBrackets: !readOnly && language != "text", CommentPrefix: commentPrefix(language)}
		ui.Column(c).Key("document").Grow(1).MinHeight(0).MinWidth(0).Children(func() {
			editor := ui.CodeEditor(c, value, &doc.state, options).Label(label).Grow(1).MinHeight(0)
			palette := fmt.Sprint(p.text, p.accent, p.orange, p.blue, p.muted)
			if doc.source != *value || doc.syntaxLanguage != language || doc.palette != palette {
				doc.source = *value
				doc.syntaxLanguage = language
				doc.palette = palette
				doc.spans = syntaxSpans(*value, language, p)
				if !readOnly {
					doc.spans = templateSyntax(doc.spans, *value, p.accent)
				}
				doc.state.SetFolds(editorFolds(*value, language))
			}
			editor.Syntax(doc.spans)
			if !readOnly {
				editor.Diagnostics(a.graphQLMarks(label, p))
			}
			if doc.find {
				doc.updateMatches(*value)
				marks := make([]ui.CodeMark, 0, len(doc.matches))
				for i, m := range doc.matches {
					color := p.orange.Alpha(.17)
					if i == doc.matchIndex {
						color = p.accent.Alpha(.3)
					}
					marks = append(marks, ui.CodeMark{Start: m.Start, End: m.End, Color: color})
				}
				editor.Marks(marks)
			}
			editor.ContextMenu(func(menu *ui.Menu) {
				if menu.Item("Undo").Disabled(readOnly).Shortcut(ui.Cmd, ui.KeyZ).Chosen() {
					doc.state.Command("undo")
				}
				if menu.Item("Redo").Disabled(readOnly).Shortcut(ui.Cmd|ui.Shift, ui.KeyZ).Chosen() {
					doc.state.Command("redo")
				}
				menu.Separator()
				if menu.Item("Cut").Disabled(readOnly).Shortcut(ui.Cmd, ui.KeyX).Chosen() {
					doc.state.Command("cut")
				}
				if menu.Item("Copy").Shortcut(ui.Cmd, ui.KeyC).Chosen() {
					doc.state.Command("copy")
				}
				if menu.Item("Paste").Disabled(readOnly).Shortcut(ui.Cmd, ui.KeyV).Chosen() {
					doc.state.Command("paste")
				}
				if menu.Item("Select All").Shortcut(ui.Cmd, ui.KeyA).Chosen() {
					doc.state.Command("selectAll")
				}
				menu.Separator()
				if menu.Item("Find…").Shortcut(ui.Cmd, ui.KeyF).Chosen() {
					doc.find = true
				}
				if menu.Item("Replace…").Disabled(readOnly).Shortcut(ui.Cmd|ui.Alt, ui.KeyF).Chosen() {
					doc.find = true
					doc.replace = true
				}
				if menu.Item("Go to Line…").Shortcut(ui.Ctrl, ui.KeyG).Chosen() {
					doc.goTo = true
				}
				if len(doc.state.Folds()) > 0 {
					menu.Separator()
					if menu.Item("Toggle Fold").Chosen() {
						doc.state.Command("fold-current")
					}
					if menu.Item("Fold All").Chosen() {
						doc.state.Command("fold-all")
					}
					if menu.Item("Unfold All").Chosen() {
						doc.state.Command("unfold-all")
					}
				}
				if !readOnly {
					if label != "Request description" {
						a.templateMenu(menu, doc, *value)
					}
					menu.Separator()
					if menu.Item("Indent").Chosen() {
						doc.state.Command("indent")
					}
					if menu.Item("Outdent").Chosen() {
						doc.state.Command("outdent")
					}
					if menu.Item("Duplicate Line").Chosen() {
						doc.state.Command("duplicate-line")
					}
					if menu.Item("Delete Line").Chosen() {
						doc.state.Command("delete-line")
					}
					if options.CommentPrefix != "" && menu.Item("Toggle Line Comment").Shortcut(ui.Cmd, ui.KeySlash).Chosen() {
						doc.state.Command("toggle-comment")
					}
				}
			})
			if editor.Changed() {
				doc.findSource = ""
				changed = true
			}
			if !readOnly && label != "Request description" {
				if language == "graphql" {
					a.graphQLCompletion(c, p, editor, doc, *value)
				} else {
					a.templateCompletion(c, p, editor, doc, *value)
				}
			}
		})
	})
	return changed
}

func commentPrefix(language string) string {
	switch language {
	case "graphql", "yaml", "python", "shell":
		return "#"
	case "javascript", "json", "go", "java", "c", "cpp":
		return "//"
	default:
		return ""
	}
}
func syntaxSpans(source, language string, p colors) []ui.CodeSpan {
	lexer := lexers.Get(language)
	if lexer == nil {
		lexer = lexers.Analyse(source)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	iterator, err := lexer.Tokenise(&chroma.TokeniseOptions{State: "root", EnsureLF: false}, source)
	if err != nil {
		return nil
	}
	out := []ui.CodeSpan{}
	offset := 0
	for token := iterator(); token != chroma.EOF; token = iterator() {
		end := offset + utf8.RuneCountInString(token.Value)
		color := p.text
		switch {
		case token.Type == chroma.NameTag || token.Type == chroma.NameAttribute:
			color = p.accent
		case token.Type.InSubCategory(chroma.LiteralString):
			color = p.orange
		case token.Type.InSubCategory(chroma.LiteralNumber):
			color = p.blue
		case token.Type.InCategory(chroma.Keyword):
			color = p.accent
		case token.Type.InCategory(chroma.Comment):
			color = p.muted
		}
		if end > offset {
			out = append(out, ui.CodeSpan{Start: offset, End: end, Color: color, String: token.Type.InSubCategory(chroma.LiteralString) || token.Type == chroma.NameTag || token.Type == chroma.NameAttribute})
		}
		offset = end
	}
	return out
}
func (a *App) editorFindBar(c *ui.Context, p colors, doc *documentEditor, value *string, label string, readOnly bool) {
	doc.updateMatches(*value)
	ui.Column(c).Background(p.sidebar).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Padding(6, 9).Gap(5).Children(func() {
		ui.Row(c).Gap(5).Children(func() {
			field := ui.TextInput(c, &doc.query).Label("Find in " + label).Placeholder("Find").Grow(1).MinWidth(0).AutoFocus().FontSize(12)
			changed := field.Changed()
			if changed {
				doc.findSource = ""
				doc.updateMatches(*value)
				doc.matchIndex = -1
				doc.nextMatch(1)
			}
			if field.Submitted() {
				doc.nextMatch(1)
			}
			count := "No matches"
			if len(doc.matches) > 0 {
				count = fmt.Sprintf("%d / %d", max(0, doc.matchIndex+1), len(doc.matches))
			}
			ui.Text(c, count).FontSize(10).TextColor(p.muted)
			if ui.Button(c, "↑").Label("Previous match").Disabled(len(doc.matches) == 0).Clicked() {
				doc.nextMatch(-1)
			}
			if ui.Button(c, "↓").Label("Next match").Disabled(len(doc.matches) == 0).Clicked() {
				doc.nextMatch(1)
			}
			iconButton(c, "more", "Find options").Menu(func(menu *ui.Menu) {
				if menu.Item("Match Case").Checked(doc.matchCase).Chosen() {
					doc.matchCase = !doc.matchCase
					doc.findSource = ""
				}
				if menu.Item("Regular Expression").Checked(doc.regex).Chosen() {
					doc.regex = !doc.regex
					doc.findSource = ""
				}
				if !readOnly && menu.Item("Replace").Checked(doc.replace).Chosen() {
					doc.replace = !doc.replace
				}
			})
			if iconButton(c, "close", "Close find").Clicked() {
				doc.find = false
				doc.state.Focus()
			}
		})
		if doc.findError != "" {
			ui.Text(c, doc.findError).FontSize(11).TextColor(p.red)
		}
		if doc.replace && !readOnly {
			ui.Row(c).Gap(6).Children(func() {
				ui.TextInput(c, &doc.replacement).Label("Replace in " + label).Placeholder("Replace with").Grow(1).MinWidth(0).FontSize(12)
				if ui.Button(c, "Replace").Disabled(len(doc.matches) == 0).FontSize(11).Clicked() {
					doc.replaceMatch(*value, false)
				}
				if ui.Button(c, "Replace All").Disabled(len(doc.matches) == 0).FontSize(11).Clicked() {
					doc.replaceMatch(*value, true)
				}
			})
		}
	})
}
func (doc *documentEditor) updateMatches(source string) {
	if doc.findSource == source && doc.findQuery == doc.query && doc.findCase == doc.matchCase && doc.findRegex == doc.regex {
		return
	}
	doc.findSource, doc.findQuery, doc.findCase, doc.findRegex = source, doc.query, doc.matchCase, doc.regex
	doc.matches = nil
	doc.captures = nil
	doc.findError = ""
	doc.pattern = nil
	if doc.query == "" {
		doc.matchIndex = -1
		return
	}
	pattern := doc.query
	if !doc.regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !doc.matchCase {
		pattern = "(?i)" + pattern
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		doc.findError = err.Error()
		return
	}
	doc.pattern = compiled
	doc.captures = compiled.FindAllStringSubmatchIndex(source, -1)
	byteRunes := make([]int, len(source)+1)
	runeIndex := 0
	for offset := 0; offset < len(source); {
		_, width := utf8.DecodeRuneInString(source[offset:])
		for i := 0; i < width; i++ {
			byteRunes[offset+i] = runeIndex
		}
		runeIndex++
		offset += width
	}
	byteRunes[len(source)] = runeIndex
	for _, match := range doc.captures {
		doc.matches = append(doc.matches, ui.TextRange{Start: byteRunes[match[0]], End: byteRunes[match[1]]})
	}
	if doc.matchIndex >= len(doc.matches) {
		doc.matchIndex = len(doc.matches) - 1
	}
}
func (doc *documentEditor) nextMatch(direction int) {
	if len(doc.matches) == 0 {
		return
	}
	if doc.matchIndex < 0 {
		doc.matchIndex = 0
		if direction < 0 {
			doc.matchIndex = len(doc.matches) - 1
		}
	} else {
		doc.matchIndex = (doc.matchIndex + direction + len(doc.matches)) % len(doc.matches)
	}
	match := doc.matches[doc.matchIndex]
	doc.state.Select(match.Start, match.End)
}
func (doc *documentEditor) replaceMatch(source string, all bool) {
	doc.updateMatches(source)
	if len(doc.matches) == 0 {
		return
	}
	indices := []int{max(0, doc.matchIndex)}
	if all {
		indices = make([]int, len(doc.matches))
		for i := range indices {
			indices[i] = i
		}
	}
	edits := make([]ui.TextEdit, 0, len(indices))
	for _, i := range indices {
		replacement := doc.replacement
		if doc.regex {
			replacement = string(doc.pattern.ExpandString(nil, replacement, source, doc.captures[i]))
		}
		match := doc.matches[i]
		edits = append(edits, ui.TextEdit{Start: match.Start, End: match.End, Text: replacement})
	}
	doc.state.Apply(edits)
	doc.findSource = ""
	doc.matchIndex = -1
}
