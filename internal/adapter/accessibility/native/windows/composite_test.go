//go:build windows

package windows

import "testing"

// TestCompositeRolesAreEnumeratedByDefault pins the link between the two lists.
// The raw-view descent only runs for an element that already passed the role
// filter, so a composite role missing from the shipped clickable roles would
// leave the descent permanently unreachable and the wrapper's misplaced badge in
// place, with nothing failing to say so.
func TestCompositeRolesAreEnumeratedByDefault(t *testing.T) {
	t.Parallel()

	for role := range compositeRoles {
		if _, ok := defaultClickableRoles[role]; !ok {
			t.Errorf(
				"composite role %q is not in the default clickable roles, so its "+
					"parts are never reached",
				role,
			)
		}
	}
}

// TestHiddenPartsWithoutAWalker covers the degraded path. get_RawViewWalker is
// allowed to fail, and when it does every composite control has to keep the
// wrapper rather than lose its badge entirely.
func TestHiddenPartsWithoutAWalker(t *testing.T) {
	t.Parallel()

	if parts := hiddenParts(nil, nil, nil, "New Tab", defaultClickableRoles); parts != nil {
		t.Fatalf("hiddenParts without a walker returned %v, want nil", parts)
	}
}

// TestSearchableName fixes what counts as a name a user can search for. The
// Segoe MDL2 case is the one that matters: a non-empty name that looks present in
// every debug dump and matches nothing a keyboard can send.
func TestSearchableName(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		name string
		want bool
	}{
		"plain":               {name: "New Tab", want: true},
		"empty":               {name: "", want: false},
		"whitespace":          {name: " \t ", want: false},
		"newline only":        {name: "\n", want: false},
		"segoe glyph":         {name: "", want: false},
		"glyph beside a word": {name: " Close", want: true},
		"non latin":           {name: "新しいタブ", want: true},
		"punctuation":         {name: "+", want: true},
	}

	for label, testCase := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			if got := searchableName(testCase.name); got != testCase.want {
				t.Errorf("searchableName(%q) = %v, want %v", testCase.name, got, testCase.want)
			}
		})
	}
}
