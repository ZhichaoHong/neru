package native

import (
	"image"
	"slices"
	"testing"

	"github.com/y3owk1n/neru/internal/domain/action"
)

// The dispatch order between the two halves of the mouse API is what regresses,
// so it is asserted here rather than left to the platform adapters: a click and
// a hold both reach the same recorded button state, and only the shell decides
// what happens first.
//
// These tests swap the package-level platform vars, so none of them may run in
// parallel.

// stubMouse points every mouse var at a recorder and restores the platform
// bindings when the test ends. held is what IsMouseButtonDown reports.
func stubMouse(t *testing.T, held bool) *[]string {
	t.Helper()

	priorEnsureMouseUp := ensureMouseUp
	priorLeftClick := LeftClickAtPoint
	priorRightClick := RightClickAtPoint
	priorMiddleClick := MiddleClickAtPoint
	priorMouseDown := MouseDownAtPoint
	priorMouseUp := MouseUpAtPoint
	priorIsDown := IsMouseButtonDown

	t.Cleanup(func() {
		ensureMouseUp = priorEnsureMouseUp
		LeftClickAtPoint = priorLeftClick
		RightClickAtPoint = priorRightClick
		MiddleClickAtPoint = priorMiddleClick
		MouseDownAtPoint = priorMouseDown
		MouseUpAtPoint = priorMouseUp
		IsMouseButtonDown = priorIsDown
	})

	var calls []string

	record := func(name string) { calls = append(calls, name) }

	click := func(name string) func(image.Point, bool, action.Modifiers) error {
		return func(image.Point, bool, action.Modifiers) error {
			record(name)

			return nil
		}
	}

	ensureMouseUp = func() { record("release_held") }
	LeftClickAtPoint = click("left_click")
	RightClickAtPoint = click("right_click")
	MiddleClickAtPoint = click("middle_click")

	MouseDownAtPoint = func(image.Point, action.MouseButton, action.Modifiers) error {
		record("mouse_down")

		return nil
	}

	MouseUpAtPoint = func(image.Point, action.MouseButton, action.Modifiers) error {
		record("mouse_up")

		return nil
	}

	IsMouseButtonDown = func(action.MouseButton) bool { return held }

	return &calls
}

// TestClickReleasesAHeldButtonFirst covers the reason a toggle drag ended up
// behaving like two clicks. A click posts its own press and release, so it
// physically ends a drag whether or not the shell says so - and if the recorded
// hold is not cleared along with it, the next toggle resolves to a release for a
// button that is already up and does nothing.
func TestClickReleasesAHeldButtonFirst(t *testing.T) {
	tests := []struct {
		name       string
		actionType action.Type
		want       []string
	}{
		{
			name:       "left click",
			actionType: action.TypeLeftClick,
			want:       []string{"release_held", "left_click"},
		},
		{
			name:       "right click",
			actionType: action.TypeRightClick,
			want:       []string{"release_held", "right_click"},
		},
		{
			name:       "middle click",
			actionType: action.TypeMiddleClick,
			want:       []string{"release_held", "middle_click"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := stubMouse(t, true)

			err := New(nil, nil).PerformAction(test.actionType, image.Pt(10, 20), false, 0)
			if err != nil {
				t.Fatalf("performing %s: %v", test.actionType, err)
			}

			if !slices.Equal(*calls, test.want) {
				t.Errorf("called %v, want %v", *calls, test.want)
			}
		})
	}
}

// TestToggleResolvesAgainstTheRecordedHold states the other half of the
// contract: what a toggle does is decided by the recorded button state alone, so
// anything that changes the physical button has to keep that record honest.
func TestToggleResolvesAgainstTheRecordedHold(t *testing.T) {
	tests := []struct {
		name string
		held bool
		want []string
	}{
		{name: "a held button is released", held: true, want: []string{"mouse_up"}},
		{name: "a free button is pressed", held: false, want: []string{"mouse_down"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := stubMouse(t, test.held)

			err := New(nil, nil).
				PerformAction(action.TypeLeftMouseToggle, image.Pt(10, 20), false, 0)
			if err != nil {
				t.Fatalf("performing a left toggle: %v", err)
			}

			if !slices.Equal(*calls, test.want) {
				t.Errorf("called %v, want %v", *calls, test.want)
			}
		})
	}
}
