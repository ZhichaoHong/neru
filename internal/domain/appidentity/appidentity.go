package appidentity

import "strings"

// pathSeparators are both separators a user might type into a configured
// identifier. The runtime identifier Windows reports always uses a backslash,
// but a hand-written config may use either. path/filepath is unusable here:
// this package is platform-neutral and has to split a Windows path on a Linux
// build too.
const pathSeparators = `/\`

// Matches reports whether configured, a value written in the user's config,
// selects the application that the platform identifies at runtime as identity.
// Both sides are trimmed and lowercased before comparison.
//
// A configured value containing no path separator matches the *basename* of the
// runtime identity. That is what lets a Windows user write "chrome.exe" instead
// of the full executable path Windows reports as an app identity, which is tied
// to one install location and changes when the app moves or updates. A
// configured value that does contain a separator is compared whole, so absolute
// paths already sitting in user configs keep matching exactly what they matched
// before.
//
// The basename branch needs no platform check to stay out of the way: macOS
// bundle IDs are reverse-DNS and Linux WM_CLASS/app_id values are single tokens,
// so neither can contain a path separator, and taking the basename of a string
// that has none returns the string.
//
// An empty configured value matches only an empty identity, so a config entry
// that omits the identifier never selects a running app.
func Matches(configured, identity string) bool {
	configured = strings.ToLower(strings.TrimSpace(configured))
	identity = strings.ToLower(strings.TrimSpace(identity))

	if strings.ContainsAny(configured, pathSeparators) {
		return configured == identity
	}

	if idx := strings.LastIndexAny(identity, pathSeparators); idx >= 0 {
		identity = identity[idx+1:]
	}

	return configured == identity
}

// MatchesAny reports whether any configured value selects identity, following
// [Matches]. Callers hold a handful of entries at most, so the linear scan is
// cheaper than the normalized map it replaced - and unlike a map it can apply
// the basename rule at all.
func MatchesAny(configured []string, identity string) bool {
	for _, candidate := range configured {
		if Matches(candidate, identity) {
			return true
		}
	}

	return false
}
