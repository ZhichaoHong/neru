//go:build windows

package windows

import (
	"image"
	"testing"

	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
)

func TestMergeCaptionButtonsSynthesizesMissingButtons(t *testing.T) {
	t.Parallel()

	buttons := []winplatform.CaptionButton{
		{Kind: winplatform.CaptionMinimize, Bounds: image.Rect(600, 0, 645, 32)},
		{Kind: winplatform.CaptionMaximize, Bounds: image.Rect(645, 0, 690, 32)},
		{Kind: winplatform.CaptionClose, Bounds: image.Rect(690, 0, 735, 32)},
	}

	// What Teams gives: plenty of elements, none of them the caption buttons.
	existing := []winElement{
		{bounds: image.Rect(0, 0, 800, 32), role: uiaControlPane, clickable: true},
		{
			bounds:    image.Rect(500, 4, 524, 28),
			role:      uiaControlButton,
			name:      "Avatar",
			clickable: true,
		},
	}

	got := mergeCaptionButtons(existing, buttons)

	want := []winElement{
		{
			bounds:    image.Rect(600, 0, 645, 32),
			role:      uiaControlButton,
			name:      "Minimize",
			clickable: true,
		},
		{
			bounds:    image.Rect(645, 0, 690, 32),
			role:      uiaControlButton,
			name:      "Maximize",
			clickable: true,
		},
		{
			bounds:    image.Rect(690, 0, 735, 32),
			role:      uiaControlButton,
			name:      "Close",
			clickable: true,
		},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d elements (%+v), want %d", len(got), got, len(want))
	}

	for i, element := range want {
		if got[i] != element {
			t.Errorf("element %d = %+v, want %+v", i, got[i], element)
		}
	}
}

func TestMergeCaptionButtonsSkipsPublishedButtons(t *testing.T) {
	t.Parallel()

	// What Edge gives: the caption buttons are in the accessibility tree already,
	// with rects a few pixels off the probed ones. Synthesizing them again would
	// put two badges on each button.
	existing := []winElement{
		{
			bounds:    image.Rect(1608, 2, 1677, 34),
			role:      uiaControlButton,
			name:      "Minimize",
			clickable: true,
		},
		{
			bounds:    image.Rect(1677, 2, 1746, 34),
			role:      uiaControlButton,
			name:      "Maximize",
			clickable: true,
		},
	}

	buttons := []winplatform.CaptionButton{
		{Kind: winplatform.CaptionMinimize, Bounds: image.Rect(1606, 0, 1676, 36)},
		{Kind: winplatform.CaptionMaximize, Bounds: image.Rect(1676, 0, 1746, 36)},
		{Kind: winplatform.CaptionClose, Bounds: image.Rect(1746, 0, 1816, 36)},
	}

	got := mergeCaptionButtons(existing, buttons)

	if len(got) != 1 {
		t.Fatalf("got %d elements (%+v), want only the unpublished close button", len(got), got)
	}

	if got[0].name != "Close" {
		t.Errorf("element name = %q, want %q", got[0].name, "Close")
	}
}

func TestMergeCaptionButtonsWithoutButtons(t *testing.T) {
	t.Parallel()

	if got := mergeCaptionButtons(nil, nil); got != nil {
		t.Fatalf("got %+v, want none", got)
	}
}

func TestEffectiveClickableRolesGatesCaptionButtons(t *testing.T) {
	t.Parallel()

	if _, ok := effectiveClickableRoles(nil)[uiaControlButton]; !ok {
		t.Error("default roles exclude Button; caption buttons would never be synthesized")
	}

	if _, ok := effectiveClickableRoles(map[string]struct{}{uiaControlEdit: {}})[uiaControlButton]; ok {
		t.Error("an explicit role filter without Button still admitted it")
	}
}
