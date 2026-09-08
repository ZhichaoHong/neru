//go:build windows

package windows

import (
	"image"
	"testing"
)

// synthCaption is a window layout to probe against: three buttons in a caption
// band, everything else caption or client. Real windows answer this way; building
// one here is what lets the search be tested without a window to hit-test.
type synthCaption struct {
	band    image.Rectangle
	buttons map[hitCode]image.Rectangle
	probes  int
}

func (s *synthCaption) probe(point image.Point) (hitCode, bool) {
	s.probes++

	for code, bounds := range s.buttons {
		if point.In(bounds) {
			return code, true
		}
	}

	if point.In(s.band) {
		return 2, true // HTCAPTION
	}

	return 1, true // HTCLIENT
}

// rightAlignedCaption is the common layout: 45x32 buttons at the right edge of a
// 32-pixel band, at 100% scaling.
func rightAlignedCaption() *synthCaption {
	return &synthCaption{
		band: image.Rect(0, 0, 800, 32),
		buttons: map[hitCode]image.Rectangle{
			htMinButton: image.Rect(600, 0, 645, 32),
			htMaxButton: image.Rect(645, 0, 690, 32),
			htClose:     image.Rect(690, 0, 735, 32),
		},
	}
}

func TestScanCaptionButtonsFindsExactBounds(t *testing.T) {
	t.Parallel()

	window := rightAlignedCaption()

	buttons := scanCaptionButtons(image.Rect(0, 0, 800, 40), 1, window.probe)

	want := []CaptionButton{
		{Kind: CaptionMinimize, Bounds: image.Rect(600, 0, 645, 32)},
		{Kind: CaptionMaximize, Bounds: image.Rect(645, 0, 690, 32)},
		{Kind: CaptionClose, Bounds: image.Rect(690, 0, 735, 32)},
	}

	assertButtons(t, want, buttons)

	// The bisection is there to make the rects exact, but it must not make the
	// search expensive: hints activation waits on it.
	if window.probes > 400 {
		t.Errorf("probes = %d, want a bounded search", window.probes)
	}
}

func TestScanCaptionButtonsScalesWithMonitor(t *testing.T) {
	t.Parallel()

	// The same caption on a 200% monitor: every dimension doubled. A search that
	// stepped in physical pixels would sample half as densely and, at higher
	// scaling, step over a button entirely.
	window := &synthCaption{
		band: image.Rect(0, 0, 1600, 64),
		buttons: map[hitCode]image.Rectangle{
			htMinButton: image.Rect(1200, 0, 1290, 64),
			htMaxButton: image.Rect(1290, 0, 1380, 64),
			htClose:     image.Rect(1380, 0, 1470, 64),
		},
	}

	buttons := scanCaptionButtons(image.Rect(0, 0, 1600, 80), 2, window.probe)

	assertButtons(t, []CaptionButton{
		{Kind: CaptionMinimize, Bounds: image.Rect(1200, 0, 1290, 64)},
		{Kind: CaptionMaximize, Bounds: image.Rect(1290, 0, 1380, 64)},
		{Kind: CaptionClose, Bounds: image.Rect(1380, 0, 1470, 64)},
	}, buttons)
}

func TestScanCaptionButtonsFindsLeftAlignedCaption(t *testing.T) {
	t.Parallel()

	// A right-to-left layout puts the buttons on the left. The right edge is
	// still searched first, because every other layout has them there.
	window := &synthCaption{
		band: image.Rect(0, 0, 800, 32),
		buttons: map[hitCode]image.Rectangle{
			htClose:     image.Rect(8, 0, 53, 32),
			htMaxButton: image.Rect(53, 0, 98, 32),
			htMinButton: image.Rect(98, 0, 143, 32),
		},
	}

	buttons := scanCaptionButtons(image.Rect(0, 0, 800, 40), 1, window.probe)

	assertButtons(t, []CaptionButton{
		{Kind: CaptionMinimize, Bounds: image.Rect(98, 0, 143, 32)},
		{Kind: CaptionMaximize, Bounds: image.Rect(53, 0, 98, 32)},
		{Kind: CaptionClose, Bounds: image.Rect(8, 0, 53, 32)},
	}, buttons)
}

