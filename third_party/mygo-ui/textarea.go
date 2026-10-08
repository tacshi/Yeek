package ui

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/internal/scene"
	"github.com/egoist/mygo/internal/text"
)

// area lays out and shows the text of a TextArea a paragraph at a time, as
// a List does its rows: each paragraph keeps its layout until an edit
// changes it, only those in view are laid out, and the heights of the
// others are estimated (heights) until they are. Text laid out whole, as
// text elements and single-line inputs are, makes the same lines, since
// the text system breaks lines a paragraph at a time too.
//
// The area scrolls as a scroll container does (flagScrollY): the wheel and
// the scroll bar move the state's offset, which the area keeps by a
// paragraph and how far into it the view starts (anchor, anchorOff), so
// that the view stays put as the heights above it are measured.
type area struct {
	// A CodeEditor's folds hide lines (code_folding.go).
	hidden           []bool
	foldStarts       map[int]int
	foldStateVersion uint64
	hs               heights
	// params are those of the layouts, without their text; version is
	// the buffer's the heights are of, and line a line of an empty text.
	params  text.Params
	version uint64
	line    text.Line

	scroll     float64 // the content's y at the top of the view
	anchor     int
	anchorOff  float64
	lastScroll float64 // the state's offset after the last layout
	// reveal scrolls the caret into view at the next layout, after an
	// edit, a move of the caret or a press.
	reveal bool
	// first and last are the paragraphs the view shows; laid counts those
	// with a layout.
	first, last int
	laid        int
	// wrapped is the height of the whole text wrapped at a width, for a text
	// area that grows with it (Lines), and the params it is of.
	wrapped        float32
	wrappedParams  text.Params
	wrappedVersion uint64
	wrappedCompose string
	// widest is the widest paragraph laid out, how far a text area that
	// does not wrap scrolls sideways.
	widest float32
}

// wrappedHeight returns the height of the text, with an input method's
// composition, wrapped at the content width cw.
func (a *area) wrappedHeight(e *node, ed *editor, cw float32) float32 {
	params := e.textParams(max(cw, 1))
	if ed.buf.indexed {
		params.KeepSpaces, params.MaxLines = true, 0
		if params != a.wrappedParams || a.wrappedVersion != ed.buf.version || a.wrappedCompose != ed.compose {
			empty := params
			empty.Text = ""
			limit := float32(ed.lines[1]) * textSystem().Layout(empty).Lines[0].Height
			var height float32
			for p := 0; p < len(ed.buf.paras) && height < limit; p++ {
				part := params
				part.Text = ed.buf.text(p)
				if ed.compose != "" && p == ed.buf.para(ed.caret) {
					at := runeOffset(part.Text, ed.caret-ed.buf.start(p))
					part.Text = part.Text[:at] + ed.compose + part.Text[at:]
				} else {
					part.Spans, _ = ed.rangeSpans(ed.buf.start(p), ed.buf.end(p))
				}
				height += textSystem().Shape(part).Height
			}
			a.wrapped, a.wrappedParams, a.wrappedVersion = height, params, ed.buf.version
			a.wrappedCompose = ed.compose
		}
		return a.wrapped
	}
	params.Text = ed.displayText()
	params.KeepSpaces, params.MaxLines = true, 0
	if ed.compose == "" {
		// Bold runs take more room, so the text may wrap sooner.
		params.Spans, _ = ed.rangeSpans(0, ed.buf.n)
	}
	if params != a.wrappedParams {
		a.wrappedParams = params
		a.wrapped = textSystem().Layout(params).Height
	}
	return a.wrapped
}

// maxLaid is how many paragraphs keep their layouts out of view, beyond
// those in view: those further from it give theirs up, keeping their
// heights.
const maxLaid = 512

