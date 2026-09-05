package ports

func supportedCapability(detail string) FeatureCapability {
	return FeatureCapability{
		Status: FeatureStatusSupported,
		Detail: detail,
	}
}

func stubCapability(detail string) FeatureCapability {
	return FeatureCapability{
		Status: FeatureStatusStub,
		Detail: detail,
	}
}

// DarwinCapabilities returns the supported macOS runtime capabilities.
func DarwinCapabilities() PlatformCapabilities {
	return PlatformCapabilities{
		Platform: "darwin",
		Process: supportedCapability(
			"focused app inspection available via Cocoa workspace APIs",
		),
		Screen: supportedCapability(
			"screen bounds and display enumeration available via Cocoa",
		),
		Cursor: supportedCapability(
			"cursor movement and tracking available via Quartz events",
		),
		Accessibility: supportedCapability(
			"macOS accessibility integration available via AXUIElement",
		),
		Overlay: supportedCapability("native overlays available via Cocoa windows"),
		Notifications: supportedCapability(
			"native alerts and notifications available via NSAlert/UserNotifications",
		),
		GlobalHotkeys: supportedCapability("global hotkeys available via per-key CGEventTaps"),
		KeyboardEventTap: supportedCapability(
			"keyboard event tap available via Quartz event taps",
		),
		AppWatcher: supportedCapability("focused-app watcher available via NSWorkspace"),
		DarkModeDetection: supportedCapability(
			"system dark mode detection available via Cocoa appearance APIs",
		),
		TextInput: supportedCapability(
			"native hint-search field available via an NSTextField overlay",
		),
		Vision: supportedCapability(
			"OCR element detection available via the Vision framework and ScreenCaptureKit",
		),
		KeyFeed: supportedCapability("key injection available via CGEventPost"),
		Systray: supportedCapability("tray icon available via NSStatusItem"),
	}
}

// LinuxCapabilities returns the supported Linux runtime capabilities.
func LinuxCapabilities() PlatformCapabilities {
	return PlatformCapabilities{
		Platform: "linux",
		Process: supportedCapability(
			"focused app inspection available via X11 _NET_WM_PID and Wayland " +
				"wlr-foreign-toplevel app_id (wlroots/KDE; PID best-effort via /proc)",
		),
		Screen: supportedCapability(
			"screen enumeration available via XRandR and Wayland xdg-output",
		),
		Cursor: supportedCapability(
			"cursor movement/tracking available via XTest and Wayland virtual-pointer",
		),
		Accessibility: supportedCapability(
			"clickable-element discovery via AT-SPI (D-Bus) tree walk; " +
				"click/scroll injection via XTest (X11) or virtual-pointer/libei " +
				"(Wayland wlroots/KDE). Coverage depends on the app's AT-SPI " +
				"support; hints.strategy = vision is the OCR fallback where it is thin",
		),
		Overlay: supportedCapability(
			"native overlays available via X11 windows or Wayland layer-shell + Cairo",
		),
		// Live-probed by the Linux SystemAdapter, which downgrades this to a
		// stub when nothing owns the notification name on the session bus.
		// See linux.SystemAdapter.notificationCapability.
		Notifications: supportedCapability(
			"notifications and alerts delivered to the session's freedesktop " +
				"notification daemon over D-Bus (org.freedesktop.Notifications); " +
				"an alert is a critical-urgency notification that stays until " +
				"dismissed, not a modal dialog",
		),
		GlobalHotkeys: supportedCapability(
			"global hotkeys available via X11 XGrabKey; on Wayland via a passive " +
				"evdev listener on /dev/input that honors neru's own keybindings " +
				"(needs input-group access and a cgo build), falling back to " +
				"compositor keybindings otherwise",
		),
		KeyboardEventTap: supportedCapability(
			"keyboard event tap available via X11 grab and Wayland evdev/layer-shell " +
				"keyboard interactivity; modifier passthrough of unbound shortcuts is " +
				"supported on the Wayland evdev backend only (X11's exclusive grab and " +
				"the wl-keyboard fallback cannot re-inject selectively)",
		),
		AppWatcher: supportedCapability(
			"focused-app change detection keyed on the WM_CLASS (X11) or " +
				"wlr-foreign-toplevel app_id (Wayland wlroots/KDE), event-driven " +
				"where the compositor/X11 exposes a focus-change signal and polling " +
				"otherwise; GNOME/Mutter exposes no focused-app source",
		),
		// Default placeholder; the Linux SystemAdapter overrides this with
		// the live-probed state (current color-scheme + source) on each
		// Capabilities() call. See linux.SystemAdapter.Capabilities.
		DarkModeDetection: supportedCapability(
			"dark mode detection via freedesktop appearance portal (Settings.Read), with kdeglobals fallback",
		),
		// What is a stub here is the *field*: no Linux text control owns
		// keyboard focus for hint search, so the characters come from the event
		// tap and an input method never sees them. The query is on screen —
		// the overlay draws the badge — which is why the detail says so rather
		// than leaving "not implemented" to imply nothing appears.
		TextInput: stubCapability(
			"no native hint-search field: the query is read from the event tap's " +
				"key stream, so dead keys and IME composition do not reach it; " +
				"the overlay draws the search badge",
		),
		// Both halves are implemented on every backend: capture through
		// wlr-screencopy, XGetImage or the portal's ScreenCast session,
		// recognition through tesseract. The detail names the three things that
		// can still be missing on a given machine — KDE's screen-sharing consent
		// has to be approved once, the tesseract language data is a separate
		// distribution package from the library Neru links, and the CGO-off
		// build has no engine — because this preset is static and `neru doctor`
		// reports it without probing. VisionPort.Health answers the same
		// question for the live session, and names which one it is.
		Vision: supportedCapability(
			"vision element detection via tesseract OCR over a screen capture " +
				"(wlr-screencopy on wlroots, XGetImage on X11, the xdg-desktop-portal " +
				"ScreenCast session on KDE, which asks for screen-sharing consent " +
				"once); text only, with no rectangle detection; needs a cgo build " +
				"and the tesseract eng language data installed",
		),
		KeyFeed: supportedCapability(
			"key injection via a uinput virtual keyboard when /dev/uinput is " +
				"writable (works on X11, wlroots, and KWin), falling back to " +
				"zwp_virtual_keyboard_v1 on wlroots compositors",
		),
		Systray: supportedCapability(
			"tray icon available via the D-Bus StatusNotifierItem + dbusmenu protocols",
		),
	}
}

