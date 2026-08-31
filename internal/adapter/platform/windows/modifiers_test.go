//go:build windows

package windows

import (
	"testing"

	"github.com/y3owk1n/neru/internal/domain/action"
)

// TestWindowsModifierKeysCoversEveryModifier pins the two properties PlanFor
// reads off this table.
//
// A modifier with no canonical key can be suppressed but never presented, so
// `--modifier cmd` would silently do nothing; a modifier with two of them gets
// pressed twice and released once, latching a key nobody is holding. And a
// virtual key listed twice is read once, under whichever entry claimed it first,
// so the other entry's side of the keyboard goes unsuppressed.
func TestWindowsModifierKeysCoversEveryModifier(t *testing.T) {
	t.Parallel()

	wanted := []action.Modifiers{
		action.ModShift,
		action.ModCtrl,
		action.ModAlt,
		action.ModCmd,
	}

	canonical := make(map[action.Modifiers]int, len(wanted))
	seen := make(map[uint16]struct{}, len(windowsModifierKeys))

	for _, key := range windowsModifierKeys {
		if _, repeat := seen[key.virtualKey]; repeat {
			t.Errorf("virtual key %#x is listed more than once", key.virtualKey)
		}

		seen[key.virtualKey] = struct{}{}

		if key.canonical {
			canonical[key.modifier]++
		}
	}

	for _, modifier := range wanted {
		if got := canonical[modifier]; got != 1 {
			t.Errorf("%v has %d canonical keys, want exactly 1", modifier, got)
		}
	}
}

// TestWindowsModifierKeysMarkExtendedKeys pins the side-telling half. Once a
// virtual key is resolved to a scancode, KEYEVENTF_EXTENDEDKEY is the only thing
// separating VK_RCONTROL from VK_LCONTROL and VK_RMENU from VK_LMENU, and both
// Windows keys live in the extended range. Right shift is deliberately not
// extended: its scancode already differs from left shift's.
func TestWindowsModifierKeysMarkExtendedKeys(t *testing.T) {
	t.Parallel()

	wantExtended := map[uint16]bool{
		vkLShift:   false,
		vkRShift:   false,
		vkLControl: false,
		vkRControl: true,
		vkLMenu:    false,
		vkRMenu:    true,
		vkLWin:     true,
		vkRWin:     true,
	}

	for _, key := range windowsModifierKeys {
		want, listed := wantExtended[key.virtualKey]
		if !listed {
			t.Errorf("virtual key %#x is not covered by this test", key.virtualKey)

			continue
		}

		if key.extended != want {
			t.Errorf("virtual key %#x extended = %v, want %v", key.virtualKey, key.extended, want)
		}
	}
}
