//go:build windows

package vision

import (
	"image"
	"testing"

	"github.com/y3owk1n/neru/internal/config"
)

// TestWindowsDefaultConfidenceLetsTheClassifierWork ties the Windows platform
// default to the classifier that depends on it. They live in different packages
// and nothing else pins them together: config_windows.go sets
// button_min_confidence to 0 because Windows.Media.Ocr reports no confidence, and
// isLikelyButton compares a region's score against that value with no
// zero-means-unset fallback. Raise the default and every Windows word - all of
// them scoring 0 - stops being a Button, which is text found and then made
// unclickable rather than an error anybody sees.
//
// The region is 60x30 and text: aspect 2.0, inside the button band and below the
// link band, so the Button branch is the one the confidence gate decides.
func TestWindowsDefaultConfidenceLetsTheClassifierWork(t *testing.T) {
	t.Parallel()

	vision := config.DefaultConfig().Hints.Vision

	if vision.ButtonMinConfidence != 0 {
		t.Fatalf(
			"the Windows default button_min_confidence is %v; the OCR engine reports no "+
				"confidence, so anything above 0 suppresses every Button",
			vision.ButtonMinConfidence,
		)
	}

	classifier := newRegionClassifier(vision)

	role, clickable := classifier.Classify(DetectedRegion{
		Bounds: image.Rect(0, 0, 60, 30),
		IsText: true,
		Score:  0,
	})

	if !clickable {
		t.Errorf(
			"a button-shaped word scoring 0 classified as %q and unclickable; the "+
				"platform default and the confidence gate disagree",
			role,
		)
	}

	if want := currentClassifierRoles().Button; role != want {
		t.Errorf("Classify returned role %q, want %q", role, want)
	}
}