// edited follows an edit of the buffer: the paragraphs from first, which
// were old, are now after others, without a layout.
func (a *area) edited(b *buffer, first int, old []paragraph, after int) {
	a.widest = 0
	if len(a.foldStarts) > 0 {
		a.version = 0
	}
	a.foldStateVersion = 0
	for _, o := range old {
		if o.layout != nil {
			a.laid--
		}
	}
	if a.version+1 == b.version {
		a.version = b.version
		if len(old) == after {
			for k, o := range old {
				if o.h > 0 {
					a.hs.add(first+k, -float64(o.h), 1)
				}
			}
		} else {
			a.hs.reset(b.paras)
		}
	}
	switch {
	case a.anchor >= first+len(old):
		a.anchor += after - len(old)
	case a.anchor > first:
		a.anchor, a.anchorOff = first, 0
	}
	a.reveal = true
}

// sync gets the heights in step with the buffer and the layouts' params,
// which a new width or style changes.
func (a *area) sync(b *buffer, params text.Params) {
	if params != a.params {
		for i := range b.paras {
			b.paras[i].layout, b.paras[i].h = nil, 0
		}
		a.laid = 0
		a.params = params
		a.version = 0
		a.foldStateVersion = 0
		a.widest = 0
		empty := params
		empty.Text = ""
		a.line = textSystem().Layout(empty).Lines[0]
	}
	if a.version != b.version || len(a.hs.measured) != len(b.paras)+1 {
		a.widest = 0
		if a.version != b.version {
			a.laid = 0 // a new text: its paragraphs have no layouts
		}
		a.hs.reset(b.paras)
		a.foldStateVersion = 0
		a.version = b.version
		a.anchor = min(a.anchor, len(b.paras)-1)
	}
	m, unknown := a.hs.totals()
	if slices.Contains(a.hidden, true) {
		a.hs.est = float64(a.line.Height)
		return
	}
	if n := len(b.paras) - unknown; n > 0 {
		a.hs.est = m / float64(n)
	} else {
		a.hs.est = float64(a.line.Height)
	}
}

// paraLayout returns the layout of paragraph p, with the composition of an
// input method in the paragraph of the caret.
func (a *area) paraLayout(ed *editor, p int) *text.Layout {
	if a.isHidden(p) {
		return &hiddenCodeLine
	}
	b := &ed.buf
	pr := &b.paras[p]
	compose := ""
	if ed.compose != "" && p == b.para(ed.caret) {
		compose = ed.compose
	}
	spans := ""
	if compose == "" {
		spans, _ = ed.rangeSpans(b.start(p), b.end(p))
	}
	if pr.layout != nil && pr.compose == compose && pr.spans == spans {
		a.widest = max(a.widest, pr.layout.Width)
		return pr.layout
	}
	t := b.text(p)
	if compose != "" {
		at := runeOffset(t, ed.caret-b.start(p))
		t = t[:at] + compose + t[at:]
	}
	params := a.params
	// A paragraph's layout outlives edits elsewhere. Its text must not
	// pin the old document when the buffer replaces it with a new string.
	if !b.indexed && len(t) < b.byteLen() {
		t = strings.Clone(t)
	}
	params.Text, params.Spans = t, spans
	l := textSystem().Shape(params)
	a.widest = max(a.widest, l.Width)
	if pr.layout == nil {
		a.laid++
	}
	if pr.h > 0 {
		a.hs.add(p, float64(l.Height-pr.h), 0)
	} else {
		a.hs.add(p, float64(l.Height), -1)
	}
	pr.layout, pr.compose, pr.spans, pr.h = l, compose, spans, l.Height
	return l
}

// local returns where rune i of paragraph p is in its layout, which holds
// the composition after the caret.
func (a *area) local(ed *editor, p, i int) int {
	l := i - ed.buf.start(p)
	if ed.compose != "" && i > ed.caret && p == ed.buf.para(ed.caret) {
		l += utf8.RuneCountInString(ed.compose)
	}
	return l
}

// global returns the rune of the text at index l of the layout of
// paragraph p.
func (a *area) global(ed *editor, p, l int) int {
	start := ed.buf.start(p)
	if ed.compose != "" && p == ed.buf.para(ed.caret) {
		n, c := utf8.RuneCountInString(ed.compose), ed.caret-start
		switch {
		case l > c+n:
			l -= n
		case l > c:
			l = c
		}
	}
	return min(start+l, ed.buf.end(p))
}

