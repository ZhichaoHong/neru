//go:build windows

package windows

import "unsafe"

// Controls whose actionable parts UI Automation hides from the control view.
//
// WinUI's SplitButton is the case that forced this. It publishes one element for
// the whole control, carrying both the Invoke and the ExpandCollapse pattern, and
// a FindAll never returns the two Buttons it is really made of. Windows
// Terminal's new-tab control is 96px wide: the primary half ends at x+47 and the
// dropdown half starts at x+49, so the wrapper's center - the point every hint
// clicks - lands in the seam and hits neither half.
//
// The parts are there in the raw view. Descending it is keyed on control type
// rather than attempted everywhere because the raw view of a XAML window is
// mostly layout scaffolding: panels, borders, presenters. None of that should
// carry a badge, and walking it for every element would cost a COM round trip
// per node to find that out.
var compositeRoles = map[string]struct{}{
	uiaControlSplitButton: {},
}

// hiddenParts returns the wrapper's immediate raw-view children that qualify as
// hint targets in their own right.
//
// A part left unnamed inherits wrapperName. Splitting the wrapper into parts
// otherwise trades a misplaced badge for an unsearchable one: Terminal's
// SplitButton is named "New Tab" and its primary half is named with the icon
// glyph, which usableName drops, so the name worth searching for lives on the
// element being discarded.
//
// An empty result means the caller keeps the wrapper. A provider that publishes
// no parts, or whose parts the role filter rejects, still deserves a badge -
// approximately placed beats absent.
//
// One level only. The parts of a composite control are its direct children, and
// each level below that is scaffolding rather than something to click.
func hiddenParts(
	walker, wrapper unsafe.Pointer,
	wrapperName string,
	keptRoles map[string]struct{},
) []winElement {
	if walker == nil || wrapper == nil {
		return nil
	}

	inheritable := searchableName(wrapperName)

	var child unsafe.Pointer

	hresult := comCall(
		walker,
		vtWalkerGetFirstChild,
		uintptr(wrapper),
		uintptr(unsafe.Pointer(&child)),
	)
	if failed(hresult) {
		return nil
	}

	var parts []winElement

	for child != nil {
		extracted, ok := extractWinElement(child, keptRoles)
		if ok {
			if inheritable && extracted.name == "" {
				extracted.name = wrapperName
			}

			parts = append(parts, extracted)
		}

		// The sibling has to be fetched before the child is released: the walker
		// navigates from a live element.
		var sibling unsafe.Pointer

		if failed(comCall(
			walker,
			vtWalkerGetNextSibling,
			uintptr(child),
			uintptr(unsafe.Pointer(&sibling)),
		)) {
			sibling = nil
		}

		comCall(child, vtRelease)

		child = sibling
	}

	return parts
}
