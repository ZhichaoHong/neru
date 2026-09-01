//go:build windows

package windows

import (
	"image"
	"time"

	"github.com/y3owk1n/neru/internal/adapter/platform/mousestate"
	"github.com/y3owk1n/neru/internal/domain/action"
)

// heldButtons records which mouse buttons Neru is currently holding down. It
// lives beside the code that posts the press and the release so that a cursor
// move can tell a drag from a hover.
var heldButtons mousestate.Tracker

// IsMouseButtonDown reports whether the given button is held down.
func IsMouseButtonDown(button action.MouseButton) bool {
	return heldButtons.IsDown(button)
}

// HeldMouseButtons returns the buttons currently held, in left, right, middle
// order.
func HeldMouseButtons() []action.MouseButton {
	return heldButtons.HeldButtons()
}

const (
	// dragStepDistance is the longest distance one step of a drag covers, in
	// pixels. Short drags get fewer steps than long ones rather than the same
	// count stretched over less ground.
	dragStepDistance = 48

	// dragStepCountMin and dragStepCountMax bound the walk: two steps so that
	// even a few pixels of travel arrive as movement rather than a jump, and a
	// ceiling so a drag across a wide desktop does not pay for a step per 48px.
	dragStepCountMin = 2
	dragStepCountMax = 10

	// dragStepInterval is the pause after each step. It is what gives the
	// window under the cursor a chance to run its own drag loop between our
	// moves; without it the whole walk lands in its queue as one burst and the
	// button-up is already behind it.
	dragStepInterval = 24 * time.Millisecond
)

// moveCursorDragging walks the cursor to point one step at a time.
//
// A single SetCursorPos is enough to move the cursor but not to drag with it. A
// source window starts a drag only once the cursor has left the press point by
// the system drag threshold, and the modal drop loop it then enters needs a
// further move over the destination before it has a drop target to release
// onto. One jump followed straight away by the button-up gives it neither, so
// the drop is abandoned and the press and release read as a plain click on the
// destination.
func moveCursorDragging(point image.Point) error {
	from, err := cursorPosition()
	if err != nil {
		return setCursorPos(point)
	}

	for _, step := range dragPath(from, point) {
		err = setCursorPos(step)
		if err != nil {
			return err
		}

		time.Sleep(dragStepInterval)
	}

	return nil
}

// dragPath returns the positions a drag from one point to the other passes
// through, the last of which is the destination itself. The step count scales
// with the distance covered, so a short drag does not pay for as many steps as
// one across the desktop.
func dragPath(from, dest image.Point) []image.Point {
	span := max(abs(dest.X-from.X), abs(dest.Y-from.Y))
	steps := min(max(span/dragStepDistance, dragStepCountMin), dragStepCountMax)

	path := make([]image.Point, 0, steps)

	for step := 1; step < steps; step++ {
		path = append(path, image.Point{
			X: from.X + (dest.X-from.X)*step/steps,
			Y: from.Y + (dest.Y-from.Y)*step/steps,
		})
	}

	return append(path, dest)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}

	return value
}
