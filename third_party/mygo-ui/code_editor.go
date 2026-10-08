package ui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/internal/text"
)

// TextRange uses rune offsets, as the native editor does, rather than byte or UTF-16 offsets.
type TextRange struct{ Start, End int }
type TextEdit struct {
	Start, End int
	Text       string
}

// CodeSpan colors a range without changing glyph shaping or cursor geometry.
type CodeSpan struct {
	String     bool
	Start, End int
	Color      Color
}
type CodeMark struct {
	Start, End int
	Color      Color
}

type editorAction struct {
	kind       string
	start, end int
	text       string
	edits      []TextEdit
}

// CodeEditorState exposes selection and commands while the native widget owns editing and undo.
// Give each document editor its own state. Commands are consumed once, on its next frame.
type CodeEditorState struct {
	invalidate              func()
	completion              codeCompletion
	folds                   []CodeFold
	foldVersion             uint64
	document                *editor
	Caret, Anchor           int
	Line, Column, LineCount int
	Focused                 bool
	SelectedText            string
	actions                 []editorAction
	focus                   bool
}

func (s *CodeEditorState) Select(start, end int) {
	s.actions = append(s.actions, editorAction{kind: "select", start: start, end: end})
	s.changed()
}
func (s *CodeEditorState) Insert(value string) {
	s.actions = append(s.actions, editorAction{kind: "insert", text: value})
	s.focus = true
	s.changed()
}
func (s *CodeEditorState) Replace(start, end int, value string) {
	s.actions = append(s.actions, editorAction{kind: "replace", start: start, end: end, text: value})
	s.changed()
}
func (s *CodeEditorState) Apply(edits []TextEdit) {
	s.actions = append(s.actions, editorAction{kind: "edits", edits: slices.Clone(edits)})
	s.changed()
}
func (s *CodeEditorState) GoTo(line, column int) {
	s.actions = append(s.actions, editorAction{kind: "goto", start: line, end: column})
	s.focus = true
	s.changed()
}
func (s *CodeEditorState) Command(name string) {
	s.actions = append(s.actions, editorAction{kind: name})
	s.changed()
}
func (s *CodeEditorState) Focus() { s.focus = true; s.changed() }
func (s *CodeEditorState) changed() {
	if s.invalidate != nil {
		s.invalidate()
	}
}

// CodeEditorOptions controls code-specific behavior. Normal TextArea widgets retain their behavior.
type CodeEditorOptions struct {
	SingleLine     bool
	Font           string
	FontSize       float32
	Wrap           bool
	ReadOnly       bool
	LineNumbers    bool
	TabSize        int
	IndentWithTabs bool
	AutoIndent     bool
	CloseBrackets  bool
	CommentPrefix  string
}

