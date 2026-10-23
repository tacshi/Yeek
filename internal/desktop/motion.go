package desktop

import (
	"time"

	"github.com/egoist/mygo/yeekui"
)

var (
	cssTransition     = ui.Animation{Duration: 150 * time.Millisecond, Ease: ui.CubicBezier(.4, 0, .2, 1)}
	dialogEnterFrames = []ui.Keyframe{{Value: 0}, {At: 100 * time.Millisecond, Value: 1, Ease: ui.EaseOut}}
)

func disclosure(c *ui.Context, open bool, size float32, color ui.Color) ui.Element {
	turn := float32(0)
	if open {
		turn = 90
	}
	return icon(c, "chevron").FontSize(size).TextColor(color).AnimatePose(ui.Pose{Rotate: turn}, cssTransition)
}

func dialogEnter(backdrop, panel ui.Element) {
	t := backdrop.Keyframes("enter", dialogEnterFrames)
	if t >= 1 {
		return
	}
	backdrop.Opacity(t)
	scale := .97 + .03*t
	panel.Translate(0, 5*(1-t)).Scale(scale, scale)
}

func dropdownEnter(backdrop, panel ui.Element) {
	t := backdrop.Keyframes("enter", dialogEnterFrames)
	if t >= 1 {
		return
	}
	scale := .98 + .02*t
	panel.Opacity(t).Translate(0, -5*(1-t)).Scale(scale, scale)
}