// caretAt returns the caret before rune i, extra runes into a
// composition there: its x, top and height in the content.
func (a *area) caretAt(ed *editor, i, extra int) (x float32, y float64, h float32) {
	p := ed.buf.para(i)
	l := a.paraLayout(ed, p)
	affinity := text.Downstream
	if i == ed.caret {
		affinity = ed.caretAffinity
	}
	x, ly, h := l.CaretAt(text.CaretPosition{Index: a.local(ed, p, i) + extra, Affinity: affinity})
	return x, a.hs.top(p) + float64(ly), h
}

// lineEdges returns the edges of the visual line holding rune i.
func (a *area) lineEdges(ed *editor, i int) (int, int) {
	p := ed.buf.para(i)
	l := a.paraLayout(ed, p)
	line := &l.Lines[l.LineAt(a.local(ed, p, i))]
	return a.global(ed, p, line.Start), a.global(ed, p, line.End)
}

// firstLine returns the first line of the text, or a line of an empty text
// while the first paragraph has no layout.
func (a *area) firstLine(b *buffer) *text.Line {
	if l := b.paras[0].layout; l != nil {
		return &l.Lines[0]
	}
	return &a.line
}

// indexAt returns the rune of the caret position closest to (x, y) in the
// content.
func (a *area) indexAt(ed *editor, x float32, y float64) int {
	return a.positionAt(ed, x, y).Index
}

func (a *area) positionAt(ed *editor, x float32, y float64) text.CaretPosition {
	return a.hitPosition(ed, x, y, false)
}

func (a *area) documentPositionAt(ed *editor, x float32, y float64) text.CaretPosition {
	return a.hitPosition(ed, x, y, true)
}

func (a *area) hitPosition(ed *editor, x float32, y float64, virtual bool) text.CaretPosition {
	n := len(ed.buf.paras)
	y = max(y, 0)
	p := a.hs.at(y)
	// Laying the paragraph out may show it is not as high as estimated.
	for range n {
		l := a.paraLayout(ed, p)
		top := a.hs.top(p)
		switch {
		case y < top && p > 0:
			p--
		case y >= top+float64(l.Height) && p < n-1:
			p++
		default:
			position := l.PositionAt(x, float32(y-top))
			if virtual {
				position.Index += ed.buf.start(p)
				if ed.compose != "" && p > ed.buf.para(ed.caret) {
					position.Index += utf8.RuneCountInString(ed.compose)
				}
			} else {
				position.Index = a.global(ed, p, position.Index)
			}
			return position
		}
	}
	return text.CaretPosition{Index: ed.buf.n, Affinity: text.Upstream}
}

// setScroll scrolls the content to y, keeping the place by the paragraph
// there.
func (a *area) setScroll(y float64) {
	a.scroll = y
	a.anchor = a.hs.at(y)
	a.anchorOff = y - a.hs.top(a.anchor)
}

