//go:build !darwin || darwin

package vision

import (
	"image"
	"testing"
)

// The classifier's answer is a native role name, so every case here runs
// against both vocabularies that have a vision backend: macOS's AX names and
// Linux's AT-SPI names. Asserting a literal "AXButton" would pass on the
// developer's machine and say nothing about the platform the OCR backend runs
// on — which is the bug classifier_roles.go exists to prevent. Each case names
// the field it expects rather than the string, and the vocabulary supplies the
// string.
var classifierVocabularies = map[string]classifierRoles{
	"ax":    axClassifierRoles,
	"atspi": atspiClassifierRoles,
}

func TestRegionClassifier_Classify(t *testing.T) {
	tests := []struct {
		name      string
		region    DetectedRegion
		want      func(classifierRoles) string
		clickable bool
	}{
		{
			// A typical button: 120x30, centered text, high saliency.
			name: "text with button geometry",
			region: DetectedRegion{
				Bounds: image.Rect(100, 100, 220, 130),
				Score:  0.8,
				IsText: true,
				Label:  labelSubmit,
			},
			want:      func(r classifierRoles) string { return r.Button },
			clickable: true,
		},
		{
			// A link: wide and short text.
			name: "wide short text",
			region: DetectedRegion{
				Bounds: image.Rect(50, 200, 300, 230),
				Score:  0.6,
				IsText: true,
				Label:  "Click here for more information",
			},
			want:      func(r classifierRoles) string { return r.Link },
			clickable: true,
		},
		{
			// Tall narrow text block (aspect ratio < 1.2) with low score.
			name: "tall narrow low-confidence text",
			region: DetectedRegion{
				Bounds: image.Rect(10, 10, 40, 60),
				Score:  0.2,
				IsText: true,
				Label:  "Hi",
			},
			want:      func(r classifierRoles) string { return r.StaticText },
			clickable: false,
		},
		{
			// A 32x32 square icon: not a checkbox (too big), not a large image.
			name:      "small square icon",
			region:    DetectedRegion{Bounds: image.Rect(0, 0, 32, 32), Score: 0.4},
			want:      func(r classifierRoles) string { return r.Button },
			clickable: true,
		},
		{
			// A 64x64 region is large enough to be an actual image, not an icon.
			name:      "large square region",
			region:    DetectedRegion{Bounds: image.Rect(0, 0, 64, 64), Score: 0.5},
			want:      func(r classifierRoles) string { return r.Image },
			clickable: false,
		},
		{
			name:      "small square region",
			region:    DetectedRegion{Bounds: image.Rect(10, 10, 26, 26), Score: 0.3},
			want:      func(r classifierRoles) string { return r.CheckBox },
			clickable: true,
		},
		{
			// A region with no area is a bug in the caller rather than a
			// finding about the screen, and the classifier says so instead of
			// guessing.
			name: "degenerate region",
			region: DetectedRegion{
				Bounds: image.Rect(10, 10, 10, 10),
				Score:  0.9,
				IsText: true,
			},
			want:      func(r classifierRoles) string { return r.Unknown },
			clickable: false,
		},
	}

	for vocabulary, roles := range classifierVocabularies {
		for _, test := range tests {
			t.Run(vocabulary+"/"+test.name, func(t *testing.T) {
				classifier := &regionClassifier{roles: roles}

				role, clickable := classifier.Classify(test.region)
				if want := test.want(roles); role != want {
					t.Errorf("role %q, want %q", role, want)
				}

				if clickable != test.clickable {
					t.Errorf("clickable %v, want %v", clickable, test.clickable)
				}
			})
		}
	}
}

func TestMergeRegions_NonOverlapping(t *testing.T) {
	regions := []DetectedRegion{
		{Bounds: image.Rect(0, 0, 50, 50), Score: 0.9},
		{Bounds: image.Rect(100, 0, 150, 50), Score: 0.8},
	}

	merged := MergeRegions(regions, 0.5)
	if len(merged) != 2 {
		t.Errorf("expected 2 regions, got %d", len(merged))
	}
}

func TestMergeRegions_Overlapping(t *testing.T) {
	regions := []DetectedRegion{
		{Bounds: image.Rect(0, 0, 100, 100), Score: 0.9},
		{Bounds: image.Rect(10, 10, 90, 90), Score: 0.5}, // high IoU with first
	}

	merged := MergeRegions(regions, 0.5)
	if len(merged) != 1 {
		t.Errorf("expected 1 merged region, got %d", len(merged))
	}
}

