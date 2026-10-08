package ui

type codeCompletion struct {
	key                  string
	count, index, chosen int
	requested, dismissed bool
}

// Completions enables editor-owned keyboard navigation for an application popup.
// key identifies its options and replacement range; changing it resets selection.
func (s *CodeEditorState) Completions(key string, count int) {
	if s.completion.key != key {
		s.completion.index = 0
	}
	s.completion.key = key
	s.completion.count = max(0, count)
	s.completion.index = max(0, min(s.completion.index, count-1))
}

func (s *CodeEditorState) CompletionIndex() int { return s.completion.index }

// CompletionEvent consumes keyboard intent since the previous frame. chosen is
// -1 when no option was accepted. The application inserts its chosen completion.
func (s *CodeEditorState) CompletionEvent() (requested, dismissed bool, chosen int) {
	v := &s.completion
	requested, dismissed, chosen = v.requested, v.dismissed, v.chosen-1
	v.requested, v.dismissed, v.chosen = false, false, 0
	return
}

func (ed *editor) completionWants(key Key, mods Modifiers) bool {
	if !ed.code || ed.readOnly || ed.codeState == nil || ed.compose != "" {
		return false
	}
	if key == KeySpace && mods == Ctrl {
		return true
	}
	if mods != 0 || ed.codeState.completion.count == 0 {
		return false
	}
	switch key {
	case KeyUp, KeyDown, KeyEnter, KeyTab, KeyEscape:
		return true
	}
	return false
}

func (ed *editor) completionKey(key Key, mods Modifiers) bool {
	if !ed.completionWants(key, mods) {
		return false
	}
	v := &ed.codeState.completion
	switch key {
	case KeySpace:
		v.requested = true
	case KeyUp:
		v.index = (v.index + v.count - 1) % v.count
	case KeyDown:
		v.index = (v.index + 1) % v.count
	case KeyEnter, KeyTab:
		v.chosen, v.count = v.index+1, 0
	case KeyEscape:
		v.dismissed, v.count = true, 0
	}
	return true
}

// AttachToCaret positions a popup beside a native editor's caret and keeps it
// within the same focus scope, including when the editor is inside a dialog.
func (e *Element) AttachToCaret(target *Element) *Element {
	e.AttachTo(target, AnchorBottomLeft, AnchorTopLeft)
	e.attachCaret = true
	return e
}

// EditorSuggestions keeps pointer presses in a completion popup from ending
// the editor's focus, and exposes its items as an accessibility list.
func (e *Element) EditorSuggestions(target *Element) *Element {
	e.AttachToCaret(target).Role(RoleList)
	e.flags |= flagKeepFocus | flagClickable
	target.expanded = true
	return e
}

func (e *Element) EditorSuggestion(target *Element, index, count int, selected bool) *Element {
	e.Role(RoleListItem)
	e.flags |= flagClickable | flagHover | flagChoosable
	e.setPos, e.setSize = index+1, count
	e.highlighted = selected
	if selected {
		target.activeDescendant = e
	}
	return e
}
