package ports

import (
	"slices"
	"strings"

	"github.com/y3owk1n/neru/internal/domain/element"
)

// Matches reports whether an element satisfies the filter.
//
// A method on the filter rather than on any adapter. It reads nothing but the
// filter's own fields and the element's, so every source of elements - an
// accessibility tree, OCR, anything later - gets the same answer for the same
// question. It used to live on the accessibility adapter and never touched that
// adapter's state, which is why the vision path silently had no text filtering at
// all: the only implementation was somewhere the vision path could not reach.
//
// Roles, ExcludeRoles and MinSize are AND conditions. The four text fields are OR
// among themselves: an element matches when any of title, description, value or one
// of TextContainsList hits. Setting no text field at all means text is not
// consulted, which is not the same as matching nothing.
//
// Text terms must be lowercased before they reach here. Callers own that because
// the terms are fixed for a whole activation while the elements are not, so
// lowercasing once at the filter is one allocation against one per element.
func (f ElementFilter) Matches(elem *element.Element) bool {
	bounds := elem.Bounds()
	if bounds.Dx() < f.MinSize.X || bounds.Dy() < f.MinSize.Y {
		return false
	}

	if len(f.Roles) > 0 && !slices.Contains(f.Roles, elem.Role()) {
		return false
	}

	if slices.Contains(f.ExcludeRoles, elem.Role()) {
		return false
	}

	if !f.wantsText() {
		return true
	}

	return f.matchesText(elem)
}

// wantsText reports whether any text term was set at all.
func (f ElementFilter) wantsText() bool {
	return f.TitleContains != "" || f.DescriptionContains != "" ||
		f.ValueContains != "" || len(f.TextContainsList) > 0
}

// matchesText is the OR across the four text fields. Only called once wantsText
// has said at least one of them carries a term.
func (f ElementFilter) matchesText(elem *element.Element) bool {
	title := strings.ToLower(elem.Title())
	description := strings.ToLower(elem.Description())
	value := strings.ToLower(textForFilter(elem))

	if contains(title, f.TitleContains) ||
		contains(description, f.DescriptionContains) ||
		contains(value, f.ValueContains) {
		return true
	}

	for _, term := range f.TextContainsList {
		if contains(title, term) || contains(description, term) ||
			contains(value, term) {
			return true
		}
	}

	return false
}

// contains is strings.Contains with the empty term meaning "no term set" rather
// than "matches everything", which is what strings.Contains would say.
func contains(haystack, term string) bool {
	if term == "" || haystack == "" {
		return false
	}

	return strings.Contains(haystack, term)
}

// textForFilter is the element's searchable text: its value, its extra search
// text, or both joined. An OCR element carries its recognized string in both
// Title and SearchText, so it is reachable through every text field a user can
// set.
func textForFilter(elem *element.Element) string {
	value := elem.Value()

	searchText := elem.SearchText()
	if searchText == "" {
		return value
	}

	if value == "" {
		return searchText
	}

	return value + " " + searchText
}