func TestMergeRegions_PartialOverlap(t *testing.T) {
	regions := []DetectedRegion{
		{Bounds: image.Rect(0, 0, 50, 50), Score: 0.9},
		{Bounds: image.Rect(30, 30, 80, 80), Score: 0.8}, // partial overlap
	}

	merged := MergeRegions(regions, 0.5)
	if len(merged) != 2 {
		t.Errorf("expected 2 regions for partial overlap, got %d", len(merged))
	}
}

func TestTestClassifier(t *testing.T) {
	testClassifier := NewTestClassifier()

	region := DetectedRegion{
		Bounds: image.Rect(100, 100, 200, 130),
		Score:  0.7,
		IsText: true,
		Label:  "OK",
	}

	role, clickable := testClassifier.Classify(region)
	if role == "" {
		t.Errorf("expected non-empty role")
	}

	// Should classify as button (aspect ratio ~3.3, score 0.7, text) in
	// whatever vocabulary the running platform speaks.
	if want := currentClassifierRoles().Button; role != want || !clickable {
		t.Errorf("expected %s/clickable, got %s/%v", want, role, clickable)
	}
}

// TestRegionClassifier_ButtonConfidenceGate pins the one config value that
// decides whether a score-free OCR engine can produce clickable hints at all.
//
// Windows.Media.Ocr reports no per-word confidence, so every word arrives
// scoring 0. With the cross-platform floor of 0.3 that region is static text and
// cannot be hinted; with the Windows default of 0 geometry decides, which is
// what the other platforms' scores effectively do anyway.
func TestRegionClassifier_ButtonConfidenceGate(t *testing.T) {
	// Button geometry: 120x30, aspect 4.0, inside the 0.8-8.0 window.
	region := DetectedRegion{
		Bounds: image.Rect(100, 100, 220, 130),
		Score:  0,
		IsText: true,
		Label:  labelSubmit,
	}

	tests := []struct {
		name      string
		minConf   float64
		want      func(classifierRoles) string
		clickable bool
	}{
		{
			name:      "a positive floor rejects an unscored word",
			minConf:   0.3,
			want:      func(r classifierRoles) string { return r.StaticText },
			clickable: false,
		},
		{
			name:      "a zero floor leaves geometry to decide",
			minConf:   0,
			want:      func(r classifierRoles) string { return r.Button },
			clickable: true,
		},
	}

	for vocabulary, roles := range classifierVocabularies {
		for _, test := range tests {
			t.Run(vocabulary+"/"+test.name, func(t *testing.T) {
				classifier := &regionClassifier{roles: roles}
				classifier.cfg.ButtonMinConfidence = test.minConf

				role, clickable := classifier.Classify(region)
				if want := test.want(roles); role != want {
					t.Errorf("role %q, want %q", role, want)
				}

				if clickable != test.clickable {
					t.Errorf("clickable %v, want %v", clickable, test.clickable)
				}
			})
		}
	}
}

// TestMergeRegions_TiebreakIsDeterministic covers suppression when no region
// carries a score. Sorting by score alone leaves the survivor of an overlapping
// pair decided by the order the OCR engine emitted them in, so the same screen
// would hint differently between runs of different engines.
func TestMergeRegions_TiebreakIsDeterministic(t *testing.T) {
	big := DetectedRegion{Bounds: image.Rect(0, 0, 100, 100)}
	small := DetectedRegion{Bounds: image.Rect(10, 10, 90, 90)}

	for _, test := range []struct {
		name  string
		input []DetectedRegion
	}{
		{name: "larger first", input: []DetectedRegion{big, small}},
		{name: "smaller first", input: []DetectedRegion{small, big}},
	} {
		t.Run(test.name, func(t *testing.T) {
			merged := MergeRegions(test.input, 0.5)
			if len(merged) != 1 {
				t.Fatalf("got %d regions, want 1", len(merged))
			}

			if merged[0].Bounds != big.Bounds {
				t.Errorf("survivor %v, want the larger %v", merged[0].Bounds, big.Bounds)
			}
		})
	}
}

// TestMergeRegions_TiebreakFallsBackToPosition covers two unscored regions of
// equal area, where only reading order separates them.
func TestMergeRegions_TiebreakFallsBackToPosition(t *testing.T) {
	upper := DetectedRegion{Bounds: image.Rect(0, 0, 100, 100)}
	lower := DetectedRegion{Bounds: image.Rect(5, 10, 105, 110)}

	merged := MergeRegions([]DetectedRegion{lower, upper}, 0.5)
	if len(merged) != 1 {
		t.Fatalf("got %d regions, want 1", len(merged))
	}

	if merged[0].Bounds != upper.Bounds {
		t.Errorf("survivor %v, want the upper-left %v", merged[0].Bounds, upper.Bounds)
	}
}
