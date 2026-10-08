package ui

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/internal/text"
)

// CodeFold describes an opening delimiter through its closing delimiter, in rune offsets.
// Folding affects layout only. Copying, saving and sending retain the complete document.
type CodeFold struct {
	Placeholder string
	Start, End  int
	Collapsed   bool
}

func (s *CodeEditorState) SetFolds(folds []CodeFold) {
	next := slices.Clone(folds)
	for i := range next {
		for _, old := range s.folds {
			if next[i].Start == old.Start && next[i].End == old.End {
				next[i].Collapsed = old.Collapsed
				break
			}
		}
	}
	if slices.Equal(next, s.folds) {
		return
	}
	s.folds = next
	s.foldVersion++
}
func (s *CodeEditorState) Folds() []CodeFold { return slices.Clone(s.folds) }
func (s *CodeEditorState) ToggleFold(index int) {
	if index < 0 || index >= len(s.folds) {
		return
	}
	s.folds[index].Collapsed = !s.folds[index].Collapsed
	s.foldVersion++
	if ed := s.document; ed != nil {
		if s.folds[index].Collapsed {
			first, last := ed.buf.para(s.folds[index].Start), ed.buf.para(s.folds[index].End)
			line := ed.buf.para(ed.caret)
			if line > first && line <= last {
				ed.caret = s.folds[index].Start
				ed.anchor = ed.caret
			}
		}
		ed.area.reveal = true
	}
}
func (s *CodeEditorState) FoldAll(collapsed bool) {
	for i := range s.folds {
		s.folds[i].Collapsed = collapsed
	}
	s.foldVersion++
	if ed := s.document; ed != nil {
		if collapsed {
			for _, fold := range s.folds {
				first, last := ed.buf.para(fold.Start), ed.buf.para(fold.End)
				line := ed.buf.para(ed.caret)
				if line > first && line <= last {
					ed.caret = fold.Start
					ed.anchor = ed.caret
					break
				}
			}
		}
		ed.area.reveal = true
	}
}
func (s *CodeEditorState) foldEdits(start, end int, inserted string) {
	delta := utf8.RuneCountInString(inserted) - (end - start)
	for i := range s.folds {
		fold := &s.folds[i]
		switch {
		case end <= fold.Start:
			fold.Start += delta
			fold.End += delta
		case start >= fold.End:
		case start > fold.Start && end < fold.End:
			fold.End += delta
			fold.Collapsed = false
		default:
			fold.End = fold.Start
		}
	}
	s.folds = slices.DeleteFunc(s.folds, func(f CodeFold) bool { return f.End <= f.Start })
	s.foldVersion++
}
func (ed *editor) unfoldAt(position int) {
	if ed.codeState == nil {
		return
	}
	line := ed.buf.para(position)
	for i, fold := range ed.codeState.folds {
		if fold.Collapsed && line > ed.buf.para(fold.Start) && line <= ed.buf.para(fold.End) {
			ed.codeState.folds[i].Collapsed = false
			ed.codeState.foldVersion++
		}
	}
}
func (a *area) syncFolds(ed *editor) {
	version := uint64(1)
	if ed.codeState != nil {
		version += ed.codeState.foldVersion
	}
	if a.foldStateVersion == version {
		return
	}
	a.foldStateVersion = version
	a.hidden = make([]bool, len(ed.buf.paras))
	a.foldStarts = map[int]int{}
	a.hs.reset(ed.buf.paras)
	a.widest = 0
	if ed.code && ed.codeState != nil {
		for i, fold := range ed.codeState.folds {
			if fold.Start < 0 || fold.End > ed.buf.n || fold.End <= fold.Start {
				continue
			}
			first, last := ed.buf.para(fold.Start), ed.buf.para(fold.End)
			if first >= last {
				continue
			}
			if prev, ok := a.foldStarts[first]; !ok || ed.codeState.folds[prev].End < fold.End {
				a.foldStarts[first] = i
			}
			if fold.Collapsed {
				for line := first + 1; line <= last; line++ {
					a.hidden[line] = true
				}
			}
		}
	}
	for i, hidden := range a.hidden {
		if !hidden {
			continue
		}
		if h := ed.buf.paras[i].h; h > 0 {
			a.hs.add(i, -float64(h), 0)
		} else {
			a.hs.add(i, 0, -1)
		}
	}
	a.hs.est = float64(a.line.Height)
}
func (a *area) isHidden(line int) bool { return line >= 0 && line < len(a.hidden) && a.hidden[line] }

var hiddenCodeLine = text.Layout{}

// foldAt returns the fold whose marker is at (x, y), relative to the
// element: its chevron in the gutter, or the ellipsis after a collapsed line.
func (ed *editor) foldAt(x, y float32) (int, bool) {
	if !ed.code || ed.codeState == nil || ed.area == nil || !ed.codeOptions.LineNumbers || !ed.laidOut() {
		return 0, false
	}
	line := ed.area.hs.at(float64(y-ed.originY) + ed.area.scroll)
	index, ok := ed.area.foldStarts[line]
	if !ok || index >= len(ed.codeState.folds) {
		return 0, false
	}
	inGutter := x >= ed.originX-19 && x < ed.originX
	if !inGutter && ed.codeState.folds[index].Collapsed {
		layout := ed.area.paraLayout(ed, line)
		inGutter = x-ed.originX+ed.scrollX >= layout.Width
	}
	return index, inGutter
}

// overFold reports whether (x, y) is over a fold marker, which takes a
// pointing-hand cursor as a button does.
func (ed *editor) overFold(x, y float32) bool {
	_, ok := ed.foldAt(x, y)
	return ok
}
func (ed *editor) pressFold(x, y float32) bool {
	index, ok := ed.foldAt(x, y)
	if !ok {
		return false
	}
	ed.codeState.ToggleFold(index)
	return true
}
func (a *area) paintFold(e *node, p *Painter, line int, ox, oy float32) {
	ed := e.st.editor
	if ed.codeState == nil {
		return
	}
	index, ok := a.foldStarts[line]
	if !ok {
		return
	}
	fold := ed.codeState.folds[index]
	layout := a.paraLayout(ed, line)
	y := oy + float32(a.hs.top(line)-a.scroll)
	h := a.line.Height
	x := e.x + ed.originX - 10
	cy := y + h/2
	color := e.c.theme.TextMuted
	var arrow Path
	if fold.Collapsed {
		arrow.MoveTo(x-2, cy-3)
		arrow.LineTo(x+2, cy)
		arrow.LineTo(x-2, cy+3)
	} else {
		arrow.MoveTo(x-3, cy-2)
		arrow.LineTo(x, cy+2)
		arrow.LineTo(x+3, cy-2)
	}
	p.StrokePath(&arrow, 1, color)
	if fold.Collapsed {
		last := ed.buf.para(fold.End)
		marker := fold.Placeholder
		if marker == "" {
			marker = " … " + strings.TrimSpace(ed.buf.text(last))
		}
		params := e.textParams(0)
		params.Width = 0
		params.Text = marker
		label := textSystem().Layout(params)
		p.textLayout(label, ox+layout.Width+4, y, color, e.resolvedText(), nil)
	}
}
