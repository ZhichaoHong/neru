package config_test

import (
	"testing"

	"github.com/y3owk1n/neru/internal/config"
)

// The identity rule has to reach every place a configured `bundle_id` is
// compared. When one site misses it, a bare executable name selects a per-app
// override and silently fails to exclude the same app, or drives hints but not
// grid.
func TestBareExeNameMatchesAtEveryLookupSite(t *testing.T) {
	t.Parallel()

	const identity = `C:\Program Files\Google\Chrome\Application\chrome.exe`

	entry := []config.AppConfig{{BundleID: "chrome.exe", Strategy: "vision"}}

	hints := &config.HintsConfig{AppConfigs: entry}
	if hints.AppConfigForBundleID(identity) == nil {
		t.Error("HintsConfig.AppConfigForBundleID did not match a bare exe name")
	}

	grid := &config.GridConfig{AppConfigs: entry}
	if grid.AppConfigForBundleID(identity) == nil {
		t.Error("GridConfig.AppConfigForBundleID did not match a bare exe name")
	}

	recursiveGrid := &config.RecursiveGridConfig{AppConfigs: entry}
	if recursiveGrid.AppConfigForBundleID(identity) == nil {
		t.Error("RecursiveGridConfig.AppConfigForBundleID did not match a bare exe name")
	}

	scroll := &config.ScrollConfig{AppConfigs: entry}
	if scroll.AppConfigForBundleID(identity) == nil {
		t.Error("ScrollConfig.AppConfigForBundleID did not match a bare exe name")
	}

	excluded := &config.Config{
		General: config.GeneralConfig{ExcludedApps: []string{"chrome.exe"}},
	}
	if !excluded.IsAppExcluded(identity) {
		t.Error("Config.IsAppExcluded did not match a bare exe name")
	}

	if excluded.IsAppExcluded(`C:\Windows\System32\notepad.exe`) {
		t.Error("Config.IsAppExcluded matched an app that is not excluded")
	}
}

// GlobalHotkeysForApp resolves root-level [[app_configs]] with its own inline
// comparison, so it needs its own guard.
func TestGlobalHotkeysForAppMatchesBareExeName(t *testing.T) {
	t.Parallel()

	const identity = `C:\Program Files\Google\Chrome\Application\chrome.exe`

	cfg := &config.Config{
		Hotkeys: config.HotkeysConfig{
			Bindings: map[string][]string{"Primary+Shift+Space": {"hints"}},
		},
		AppConfigs: []config.AppConfig{{
			BundleID: "chrome.exe",
			Hotkeys:  map[string]config.StringOrStringArray{"Primary+Shift+G": {"grid"}},
		}},
	}

	merged := cfg.GlobalHotkeysForApp(identity)
	if _, ok := merged["Primary+Shift+G"]; !ok {
		t.Errorf(
			"GlobalHotkeysForApp did not merge the override for a bare exe name, got %v",
			merged,
		)
	}

	unmatched := cfg.GlobalHotkeysForApp(`C:\Windows\System32\notepad.exe`)
	if _, ok := unmatched["Primary+Shift+G"]; ok {
		t.Error("GlobalHotkeysForApp merged an override for a non-matching app")
	}
}
