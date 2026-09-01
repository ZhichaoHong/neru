package appidentity_test

import (
	"testing"

	"github.com/y3owk1n/neru/internal/domain/appidentity"
)

const chromePath = `C:\Program Files\Google\Chrome\Application\chrome.exe`

func TestMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		identity   string
		want       bool
	}{
		{
			name:       "bare exe name matches full windows path",
			configured: "chrome.exe",
			identity:   chromePath,
			want:       true,
		},
		{
			name:       "bare exe name rejects a different exe",
			configured: "firefox.exe",
			identity:   chromePath,
			want:       false,
		},
		{
			name:       "full path still matches itself",
			configured: chromePath,
			identity:   chromePath,
			want:       true,
		},
		{
			name:       "full path rejects the same exe under a different root",
			configured: chromePath,
			identity:   `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			want:       false,
		},
		{
			name:       "bare exe name matches that same exe under any root",
			configured: "chrome.exe",
			identity:   `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			want:       true,
		},
		{
			name:       "bare exe name survives a versioned WindowsApps path",
			configured: "wt.exe",
			identity:   `C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.24.11911.0_x64__8wekyb3d8bbwe\wt.exe`,
			want:       true,
		},
		{
			name:       "forward slashes accepted in a configured path",
			configured: "C:/Program Files/Google/Chrome/Application/chrome.exe",
			identity:   "C:/Program Files/Google/Chrome/Application/chrome.exe",
			want:       true,
		},
		{
			name:       "case and surrounding whitespace normalized",
			configured: "  Chrome.EXE  ",
			identity:   `C:\Program Files\Google\Chrome\Application\CHROME.exe`,
			want:       true,
		},
		{
			name:       "macos bundle id unaffected",
			configured: "com.apple.Safari",
			identity:   "com.apple.safari",
			want:       true,
		},
		{
			name:       "macos bundle id still rejects a different app",
			configured: "com.apple.Safari",
			identity:   "com.google.Chrome",
			want:       false,
		},
		{
			name:       "linux wm_class unaffected",
			configured: "Google-chrome",
			identity:   "google-chrome",
			want:       true,
		},
		{
			name:       "empty configured value does not match a real identity",
			configured: "",
			identity:   chromePath,
			want:       false,
		},
		{
			name:       "a path prefix is not a partial match",
			configured: `C:\Program Files\Google`,
			identity:   chromePath,
			want:       false,
		},
		{
			name:       "a directory name does not match the exe inside it",
			configured: "application",
			identity:   chromePath,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := appidentity.Matches(tt.configured, tt.identity); got != tt.want {
				t.Errorf(
					"Matches(%q, %q) = %v, want %v",
					tt.configured, tt.identity, got, tt.want,
				)
			}
		})
	}
}

func TestMatchesAny(t *testing.T) {
	t.Parallel()

	configured := []string{"com.apple.Safari", "chrome.exe"}

	if !appidentity.MatchesAny(configured, chromePath) {
		t.Error("MatchesAny did not match a bare exe name against a full path")
	}

	if appidentity.MatchesAny(configured, `C:\Windows\System32\notepad.exe`) {
		t.Error("MatchesAny matched an identity that is not in the list")
	}

	if appidentity.MatchesAny(nil, chromePath) {
		t.Error("MatchesAny matched against an empty list")
	}
}
