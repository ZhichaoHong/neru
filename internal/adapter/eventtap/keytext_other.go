//go:build !windows

package eventtap

// textForKey reports false everywhere but Windows, so the caller falls back to
// reading the key name.
//
// macOS needs no answer here - its hint search runs on a native text field that
// receives characters already composed, layout and input method included. The
// Linux backends do need one and have none yet: X11 would resolve it through
// XkbLookupKeySym and evdev through xkbcommon, neither of which the taps carry.
// See Adapter.TextForKey.
func textForKey(_ string) (string, bool) { return "", false }
