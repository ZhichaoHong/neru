//go:build windows

package eventtap

import "github.com/y3owk1n/neru/internal/adapter/platform/windows"

// textForKey answers from the active keyboard layout, through the same
// translation the hook names keys with. See Adapter.TextForKey.
func textForKey(key string) (string, bool) {
	return windows.TextForKeyCombo(key)
}
