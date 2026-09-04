package appwatcher

import (
	"testing"

	"go.uber.org/zap"
)

// TestWatcherDispatchesMissionControlOnlyWhileDetectionIsArmed pins the contract
// ports.AppWatcherPort.SetMCDetection states: while detection is disabled neither
// Mission Control callback fires, so an event the platform delivers anyway is
// reported to nobody.
//
// Detection starts disabled, which is why the arming has to reach the watcher on
// every reload and not only at startup - nothing further down the chain will let
// the event through on its own. The gate had no test at all, so a reload that
// never armed it looked from here exactly like a desktop where Mission Control
// was never opened.
func TestWatcherDispatchesMissionControlOnlyWhileDetectionIsArmed(t *testing.T) {
	watcher := NewWatcher(zap.NewNop())

	var activated, deactivated int

	watcher.OnMissionControlActivated(func() { activated++ })
	watcher.OnMissionControlDeactivated(func() { deactivated++ })

	watcher.HandleMissionControlActivated()
	watcher.HandleMissionControlDeactivated()

	if activated != 0 || deactivated != 0 {
		t.Fatalf(
			"detection off dispatched %d activations and %d deactivations, want none of either",
			activated, deactivated,
		)
	}

	watcher.SetMCDetection(true)
	watcher.HandleMissionControlActivated()
	watcher.HandleMissionControlDeactivated()

	if activated != 1 || deactivated != 1 {
		t.Fatalf(
			"detection on dispatched %d activations and %d deactivations, want one of each",
			activated, deactivated,
		)
	}

	watcher.SetMCDetection(false)
	watcher.HandleMissionControlActivated()
	watcher.HandleMissionControlDeactivated()

	if activated != 1 || deactivated != 1 {
		t.Fatalf(
			"detection disarmed again dispatched %d activations and %d deactivations, want the earlier one of each",
			activated, deactivated,
		)
	}
}
