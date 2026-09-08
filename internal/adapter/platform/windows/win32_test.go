//go:build windows && (amd64 || arm64)

package windows

import (
	"image"
	"strings"
	"testing"
)

func TestPackMonitorPoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		point image.Point
		want  uintptr
	}{
		{
			name:  "primary monitor coordinate",
			point: image.Pt(1280, 720),
			want:  uintptr(0x000002D000000500),
		},
		{
			name:  "left of primary monitor coordinate",
			point: image.Pt(-1080, 261),
			want:  uintptr(0x00000105FFFFFBC8),
		},
		{
			name:  "right monitor coordinate",
			point: image.Pt(2846, 261),
			want:  uintptr(0x0000010500000B1E),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := packMonitorPoint(testCase.point); got != testCase.want {
				t.Fatalf("packMonitorPoint(%v) = %#x, want %#x", testCase.point, got, testCase.want)
			}
		})
	}
}

const (
	genericMonitorName  = "Generic PnP Monitor"
	dellMonitorName     = "DELL U2720Q"
	firstDisplayDevice  = `\\.\DISPLAY1`
	secondDisplayDevice = `\\.\DISPLAY2`
)

// SystemPort keys a screen by its name, so two monitors reporting the same
// friendly name have to be told apart before either is looked up. An unbranded
// panel reports "Generic PnP Monitor", and a desk with two of them is the case
// that stacked both monitor_select panels on one monitor.
func TestDisambiguateMonitorNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		monitors []displayMonitor
		want     []string
	}{
		{
			name: "distinct names are left alone",
			monitors: []displayMonitor{
				{name: dellMonitorName, device: firstDisplayDevice},
				{name: "LG HDR 4K", device: secondDisplayDevice},
			},
			want: []string{"DELL U2720Q", "LG HDR 4K"},
		},
		{
			name: "a repeated name is suffixed with the GDI device name",
			monitors: []displayMonitor{
				{name: genericMonitorName, device: `\\.\DISPLAY5`},
				{name: genericMonitorName, device: `\\.\DISPLAY6`},
			},
			want: []string{
				"Generic PnP Monitor (DISPLAY5)",
				"Generic PnP Monitor (DISPLAY6)",
			},
		},
		{
			name: "matching is case-insensitive, the way the lookup is",
			monitors: []displayMonitor{
				{name: genericMonitorName, device: firstDisplayDevice},
				{name: "GENERIC PNP MONITOR", device: secondDisplayDevice},
			},
			want: []string{
				"Generic PnP Monitor (DISPLAY1)",
				"GENERIC PNP MONITOR (DISPLAY2)",
			},
		},
		{
			name: "a repeat among distinct names only suffixes the repeat",
			monitors: []displayMonitor{
				{name: dellMonitorName, device: firstDisplayDevice},
				{name: genericMonitorName, device: secondDisplayDevice},
				{name: genericMonitorName, device: `\\.\DISPLAY3`},
			},
			want: []string{
				"DELL U2720Q",
				"Generic PnP Monitor (DISPLAY2)",
				"Generic PnP Monitor (DISPLAY3)",
			},
		},
		{
			name: "an unreadable device name falls back to the enumeration index",
			monitors: []displayMonitor{
				{name: genericMonitorName, device: ""},
				{name: genericMonitorName, device: ""},
			},
			want: []string{
				"Generic PnP Monitor (#1)",
				"Generic PnP Monitor (#2)",
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			monitors := make([]displayMonitor, len(testCase.monitors))
			copy(monitors, testCase.monitors)

			disambiguateMonitorNames(monitors)

			seen := make(map[string]bool, len(monitors))

			for index, monitor := range monitors {
				if monitor.name != testCase.want[index] {
					t.Errorf("monitor %d is named %q, want %q",
						index, monitor.name, testCase.want[index])
				}

				folded := strings.ToLower(monitor.name)
				if seen[folded] {
					t.Errorf("monitor %d repeats the name %q; the by-name lookup "+
						"would answer with the first match for both", index, monitor.name)
				}

				seen[folded] = true
			}
		})
	}
}