// WindowsCapabilities returns the current Windows runtime capabilities.
func WindowsCapabilities() PlatformCapabilities {
	return PlatformCapabilities{
		Platform: "windows",
		Process: supportedCapability(
			"focused app inspection available via Win32 foreground-window APIs",
		),
		Screen: supportedCapability(
			"screen bounds and display enumeration available via Win32 monitor APIs",
		),
		Cursor: supportedCapability(
			"cursor movement and tracking available via SetCursorPos/GetCursorPos",
		),
		Accessibility: supportedCapability(
			"clickable-element discovery available via UI Automation (initial coverage)",
		),
		Overlay: supportedCapability(
			"native overlays available via layered Win32 window + GDI",
		),
		Notifications: supportedCapability(
			"notifications shown as balloon tips on the tray icon (Shell_NotifyIcon " +
				"NIF_INFO), rendered as toasts on Windows 10 and 11; they need " +
				"systray.enabled, and alerts use MessageBoxW",
		),
		GlobalHotkeys: supportedCapability(
			"global hotkeys available via RegisterHotKey",
		),
		KeyboardEventTap: supportedCapability(
			"keyboard event tap available via WH_KEYBOARD_LL hook; unbound modifier " +
				"shortcuts pass through to the focused application when asked to",
		),
		AppWatcher: supportedCapability(
			"focused-app change detection keyed on the executable path, event-driven " +
				"via a SetWinEventHook(EVENT_SYSTEM_FOREGROUND) with a periodic " +
				"re-sample so a coalesced or missed event self-heals; deactivate is " +
				"synthesized from the previous foreground, display changes arrive as " +
				"WM_DISPLAYCHANGE, and app launch, termination and Mission Control " +
				"never fire",
		),
		DarkModeDetection: supportedCapability(
			"dark mode detection available via the Windows personalization registry " +
				"(Themes\\Personalize AppsUseLightTheme)",
		),
		// Still a stub, because the *field* is what this row is about and Windows
		// has none. What it does have is layout translation on the fallback path:
		// the hook resolves what each keystroke types through the active layout,
		// so a shifted character reaches the query even where the key name cannot
		// name it. A dead key and an input method still do not, which is the part
		// only a real field would buy.
		TextInput: stubCapability(
			"no native hint-search field: the query is read from the event tap's " +
				"key stream, translated through the active keyboard layout, so " +
				"shifted characters reach it but dead keys and IME composition " +
				"do not",
		),
		// Static, deliberately, exactly as the Linux entry above is: whether this
		// machine has OCR language data installed is a runtime fact, and probing
		// it here would cost a WinRT activation on every doctor and info call to
		// duplicate what VisionPort.Health answers better. The detail names what
		// can be missing per machine.
		Vision: supportedCapability(
			"vision element detection via Windows.Media.Ocr over a GDI screen " +
				"capture; text only, with no rectangle detection and no confidence " +
				"score; needs OCR language data for at least one language installed",
		),
		KeyFeed: supportedCapability("key injection available via SendInput"),
		Systray: supportedCapability(
			"tray icon available via the Win32 notification area (Shell_NotifyIcon)",
		),
	}
}
