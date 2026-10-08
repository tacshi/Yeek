# MyGo native UI

Based on `github.com/egoist/mygo/ui` at v0.3.4, under the included MIT license. The framework and CLI are pinned to MyGo v0.3.4 too.

As upstream does, the public API (`api_gen.go`) is generated from the private renderer API (`core*` functions and exported methods of `node` and `context`): run `go generate .` here after changing it. `internal/uigen` is upstream's generator, changed only to run from this directory.

Yeek's changes:

- `CodeEditor` (`code_editor.go`, `code_folding.go`, `code_completion.go`) is a native text area for code: a gutter with line numbers and fold markers, syntax colors (`Syntax`), marks and diagnostics, indenting, comment toggling, closing brackets, completion popups attached to the caret (`AttachToCaret`, `EditorSuggestions`), and commands through `CodeEditorState`. It hooks into the editor in `editor.go`, `editor_navigation.go`, `editor_input.go` (typed text goes through `codeInsert`), `editor_layout.go`, `textinput.go` (`textInputBaseWith`), `textarea.go` and `richtext.go` (`spanPaint` offsets). A text area that does not wrap scrolls sideways.
- A new `CodeEditor` document opens at its first line with the caret there, as CodeMirror does, instead of scrolled to a caret at the end like a text field.
- Carets are 2px wide, as in Yaak. A `CodeEditor`'s is as tall as the text (1.3× the font size), centred in its line, like Yaak's CodeMirror caret.
- A read-only `CodeEditor` draws its caret where it is clicked, and the pointer becomes a pointing hand over fold markers.
- `CodeEditorState.Pasted` reports the text last pasted and whether it replaced the editor's whole text.
- `Element.SelectAll` selects all of a text input's text, as when a sidebar row opens for an inline rename.
- `SplitQuiet` is a split whose divider is drawn only while it is hovered or dragged, as in Yaak's pane layout.
- A `NumberInput` given a width widens its text field to fill it.
- `Context.SetZoom` scales the whole interface, as a browser's zoom does, for Yaak's interface zoom: the window's size and pointer input are divided by the zoom and the device scale multiplied by it; menus, the input method's caret and assistive technology get window coordinates. A Tester keeps working in the interface's DIPs.
- `Tester.Batch` delivers input before rendering, as events can arrive during one display frame.
