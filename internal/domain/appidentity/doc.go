// Package appidentity owns the rule for deciding whether a configured
// application identifier selects the application the platform reports at
// runtime.
//
// The rule lives here, in one place, because the comparison happens at two
// layers that cannot import each other: the configuration lookups in
// internal/config (`bundle_id` under every `[[<mode>.app_configs]]`) and the
// accessibility adapter, which owns the `general.excluded_apps` check on the
// hot path. Two copies of this rule means a bare executable name that selects a
// per-app override silently fails to exclude the same app.
//
// The identifier a platform reports differs per platform - a reverse-DNS bundle
// ID on macOS, `WM_CLASS` or `app_id` on Linux, the full path to the executable
// on Windows - but the matching rule is deliberately platform-neutral. See
// [Matches].
package appidentity
