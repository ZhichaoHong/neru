package ports_test

import (
	"image"
	"testing"

	"github.com/y3owk1n/neru/internal/domain/element"
	"github.com/y3owk1n/neru/internal/ports"
)

func filterElement(
	t *testing.T,
	role element.Role,
	opts ...element.Option,
) *element.Element {
	t.Helper()

	built, err := element.NewElement(
		element.ID("test-id"),
		image.Rect(0, 0, 100, 100),
		role,
		opts...,
	)
	if err != nil {
		t.Fatalf("building element: %v", err)
	}

	return built
}

func TestElementFilterMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		elem   func(t *testing.T) *element.Element
		filter ports.ElementFilter
		want   bool
	}{
		{
			name: "an element of a wanted role matches",
			elem: func(t *testing.T) *element.Element {
				return filterElement(t, element.RoleButton)
			},
			filter: ports.ElementFilter{Roles: []element.Role{element.RoleButton}},
			want:   true,
		},
		{
			name: "an element of an unwanted role does not match",
			elem: func(t *testing.T) *element.Element {
				return filterElement(t, element.RoleButton)
			},
			filter: ports.ElementFilter{Roles: []element.Role{element.RoleLink}},
			want:   false,
		},
		{
			name: "an excluded role loses even when it is also wanted",
			elem: func(t *testing.T) *element.Element {
				return filterElement(t, element.RoleButton)
			},
			filter: ports.ElementFilter{
				Roles:        []element.Role{element.RoleButton},
				ExcludeRoles: []element.Role{element.RoleButton},
			},
			want: false,
		},
		{
			name: "an element at least the minimum size matches",
			elem: func(t *testing.T) *element.Element {
				return filterElement(t, element.RoleButton)
			},
			filter: ports.ElementFilter{MinSize: image.Point{X: 50, Y: 50}},
			want:   true,
		},
		{
			name: "an element under the minimum size does not match",
			elem: func(t *testing.T) *element.Element {
				return filterElement(t, element.RoleButton)
			},
			filter: ports.ElementFilter{MinSize: image.Point{X: 200, Y: 200}},
			want:   false,
		},
		{
			name: "extra search text is searched as well as the value",
			elem: func(t *testing.T) *element.Element {
				return filterElement(
					t,
					element.RoleButton,
					element.WithSearchText("Apple Notes row"),
				)
			},
			filter: ports.ElementFilter{ValueContains: "notes"},
			want:   true,
		},
		{
			// The filter's own terms are not folded here - the caller owns that,
			// because they are fixed for a whole activation while the elements are
			// not. Stated as a test so nobody re-adds folding on this side and
			// pays for it per element.
			name: "an unfolded term does not match, which is the caller's contract",
			elem: func(t *testing.T) *element.Element {
				return filterElement(
					t,
					element.RoleButton,
					element.WithTitle("Save"),
				)
			},
			filter: ports.ElementFilter{TitleContains: "SAVE"},
			want:   false,
		},
		{
			// The bug this predicate moved house to fix. An OCR element sets both
			// Title and SearchText to the recognized string, so a text filter
			// reaches it through any of the four fields - and before the move there
			// was no implementation the vision path could call at all.
			name: "a recognized OCR region matches on its text",
			elem: func(t *testing.T) *element.Element {
				return filterElement(
					t,
					element.Role("AXStaticText"),
					element.WithVisionOnly(),
					element.WithTitle("Downloads"),
					element.WithSearchText("Downloads"),
				)
			},
			filter: ports.ElementFilter{
				TitleContains:       "download",
				DescriptionContains: "download",
				ValueContains:       "download",
			},
			want: true,
		},
		{
			name: "an OCR region whose text does not contain the term is dropped",
			elem: func(t *testing.T) *element.Element {
				return filterElement(
					t,
					element.Role("AXStaticText"),
					element.WithVisionOnly(),
					element.WithTitle("Documents"),
					element.WithSearchText("Documents"),
				)
			},
			filter: ports.ElementFilter{
				TitleContains:       "download",
				DescriptionContains: "download",
				ValueContains:       "download",
			},
			want: false,
		},
		{
			name: "a later term in the list matches on its own",
			elem: func(t *testing.T) *element.Element {
				return filterElement(
					t,
					element.RoleButton,
					element.WithTitle("Cancel"),
				)
			},
			filter: ports.ElementFilter{
				TitleContains:    "ok",
				TextContainsList: []string{"cancel"},
			},
			want: true,
		},
		{
			// Setting no text term means text is not consulted, which is not the
			// same as matching nothing.
			name: "an element with no text survives a filter with no text terms",
			elem: func(t *testing.T) *element.Element {
				return filterElement(t, element.RoleButton)
			},
			filter: ports.ElementFilter{Roles: []element.Role{element.RoleButton}},
			want:   true,
		},
		{
			// An empty term must not behave like strings.Contains, which reports
			// true for the empty needle against anything.
			name: "an empty term in the list does not match everything",
			elem: func(t *testing.T) *element.Element {
				return filterElement(
					t,
					element.RoleButton,
					element.WithTitle("Save"),
				)
			},
			filter: ports.ElementFilter{
				TextContainsList: []string{"", "nothingalike"},
			},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.filter.Matches(test.elem(t)); got != test.want {
				t.Errorf("Matches() = %v, want %v", got, test.want)
			}
		})
	}
}
