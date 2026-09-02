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

	if parts := hiddenParts(nil, nil, defaultClickableRoles); parts != nil {
		t.Fatalf("hiddenParts without a walker returned %v, want nil", parts)
	}
}
