//go:build windows

package config

import (
	"os"
	"path/filepath"
)

func applyPlatformDefaults(cfg *Config) {
	// Windows-specific exec shell defaults (absolute path required for validation)
	// %SystemRoot% is always set on Windows (e.g. C:\Windows).
	cfg.General.ExecShell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	cfg.General.ExecShellArgs = []string{"/c"}

	// Windows.Media.Ocr reports no per-word confidence, so every region the vision
	// strategy produces here scores 0. The shared default of 0.3 would compare 0
	// against 0.3 and refuse to classify a single Button, which measured out at
	// 82-92% of the regions on a normal desktop - text found and then made
	// unclickable. 0 turns the gate off, which is the honest reading of a source
	// with nothing to threshold.
	//
	// This is a default, not a clamp: a user who sets button_min_confidence above 0
	// on Windows gets what they asked for, and CROSS_PLATFORM.md documents that it
	// suppresses Button classification entirely.
	cfg.Hints.Vision.ButtonMinConfidence = 0
}