func TestScanCaptionButtonsIgnoresWindowWithoutButtons(t *testing.T) {
	t.Parallel()

	window := &synthCaption{band: image.Rect(0, 0, 800, 32)}

	if buttons := scanCaptionButtons(image.Rect(0, 0, 800, 40), 1, window.probe); buttons != nil {
		t.Fatalf("buttons = %v, want none", buttons)
	}
}

func TestScanCaptionButtonsRejectsImplausibleRun(t *testing.T) {
	t.Parallel()

	// A window is free to answer a button code across its whole frame. Sizing a
	// hint from that would put a badge over half the window.
	window := &synthCaption{
		band: image.Rect(0, 0, 800, 32),
		buttons: map[hitCode]image.Rectangle{
			htClose: image.Rect(0, 0, 800, 32),
		},
	}

	if buttons := scanCaptionButtons(
		image.Rect(0, 0, 800, 40),
		1,
		window.probe,
	); len(
		buttons,
	) != 0 {
		t.Fatalf("buttons = %v, want none", buttons)
	}
}

func TestScanCaptionButtonsAbandonsUnansweredProbe(t *testing.T) {
	t.Parallel()

	// A spent budget or a hung window stops the search outright: two buttons
	// placed and the third guessed is worse than none, because a badge still
	// invites the click.
	window := rightAlignedCaption()

	limited := func(point image.Point) (hitCode, bool) {
		if window.probes >= 12 {
			return 0, false
		}

		return window.probe(point)
	}

	if buttons := scanCaptionButtons(image.Rect(0, 0, 800, 40), 1, limited); buttons != nil {
		t.Fatalf("buttons = %v, want none", buttons)
	}
}

func TestScanCaptionButtonsRejectsUnusableInput(t *testing.T) {
	t.Parallel()

	window := rightAlignedCaption()

	if buttons := scanCaptionButtons(image.Rectangle{}, 1, window.probe); buttons != nil {
		t.Errorf("empty area: buttons = %v, want none", buttons)
	}

	if buttons := scanCaptionButtons(image.Rect(0, 0, 800, 40), 1, nil); buttons != nil {
		t.Errorf("nil probe: buttons = %v, want none", buttons)
	}
}

func TestPackScreenPointRefusesUnreachableCoordinates(t *testing.T) {
	t.Parallel()

	// WM_NCHITTEST carries the point as two signed 16-bit words, so a coordinate
	// outside that range cannot be asked about. Wrapping it would probe a point
	// somewhere else entirely.
	if _, ok := packScreenPoint(image.Pt(40000, 10)); ok {
		t.Error("packScreenPoint accepted an x beyond int16")
	}

	if _, ok := packScreenPoint(image.Pt(10, -40000)); ok {
		t.Error("packScreenPoint accepted a y beyond int16")
	}

	// A negative coordinate is ordinary: a monitor left of the primary one has
	// them.
	packed, ok := packScreenPoint(image.Pt(-1600, 24))
	if !ok {
		t.Fatal("packScreenPoint rejected a point on a monitor left of the primary")
	}

	if got := int16(uint16(packed & 0xFFFF)); got != -1600 {
		t.Errorf("packed x = %d, want -1600", got)
	}

	if got := int16(uint16(packed >> 16)); got != 24 {
		t.Errorf("packed y = %d, want 24", got)
	}
}

func assertButtons(t *testing.T, want []CaptionButton, got []CaptionButton) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d buttons (%v), want %d", len(got), got, len(want))
	}

	for i, button := range want {
		if got[i] != button {
			t.Errorf("button %d = %+v, want %+v", i, got[i], button)
		}
	}
}