// layout lays the text area out in a content box of cw×ch: the
// paragraphs in view, the caret's when it is to be revealed, and where the
// view is.
func (a *area) layout(e *node, cw, ch float32) {
	ed := e.st.editor
	b := &ed.buf
	params := e.textParams(cw)
	params.KeepSpaces, params.MaxLines = true, 0
	a.sync(b, params)
	a.syncFolds(ed)

	st := e.st
	horizontal := float32(st.scrollX)
	if !e.noWrap {
		horizontal = 0
	}
	if st.scrollY != a.lastScroll {
		a.setScroll(st.scrollY) // the wheel or the scroll bar
	}
	// The text shows through the padding, inside the border.
	above, below := e.pad[0], e.pad[2]
	a.place(ed, ch, above, below)
	if a.reveal {
		a.reveal = false
		x, y, h := a.caretAt(ed, ed.caret, ed.composeCaret)
		if e.noWrap {
			if x < horizontal {
				horizontal = x
			} else if x > horizontal+cw-1 {
				horizontal = x - cw + 1
			}
		}
		switch {
		case y < a.scroll:
			a.setScroll(y)
		case y+float64(h) > a.scroll+float64(ch):
			// Measure the paragraphs above the caret's that the view will
			// show, so that the caret ends at its bottom whatever their
			// heights were estimated.
			p := b.para(ed.caret)
			for q, room := p-1, y+float64(h)-a.hs.top(p); q >= 0 && room < float64(ch); q-- {
				room += float64(a.paraLayout(ed, q).Height)
			}
			_, y, h = a.caretAt(ed, ed.caret, ed.composeCaret)
			a.setScroll(y + float64(h) - float64(ch))
		}
		a.place(ed, ch, above, below)
	}
	total := a.hs.top(len(b.paras))
	if s := max(0, min(a.scroll, total-float64(ch))); s != a.scroll {
		a.setScroll(s)
		a.place(ed, ch, above, below)
		total = a.hs.top(len(b.paras))
	}
	horizontal = max(0, min(horizontal, max(a.widest-cw+1, 0)))
	ed.scrollX = horizontal
	st.scrollTo(float64(horizontal), a.scroll)
	a.lastScroll = a.scroll
	e.contentW, e.contentH = float64(e.w), total+float64(e.padY())
	if e.noWrap {
		e.contentW += float64(max(a.widest-cw+1, 0))
	}
	if a.laid > maxLaid+a.last-a.first {
		a.forget(b)
	}
}

// place lays out the paragraphs in view, from the anchor down, with those
// showing in the bands above and below the view, and keeps the view where
// the anchor is.
func (a *area) place(ed *editor, ch, above, below float32) {
	n := len(ed.buf.paras)
	a.anchor = max(0, min(a.anchor, n-1))
	a.paraLayout(ed, a.anchor)
	a.first = a.anchor
	for a.first > 0 && a.hs.top(a.anchor)+a.anchorOff-a.hs.top(a.first) < float64(above) {
		a.first--
		a.paraLayout(ed, a.first)
	}
	// The heights from the first paragraph shown are known now: the place
	// is by the paragraph the offset falls in.
	a.setScroll(max(0, a.hs.top(a.anchor)+a.anchorOff))
	y := a.hs.top(a.first)
	p := a.first
	for ; p < n; p++ {
		y += float64(a.paraLayout(ed, p).Height)
		if y >= a.scroll+float64(ch+below) {
			break
		}
	}
	a.last = min(p, n-1)
}

// forget drops the layouts of the paragraphs far from view, keeping their
// heights.
func (a *area) forget(b *buffer) {
	lo, hi := a.first-maxLaid/4, a.last+maxLaid/4
	for i := range b.paras {
		if (i < lo || i > hi) && b.paras[i].layout != nil {
			b.paras[i].layout = nil
			a.laid--
		}
	}
}

