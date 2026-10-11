package desktop

import (
	"time"

	"github.com/egoist/mygo/yeekui"
)

// Yaak's motion: its MotionConfig gives what Motion animates 100 ms, and
// Tailwind's transition utilities take 150 ms along cubic-bezier(0.4, 0,
// 0.2, 1).
var (
	cssTransition     = ui.Animation{Duration: 150 * time.Millisecond, Ease: ui.CubicBezier(.4, 0, .2, 1)}
	dialogEnterFrames = []ui.Keyframe{{Value: 0}, {At: 100 * time.Millisecond, Value: 1, Ease: ui.EaseOut}}
)

// disclosure is a chevron that turns down as it opens, as Yaak's
// rotate-90 with transition-transform.
func disclosure(c *ui.Context, open bool, size float32, color ui.Color) ui.Element {
	turn := float32(0)
	if open {
		turn = 90
	}
	return icon(c, "chevron").FontSize(size).TextColor(color).AnimatePose(ui.Pose{Rotate: turn}, cssTransition)
}

// dialogEnter is Yaak's Dialog coming in in its Overlay: all of it fades
// in, and the panel rises 5px as it grows from 97% of its size.
func dialogEnter(backdrop, panel ui.Element) {
	t := backdrop.Keyframes("enter", dialogEnterFrames)
	if t >= 1 {
		return
	}
	backdrop.Opacity(t)
	scale := .97 + .03*t
	panel.Translate(0, 5*(1-t)).Scale(scale, scale)
}

// dropdownEnter is Yaak's Dropdown menu coming in: it fades in as it comes
// down 5px and grows from 98% of its size.
func dropdownEnter(backdrop, panel ui.Element) {
	t := backdrop.Keyframes("enter", dialogEnterFrames)
	if t >= 1 {
		return
	}
	scale := .98 + .02*t
	panel.Opacity(t).Translate(0, -5*(1-t)).Scale(scale, scale)
}
