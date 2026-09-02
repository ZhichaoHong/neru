//go:build windows

package windows

import "testing"

// nameCases are shared by both name helpers: searchableName answers want, and
// usableName returns the name itself exactly when want is true.
var nameCases = map[string]struct {
	name string
	want bool
}{
	"plain":               {name: "New Tab", want: true},
	"empty":               {name: "", want: false},
	"whitespace":          {name: " \t ", want: false},
	"newline only":        {name: "\n", want: false},
	"segoe glyph":         {name: "", want: false},
	"glyph beside a word": {name: " Close", want: true},
	//nolint:gosmopolitan // a published name in a non-Latin script is the case being pinned
	"non latin":   {name: "新しいタブ", want: true},
	"punctuation": {name: "+", want: true},
}

// TestSearchableName fixes what counts as a name a user can search for. The two
// cases that matter are the ones a provider actually publishes: a Segoe MDL2
// codepoint and a bare newline, both non-empty names that match nothing a keyboard
// can send.
func TestSearchableName(t *testing.T) {
	t.Parallel()

	for label, testCase := range nameCases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			if got := searchableName(testCase.name); got != testCase.want {
				t.Errorf("searchableName(%q) = %v, want %v", testCase.name, got, testCase.want)
			}
		})
	}
}

// TestUsableName pins the guarantee the rest of the adapter relies on: a title is
// either the provider's name or empty, never an untypeable string. hiddenParts
// keys its inheritance on the empty case, so a name that leaked through would
// silently stop a composite part from picking up the wrapper's name.
func TestUsableName(t *testing.T) {
	t.Parallel()

	for label, testCase := range nameCases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			want := ""
			if testCase.want {
				want = testCase.name
			}

			if got := usableName(testCase.name); got != want {
				t.Errorf("usableName(%q) = %q, want %q", testCase.name, got, want)
			}
		})
	}
}
