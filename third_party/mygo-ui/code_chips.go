package ui

import (
	"slices"

	"github.com/egoist/mygo/internal/text"
)

// CodeChip draws runes Start up to End of a CodeEditor's text as one chip,
// as a CodeMirror atomic replace decoration does: a rounded box behind
// them, which takes its hover colors while the pointer is over it. The
// caret steps over it, Backspace and Delete take it whole, and a click on
// it is reported by CodeEditorState.ChipClicked instead of placing the
// caret.
type CodeChip struct {
	Start, End                   int
	Background, Border           Color
	HoverBackground, HoverBorder Color
}

// Chips sets the editor's chips, in the frames that call it. They don't
// overlap; a chip that does is dropped.
func (e *node) Chips(chips []CodeChip) *node {
	ed := e.st.editor
	if ed == nil || !ed.code {
		return e
	}
	ed.chips = ed.chips[:0]
	for _, chip := range chips {
		if chip.End > chip.Start && chip.Start >= 0 && chip.End <= ed.buf.n {
			ed.chips = append(ed.chips, chip)
		}
	}
	slices.SortFunc(ed.chips, func(a, b CodeChip) int { return a.Start - b.Start })
	out := ed.chips[:0]
	for _, chip := range ed.chips {
		if len(out) == 0 || chip.Start >= out[len(out)-1].End {
			out = append(out, chip)
		}
	}
	ed.chips = out
	// A chip stays lit only while the pointer is over the editor.
	ed.chipsHover = len(out) > 0 && e.Hovered()
	return e
}

// ChipClicked reports, once, the chip last clicked.
func (s *CodeEditorState) ChipClicked() (CodeChip, bool) {
	if s.chipClicked == nil {
		return CodeChip{}, false
	}
	chip := *s.chipClicked
	s.chipClicked = nil
	return chip, true
}

// chipRects calls f with the boxes of the chip's runes, relative to the
// element, as they were last laid out.
func (ed *editor) chipRects(chip CodeChip, f func(Rect)) {
	if a := ed.area; a != nil {
		if a.version == 0 {
			return
		}
		first, last := ed.buf.para(chip.Start), ed.buf.para(chip.End)
		for i := max(first, a.first); i <= min(last, a.last); i++ {
			if a.isHidden(i) {
				continue
			}
			l := a.paraLayout(ed, i)
			start, end := ed.buf.start(i), ed.buf.end(i)
			from, to := max(chip.Start, start), min(chip.End, end)
			if from >= to {
				continue
			}
			y := ed.originY + float32(a.hs.top(i)-a.scroll)
			for _, r := range l.Selection(a.local(ed, i, from), a.local(ed, i, to)) {
				f(Rect{ed.originX - ed.scrollX + r.X, y + r.Y, r.W, r.H})
			}
		}
		return
	}
	if ed.layout == nil {
		return
	}
	for _, r := range ed.layout.Selection(ed.displayIndex(chip.Start), ed.displayIndex(chip.End)) {
		f(Rect{ed.originX - ed.scrollX + r.X, ed.originY + r.Y, r.W, r.H})
	}
}

// chipAt returns the index of the chip at (x, y), relative to the element,
// or -1.
func (ed *editor) chipAt(x, y float32) int {
	if !ed.code || ed.readOnly || !ed.laidOut() {
		return -1
	}
	for i, chip := range ed.chips {
		found := false
		ed.chipRects(chip, func(r Rect) { found = found || r.Contains(x, y) })
		if found {
			return i
		}
	}
	return -1
}

func (ed *editor) overChip(x, y float32) bool {
	i := ed.chipAt(x, y)
	if i != ed.hoverChip {
		ed.hoverChip = i
		if ed.codeState != nil {
			ed.codeState.changed()
		}
	}
	return i >= 0
}

// pressChip takes a click on a chip, which is reported as the pointer
// comes up. Each click of a double click is one, as DOM click events are.
func (ed *editor) pressChip(x, y float32, clicks int) bool {
	if clicks < 1 || ed.pressMods&Shift != 0 || ed.codeState == nil {
		return false
	}
	i := ed.chipAt(x, y)
	if i < 0 {
		return false
	}
	chip := ed.chips[i]
	ed.pressedChip = &chip
	return true
}

func (ed *editor) releaseChip() {
	if ed.pressedChip == nil {
		return
	}
	if ed.codeState != nil {
		ed.codeState.chipClicked = ed.pressedChip
		ed.codeState.changed()
	}
	ed.pressedChip = nil
}

// skipChip moves a caret that lands inside a chip to its edge, the one
// the caret was heading for from.
func (ed *editor) skipChip(to, from int) int {
	for _, chip := range ed.chips {
		if to > chip.Start && to < chip.End {
			if to < from {
				return chip.Start
			}
			return chip.End
		}
	}
	return to
}

// chipBeside returns the chip that ends at i, or starts there when after.
func (ed *editor) chipBeside(i int, after bool) (CodeChip, bool) {
	for _, chip := range ed.chips {
		if !after && chip.End == i || after && chip.Start == i {
			return chip, true
		}
	}
	return CodeChip{}, false
}

// paintChips paints the boxes of the chips in runes start up to end, whose
// layout has its top left at (ox, y); rects gives the boxes of runes in it.
func (ed *editor) paintChips(p *Painter, rects func(from, to int) []text.Rect, start, end int, ox, y float32) {
	for i, chip := range ed.chips {
		from, to := max(chip.Start, start), min(chip.End, end)
		if from >= to {
			continue
		}
		bg, border := chip.Background, chip.Border
		if i == ed.hoverChip && ed.chipsHover {
			bg, border = chip.HoverBackground, chip.HoverBorder
		}
		for _, r := range rects(from, to) {
			box := Rect{ox + r.X - 1, y + r.Y, r.W + 2, r.H}
			// As tall as the text and a little more, like a CodeMirror
			// widget, centred in the taller code line.
			if h := ed.codeOptions.FontSize*1.3 + 2; h < box.H {
				box.Y += (box.H - h) / 2
				box.H = h
			}
			p.Fill(box, bg, 2)
			p.Stroke(box, border, 2, 1)
		}
	}
}
