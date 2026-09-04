// Package appwatcher monitors application lifecycle events (launch, terminate,
// activate, deactivate, screen change) and dispatches them to registered callbacks.
//
// The platform-specific event source is abstracted behind build-tagged dispatch
// files, so this package compiles on all platforms. On macOS the events come
// from the Objective-C NSWorkspace observer; on Linux from the compositor's or
// X11's focus-change signal, polled where the session exposes none; on Windows
// from a SetWinEventHook(EVENT_SYSTEM_FOREGROUND) plus a periodic re-sample.
// platform_other.go is the remaining no-op slot.
//
// Only macOS reports launch, termination and Mission Control. The other two
// backends report activation, deactivation and screen changes, which is what the
// per-app tables and the keymap need.
package appwatcher
