//go:build windows

package windows

import (
	winplatform "github.com/y3owk1n/neru/internal/adapter/platform/windows"
)

// Caption buttons for windows that do not publish them.
//
// A window drawing its own frame - Teams, Explorer's newer views, most Electron
// apps - paints minimize, maximize and close into its client area and exposes no
// UIA element behind them, so hints had nothing to attach a badge to. The
// platform layer reconstructs the three rects by hit-testing the caption; this
// turns them into elements the rest of the hint pipeline already knows how to
// handle.
//
// Vision cannot cover the gap on Windows: the strategy there is OCR only, and
// caption glyphs are icon-font characters that a text recognizer does not read.

// captionButtonElements returns synthesized elements for the window's caption
// buttons, minus any the accessibility tree already found.
//
// Buttons are gated on the same role filter as everything else, so a user who
// drops "button" from hints.clickable_roles stops getting these too. The gate is
// checked before the probing so the messages are not sent when the result would
// be discarded.
func captionButtonElements(
	hwnd uintptr,
	keptRoles map[string]struct{},
	existing []winElement,
) []winElement {
	if hwnd == 0 {
		return nil
	}

	if _, wanted := effectiveClickableRoles(keptRoles)[uiaControlButton]; !wanted {
		return nil
	}

	return mergeCaptionButtons(existing, winplatform.CaptionButtons(hwnd))
}

// mergeCaptionButtons converts caption buttons into elements, dropping those an
// existing element already covers.
//
// The dedupe is what keeps a window that publishes its caption buttons *and*
// answers the hit test - Edge does both - from getting two badges on each.
// Containment is judged by the existing element's center falling inside the
// probed rect, rather than the reverse: the two rects for the same button differ
// by a few pixels either way, while a pane spanning the whole caption would
// contain all three probed centers and suppress every button.
func mergeCaptionButtons(existing []winElement, buttons []winplatform.CaptionButton) []winElement {
	if len(buttons) == 0 {
		return nil
	}

	synthesized := make([]winElement, 0, len(buttons))

	for _, button := range buttons {
		if coveredByExisting(existing, button) {
			continue
		}

		synthesized = append(synthesized, winElement{
			bounds:    button.Bounds,
			role:      uiaControlButton,
			name:      button.Kind.String(),
			clickable: true,
		})
	}

	return synthesized
}

func coveredByExisting(existing []winElement, button winplatform.CaptionButton) bool {
	for _, element := range existing {
		if element.bounds.Empty() {
			continue
		}

		center := element.bounds.Min.Add(element.bounds.Max).Div(2) //nolint:mnd
		if center.In(button.Bounds) {
			return true
		}
	}

	return false
}