// CodeEditor is a native text area with a fixed gutter and editing commands.
// Syntax and marks are paint-only: they cannot change the caret or input-method layout.
func CodeEditor(c *Context, value *string, state *CodeEditorState, options CodeEditorOptions) *Element {
	options.Font = cmp.Or(options.Font, "monospace")
	if options.FontSize <= 0 {
		options.FontSize = 12
	}
	options.TabSize = max(1, min(cmp.Or(options.TabSize, 2), 16))
	var document *editor
	if state != nil {
		document = state.document
	}
	e := textInputBaseWith(c, value, !options.SingleLine, func(ed *editor) {
		ed.code = true
		ed.codeOptions = options
		ed.readOnly = options.ReadOnly
		ed.codeState = state
	}, document)
	e.widget = "CodeEditor"
	ed := e.st.editor
	ed.readOnly = options.ReadOnly
	e.Font(options.Font).FontSize(options.FontSize).FixedLineHeight(options.FontSize * 1.75)
	gutter := float32(12)
	if options.LineNumbers {
		digits := len(strconv.Itoa(len(ed.buf.paras)))
		gutter = max(38, float32(digits)*options.FontSize*.68+32)
	}
	e.Padding(10, 12, 10, gutter)
	if options.SingleLine {
		e.Padding(4, 8)
	}
	if !options.Wrap {
		e.NoWrap()
	}
	if state != nil {
		rt := c.rt
		state.invalidate = func() {
			if rt.inFrame {
				rt.consumed = true
			} else {
				rt.requestFrame()
			}
		}
		state.document = ed
		before := ed.buf.version
		for _, action := range state.actions {
			switch action.kind {
			case "fold-all":
				state.FoldAll(true)
			case "unfold-all":
				state.FoldAll(false)
			case "fold-current":
				for i := len(state.folds) - 1; i >= 0; i-- {
					f := state.folds[i]
					if ed.caret >= f.Start && ed.caret <= f.End {
						state.ToggleFold(i)
						break
					}
				}
			case "select":
				ed.anchor = max(0, min(action.start, ed.buf.n))
				ed.move(action.end, true)
			case "goto":
				line := max(0, min(action.start-1, len(ed.buf.paras)-1))
				ed.move(min(ed.buf.paras[line].rune+max(0, action.end-1), ed.buf.end(line)), false)
			case "insert":
				ed.insert(action.text)
			case "replace":
				if !ed.readOnly {
					ed.record(false)
					ed.replace(max(0, min(action.start, ed.buf.n)), max(0, min(action.end, ed.buf.n)), action.text)
				}
			case "edits":
				if !ed.readOnly {
					ed.applyCodeEdits(action.edits)
				}
			case "indent":
				ed.indent(false)
			case "outdent":
				ed.indent(true)
			case "duplicate-line":
				ed.duplicateLine()
			case "delete-line":
				ed.deleteLine()
			case "toggle-comment":
				ed.toggleComment()
			default:
				ed.command(c, action.kind)
			}
		}
		state.actions = nil
		if state.focus {
			e.Focus()
			state.focus = false
		}
		if ed.buf.version != before {
			*value = ed.buf.s
			e.st.changed = true
			c.rt.consumed = true
		}
		state.Caret, state.Anchor = ed.caret, ed.anchor
		state.Line = ed.buf.para(ed.caret) + 1
		state.Column = ed.caret - ed.buf.paras[state.Line-1].rune + 1
		state.LineCount = len(ed.buf.paras)
		state.Focused = e.Focused()
		start, end := ed.selection()
		state.SelectedText = ed.buf.slice(start, end)
	}
	ed.readOnly = options.ReadOnly
	return e
}

// Syntax applies ordered, non-overlapping color ranges. The caller can cache them by document text.
func (e *Element) Syntax(spans []CodeSpan) *Element {
	if e.st.editor == nil {
		return e
	}
	ed := e.st.editor
	if len(spans) > 0 && len(spans) == len(ed.syntax) && &spans[0] == &ed.syntax[0] {
		e.codePaint = ed.syntaxPaint
		return e
	}
	paint := &spanPaint{}
	end := 0
	for _, span := range spans {
		if span.End <= span.Start || span.Start < end {
			continue
		}
		if span.Start > end {
			paint.spans = append(paint.spans, Span{})
			paint.styles = append(paint.styles, text.Span{End: span.Start})
		}
		paint.spans = append(paint.spans, Span{Color: span.Color})
		paint.styles = append(paint.styles, text.Span{End: span.End})
		end = span.End
	}
	ed.syntax, ed.syntaxPaint = spans, paint
	e.codePaint = paint
	return e
}
func (e *Element) Marks(marks []CodeMark) *Element { e.codeMarks = marks; return e }

// Diagnostics underlines source ranges without changing text layout or input.
func (e *Element) Diagnostics(marks []CodeMark) *Element { e.codeDiagnostics = marks; return e }

