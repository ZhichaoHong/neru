//go:build windows

package windows

import "unicode"

// Element titles as the rest of neru is entitled to receive them: human-readable
// text, or nothing.
//
// A UIA name is whatever the provider decided to publish, and providers publish
// things that are not text. WinUI names icon-only buttons with the Segoe MDL2
// codepoint the glyph is drawn from, so Terminal's new-tab button is named U+E710.
// Teams publishes a Text element whose entire name is a bare newline. Both arrive
// as non-empty strings that look like titles in a debug dump and behave like
// nothing at all.

// searchableName reports whether a name gives the user something to type.
//
// Both the live hints filter and --filter-text match on the title, so a name made
// only of characters no keyboard produces is not a label, it is a dead search key.
// A name that mixes the two - a glyph next to a word - is kept whole: the word is
// still reachable, and guessing which half the provider meant is worse than
// leaving it alone.
func searchableName(name string) bool {
	for _, char := range name {
		if unicode.IsGraphic(char) &&
			!unicode.IsSpace(char) &&
			!unicode.Is(unicode.Co, char) {
			return true
		}
	}

	return false
}

// usableName returns the name to carry as an element's title, which is the name
// itself or empty.
//
// Empty is the honest answer for a name nothing can match, and it is the case the
// pipeline downstream already handles: an unnamed element still gets a badge, and
// both the search filter and the merge dedupe skip a blank title rather than key
// on it. Keeping the raw glyph instead only means a title that matches nothing but
// still counts as present.
func usableName(name string) string {
	if !searchableName(name) {
		return ""
	}

	return name
}
