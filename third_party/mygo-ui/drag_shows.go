package ui

// DragShows makes the copy that follows the pointer as e drags its value
// (Drag) a copy of shown, as dnd-kit's DragOverlay shows a whole row
// dragged by its handle. The pointer keeps its place on the copy. Call it
// after Drag, once shown is built, in the frames that build e.
func (e *node) DragShows(shown *node) *node {
	d := e.c.rt.drag
	if d == nil || d.src != e.id || shown == nil {
		return e
	}
	if !d.shown {
		// Where the pointer pressed, from shown's corner rather than e's,
		// as the last frame laid them out.
		d.offX += e.st.x - shown.st.x
		d.offY += e.st.y - shown.st.y
		d.shown = true
	}
	d.elem = shown
	return e
}