func (ed *editor) indentString() string {
	if ed.codeOptions.IndentWithTabs {
		return "\t"
	}
	return strings.Repeat(" ", ed.codeOptions.TabSize)
}
func (ed *editor) selectedLines() (int, int) {
	start, end := ed.selection()
	first, last := ed.buf.para(start), ed.buf.para(end)
	if end > start && end == ed.buf.paras[last].rune {
		last--
	}
	return first, max(first, last)
}
func (ed *editor) applyCodeEdits(edits []TextEdit) {
	edits = slices.Clone(edits)
	slices.SortFunc(edits, func(a, b TextEdit) int { return cmp.Compare(b.Start, a.Start) })
	previous := ed.buf.n + 1
	for _, edit := range edits {
		if edit.Start < 0 || edit.End < edit.Start || edit.End > ed.buf.n || edit.End > previous {
			return
		}
		previous = edit.Start
	}
	ed.record(false)
	for _, edit := range edits {
		ed.replace(edit.Start, edit.End, edit.Text)
	}
	ed.coalesce = false
}
func (ed *editor) indent(outdent bool) {
	if ed.readOnly {
		return
	}
	start, end := ed.selection()
	if start == end && !outdent {
		column := ed.caret - ed.buf.paras[ed.buf.para(ed.caret)].rune
		value := ed.indentString()
		if !ed.codeOptions.IndentWithTabs {
			value = strings.Repeat(" ", ed.codeOptions.TabSize-column%ed.codeOptions.TabSize)
		}
		ed.insert(value)
		return
	}
	first, last := ed.selectedLines()
	edits := []TextEdit{}
	shiftStart, total := 0, 0
	for i := first; i <= last; i++ {
		at := ed.buf.paras[i].rune
		edit := TextEdit{Start: at, End: at, Text: ed.indentString()}
		delta := utf8.RuneCountInString(edit.Text)
		if outdent {
			line := ed.buf.text(i)
			count := 0
			for _, r := range line {
				if r == '\t' && count == 0 {
					count = 1
					break
				}
				if r != ' ' || count >= ed.codeOptions.TabSize {
					break
				}
				count++
			}
			edit.End = at + count
			edit.Text = ""
			delta = -count
		}
		if i == first {
			shiftStart = delta
		}
		total += delta
		edits = append(edits, edit)
	}
	ed.applyCodeEdits(edits)
	ed.anchor = max(ed.buf.paras[first].rune, start+shiftStart)
	ed.caret = max(ed.anchor, min(end+total, ed.buf.n))
	ed.finishCodeSelection()
}
func (ed *editor) finishCodeSelection() {
	if n := len(ed.undo); n > 0 {
		ed.undo[n-1].caretAfter = ed.caret
		ed.undo[n-1].anchorAfter = ed.anchor
	}
	if ed.area != nil {
		ed.area.reveal = true
	}
}
func (ed *editor) duplicateLine() {
	if ed.readOnly {
		return
	}
	first, last := ed.selectedLines()
	start, end := ed.buf.paras[first].rune, ed.buf.end(last)
	text := ed.buf.slice(start, end)
	ed.record(false)
	ed.replace(end, end, "\n"+text)
	ed.finishCodeSelection()
}
func (ed *editor) deleteLine() {
	if ed.readOnly {
		return
	}
	first, last := ed.selectedLines()
	start, end := ed.buf.paras[first].rune, ed.buf.end(last)
	if last+1 < len(ed.buf.paras) {
		end++
	} else if start > 0 {
		start--
	}
	ed.deleteRange(start, end)
}
func (ed *editor) toggleComment() {
	prefix := ed.codeOptions.CommentPrefix
	if ed.readOnly || prefix == "" {
		return
	}
	first, last := ed.selectedLines()
	remove := true
	for i := first; i <= last; i++ {
		line := strings.TrimSpace(ed.buf.text(i))
		if line != "" && !strings.HasPrefix(line, prefix) {
			remove = false
			break
		}
	}
	edits := []TextEdit{}
	for i := first; i <= last; i++ {
		line := ed.buf.text(i)
		trimmed := strings.TrimLeft(line, " \t")
		offset := utf8.RuneCountInString(line[:len(line)-len(trimmed)])
		start := ed.buf.paras[i].rune + offset
		if remove && strings.HasPrefix(trimmed, prefix) {
			count := utf8.RuneCountInString(prefix)
			if strings.HasPrefix(strings.TrimPrefix(trimmed, prefix), " ") {
				count++
			}
			edits = append(edits, TextEdit{Start: start, End: start + count})
		} else if !remove {
			edits = append(edits, TextEdit{Start: start, End: start, Text: prefix + " "})
		}
	}
	ed.applyCodeEdits(edits)
}
func (ed *editor) codeKey(k editEvent) bool {
	if !ed.code || ed.readOnly {
		return false
	}
	switch k.key {
	case KeyTab:
		if !ed.codeOptions.SingleLine && k.mods&^Shift == 0 {
			ed.indent(k.mods&Shift != 0)
			return true
		}
	case KeyEnter:
		if !ed.codeOptions.SingleLine && k.mods&^Shift == 0 && ed.codeOptions.AutoIndent {
			line := ed.buf.para(ed.caret)
			start := ed.buf.paras[line].rune
			before := ed.buf.slice(start, ed.caret)
			leading := before[:len(before)-len(strings.TrimLeft(before, " \t"))]
			trimmed := strings.TrimSpace(before)
			indent := leading
			if len(trimmed) > 0 && strings.ContainsRune("{[(", rune(trimmed[len(trimmed)-1])) {
				indent += ed.indentString()
			}
			tail := ""
			if ed.caret < ed.buf.n {
				tail = ed.buf.slice(ed.caret, ed.caret+1)
			}
			extra := ""
			if indent != leading && strings.ContainsAny(tail, "}])") {
				extra = "\n" + leading
			}
			ed.insert("\n" + indent + extra)
			if extra != "" {
				ed.move(ed.caret-utf8.RuneCountInString(extra), false)
				ed.finishCodeSelection()
			}
			return true
		}
	case KeyBackspace:
		if k.mods == 0 && ed.codeOptions.CloseBrackets && ed.caret == ed.anchor && ed.caret > 0 && ed.caret < ed.buf.n {
			pair := ed.buf.slice(ed.caret-1, ed.caret+1)
			if slices.Contains([]string{"{}", "[]", "()", "\"\"", "''"}, pair) {
				ed.deleteRange(ed.caret-1, ed.caret+1)
				return true
			}
		}
	case KeySlash:
		if k.mods == Cmd && ed.codeOptions.CommentPrefix != "" {
			ed.toggleComment()
			return true
		}
	}
	return false
}
func (ed *editor) codeInsert(value string) {
	if !ed.code || !ed.codeOptions.CloseBrackets || ed.readOnly || utf8.RuneCountInString(value) != 1 {
		ed.insert(value)
		return
	}
	pairs := map[string]string{"{": "}", "[": "]", "(": ")", "\"": "\"", "'": "'"}
	start, end := ed.selection()
	if start == end && ed.caret < ed.buf.n && ed.buf.slice(ed.caret, ed.caret+1) == value && strings.ContainsAny(value, "}])\"'") {
		ed.move(ed.caret+1, false)
		return
	}
	for _, span := range ed.syntax {
		if span.String && start > span.Start && start < span.End {
			ed.insert(value)
			return
		}
	}
	close, ok := pairs[value]
	if !ok {
		ed.insert(value)
		return
	}
	if (value == "\"" || value == "'") && start > 0 && ed.buf.slice(start-1, start) == "\\" {
		ed.insert(value)
		return
	}
	selected := ed.buf.slice(start, end)
	ed.record(false)
	ed.replace(start, end, value+selected+close)
	ed.anchor = start + 1
	ed.caret = start + 1 + utf8.RuneCountInString(selected)
	ed.finishCodeSelection()
}