// paint paints the paragraphs in view, the selection, the composition and
// the caret, with the content's top at oy.
func (a *area) paint(e *node, p *Painter, ox, oy float32) {
	ed := e.st.editor
	b := &ed.buf
	t := e.c.theme
	ts := e.resolvedText()
	focused := e.Focused()
	if ed.code && ed.codeOptions.LineNumbers {
		for i := a.first; i <= a.last; i++ {
			if a.isHidden(i) {
				continue
			}
			params := e.textParams(0)
			params.Width = 0
			params.Text = strconv.Itoa(i + 1)
			number := textSystem().Layout(params)
			x := e.x + ed.originX - number.Width - 22
			y := oy + float32(a.hs.top(i)-a.scroll)
			p.textLayout(number, x, y, t.TextMuted, ts, nil)
			a.paintFold(e, p, i, ox, oy)
		}
	}
	clip := Rect{X: e.x + ed.originX, Y: e.y + e.border[0], W: e.w - ed.originX - e.border[1], H: e.h - e.border[0] - e.border[2]}
	savedClip := p.clip
	p.pushClip(clip, [4]float32{})
	defer func() { p.popClip(); p.clip = savedClip }()
	last := len(b.paras) - 1
	for i := a.first; i <= a.last; i++ {
		if a.isHidden(i) {
			continue
		}
		l := a.paraLayout(ed, i)
		y := oy + float32(a.hs.top(i)-a.scroll)
		start, end := b.start(i), b.end(i)
		// A code editor shows its selection without the focus too.
		if focused || ed.code {
			for _, selected := range ed.selectedRanges() {
				sa, sz := selected.Start, selected.End
				if sa == sz || sa > end || sz < start || sz == start && i > 0 && sa < start {
					continue
				}
				from, to := a.local(ed, i, max(sa, start)), a.local(ed, i, min(sz, end))
				for _, r := range l.SelectionVisual(from, to, sz > end && i < last) {
					p.Fill(Rect{ox + r.X, y + r.Y, r.W, r.H}, ts.selectionColor(t), 0)
				}
			}
		}
		firstMark, _ := slices.BinarySearchFunc(e.codeMarks, start, func(mark CodeMark, position int) int {
			if mark.End <= position {
				return -1
			}
			return 1
		})
		for _, mark := range e.codeMarks[firstMark:] {
			if mark.Start >= end {
				break
			}
			from, to := max(mark.Start, start), min(mark.End, end)
			if from >= to {
				continue
			}
			for _, r := range l.Selection(a.local(ed, i, from), a.local(ed, i, to)) {
				p.Fill(Rect{X: ox + r.X, Y: y + r.Y, W: r.W, H: r.H}, mark.Color, 0)
			}
		}
		var sp *spanPaint
		if pr := &b.paras[i]; pr.compose == "" {
			_, sp = ed.rangeSpans(start, end)
		}
		if e.codePaint != nil {
			value := *e.codePaint
			value.offset = start
			if ed.compose != "" && b.para(ed.caret) == i {
				value.insertAt = ed.caret
				value.insertCount = utf8.RuneCountInString(ed.compose)
			}
			sp = &value
		}
		p.textLayout(l, ox, y, ts.color, ts, sp)
		for _, mark := range e.codeDiagnostics {
			if mark.Start >= end {
				break
			}
			from, to := max(mark.Start, start), min(mark.End, end)
			if from >= to {
				continue
			}
			for _, r := range l.Selection(a.local(ed, i, from), a.local(ed, i, to)) {
				p.Fill(Rect{X: ox + r.X, Y: y + r.Y + r.H - 2, W: r.W, H: 1}, mark.Color, 0)
			}
		}
		if ed.compose != "" && b.para(ed.caret) == i {
			c := ed.caret - start
			for _, r := range l.Selection(c, c+utf8.RuneCountInString(ed.compose)) {
				p.Fill(Rect{ox + r.X, y + r.Y + r.H - 2, r.W, 1}, ts.color, 0)
			}
		}
	}
	// A read-only code editor still shows where it was clicked.
	if focused && (!ed.readOnly || ed.code) {
		rt := e.c.rt
		phase := time.Since(rt.blinkStart)
		const blink = 530 * time.Millisecond
		if (phase/blink)%2 == 0 {
			x, y, h := a.caretAt(ed, ed.caret, ed.composeCaret)
			top := oy + float32(y-a.scroll)
			if ed.code {
				// Like Yaak's CodeMirror caret: as tall as the text, centred
				// in the taller code line.
				if text := ed.codeOptions.FontSize * 1.3; text < h {
					top += (h - text) / 2
					h = text
				}
			}
			p.s.Ops = append(p.s.Ops, scene.Op{Kind: scene.OpFill, Rect: p.snap(Rect{ox + x, top, 0, h}), Color: t.Accent.scene(), Wide: p.wide(t.Accent, Color{}, Color{}), Opacity: p.opacity})
			op := &p.s.Ops[len(p.s.Ops)-1]
			op.Rect.W = max(round(2*p.scale), 1) // 2px, as in Yaak
		}
		if phase < 30*time.Second {
			e.c.After(blink - phase%blink)
		}
	}
}
