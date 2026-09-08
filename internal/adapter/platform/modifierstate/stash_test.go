package modifierstate_test

import (
	"sync"
	"testing"

	"github.com/y3owk1n/neru/internal/adapter/platform/modifierstate"
)

// button numbers are arbitrary; a stash only ever passes them back.
const (
	leftButton  = 1
	rightButton = 3
)

// stashScenario is the presses a scenario makes followed by the releases that
// answer them, because nothing a Put does is observable except through the Take
// that undoes it.
//
// why says what the behavior is for. It prints when the case breaks, so a
// failure reports the reason the expectation was written rather than only the
// keycodes that missed it.
type stashScenario struct {
	name     string
	why      string
	presses  []stashedPress
	forgets  []uint32
	releases []expectedRelease
}

// stashedPress is one Put: a button going down, and the plan that press took to
// present the modifiers it was asked for.
type stashedPress struct {
	button uint32
	plan   modifierstate.Plan
}

// expectedRelease is one Take, and what the stash owes it.
type expectedRelease struct {
	button       uint32
	wantHeld     bool
	wantSuppress []modifierstate.Edit
	wantPress    []modifierstate.Edit
}

// TestStash_Take_AnswersEachReleaseWithItsOwnPress covers what a release gets
// back: the plan its own press stashed, once, and nothing at all where no press
// stashed one.
func TestStash_Take_AnswersEachReleaseWithItsOwnPress(t *testing.T) {
	runStashScenarios(t, []stashScenario{
		{
			name: "reports the plan the press stashed",
			why: "This is what the stash exists for: a press and its release " +
				"are two separate calls, so the edits that undo the press have " +
				"to outlive the call that made them.",
			presses: []stashedPress{{
				button: leftButton,
				plan: modifierstate.Plan{
					Suppress: edits(controlLeft),
					Press:    edits(shiftLeft),
				},
			}},
			releases: []expectedRelease{{
				button:       leftButton,
				wantHeld:     true,
				wantSuppress: edits(controlLeft),
				wantPress:    edits(shiftLeft),
			}},
		},
		{
			name: "answers nothing for a release with no press",
			why: "A release this process never made the press for — a bare " +
				"mouse_up action, or a daemon restarted mid-drag — has nothing " +
				"to undo, and reporting so is how the caller knows to present " +
				"the release's own modifiers instead.",
			releases: []expectedRelease{{button: leftButton, wantHeld: false}},
		},
		{
			name: "forgets the plan it handed back",
			why: "A second release must not undo the same press twice: " +
				"pressing a suppressed key back a second time leaves a hold " +
				"nothing answers.",
			presses: []stashedPress{{
				button: leftButton,
				plan:   modifierstate.Plan{Suppress: edits(controlLeft)},
			}},
			releases: []expectedRelease{
				{
					button:       leftButton,
					wantHeld:     true,
					wantSuppress: edits(controlLeft),
				},
				{button: leftButton, wantHeld: false},
			},
		},
		{
			name: "keeps each button apart",
			why: "Two buttons can be held at once: the release of one must " +
				"not undo the hold the other is still relying on.",
			presses: []stashedPress{
				{
					button: leftButton,
					plan:   modifierstate.Plan{Suppress: edits(controlLeft)},
				},
				{
					button: rightButton,
					plan:   modifierstate.Plan{Suppress: edits(altLeft)},
				},
			},
			releases: []expectedRelease{
				{
					button:       rightButton,
					wantHeld:     true,
					wantSuppress: edits(altLeft),
				},
				{
					button:       leftButton,
					wantHeld:     true,
					wantSuppress: edits(controlLeft),
				},
			},
		},
	})
}

// TestStash_Put_MergesASecondPressOfAButtonAlreadyDown covers a button pressed
// again while it is still down, which leaves one release to undo both presses.
func TestStash_Put_MergesASecondPressOfAButtonAlreadyDown(t *testing.T) {
	runStashScenarios(t, []stashScenario{
		{
			name: "carries both presses forward for one button",
			why: "Dropping the first plan would strand both of its halves: " +
				"nothing would release the key it pressed, and nothing would " +
				"press back the key it suppressed — the second press cannot " +
				"see that key held, because the first one released it.",
			presses: []stashedPress{
				{
					button: leftButton,
					plan: modifierstate.Plan{
						Suppress: edits(controlLeft),
						Press:    edits(shiftLeft),
					},
				},
				{
					button: leftButton,
					plan: modifierstate.Plan{
						Suppress: edits(altLeft),
						Press:    edits(superLeft),
					},
				},
			},
			releases: []expectedRelease{{
				button:       leftButton,
				wantHeld:     true,
				wantSuppress: edits(controlLeft, altLeft),
				wantPress:    edits(shiftLeft, superLeft),
			}},
		},
		{
			name: "drops a key the earlier press already answers",
			why: "The crossing case: the second press reads a key the first " +
				"one pressed as held, so a naive union would both release it " +
				"and press it back. Whichever half of the pair the later plan " +
				"repeats, the earlier one already describes what the key was " +
				"before Neru touched it, and that is the state the release has " +
				"to land on.",
			// The first press pressed shift itself and released the ctrl the
			// user was holding; the second sees exactly the opposite of both.
			presses: []stashedPress{
				{
					button: leftButton,
					plan: modifierstate.Plan{
						Suppress: edits(controlLeft),
						Press:    edits(shiftLeft),
					},
				},
				{
					button: leftButton,
					plan: modifierstate.Plan{
						Suppress: edits(shiftLeft),
						Press:    edits(controlLeft),
					},
				},
			},
			// Only the ctrl the user is holding, and only the shift Neru
			// pressed: that is the state before Neru touched either key.
			releases: []expectedRelease{{
				button:       leftButton,
				wantHeld:     true,
				wantSuppress: edits(controlLeft),
				wantPress:    edits(shiftLeft),
			}},
		},
	})
}

// TestStash_ForgetKey_DropsWhatTheUserLetGoOf covers the release the drag can
// otherwise not see: the user letting go of a suppressed modifier while the
// button is still down.
func TestStash_ForgetKey_DropsWhatTheUserLetGoOf(t *testing.T) {
	runStashScenarios(t, []stashScenario{
		{
			name: "leaves nothing to press back for a key the user released",
			why: "A keyboard-driven drag lets go of the chord's modifier before " +
				"typing the keys that steer it. Pressing that key back when the " +
				"button goes up leaves it held with nothing to release it, so " +
				"every key afterwards arrives modified.",
			presses: []stashedPress{{
				button: leftButton,
				plan:   modifierstate.Plan{Suppress: edits(shiftLeft)},
			}},
			forgets:  []uint32{shiftLeft},
			releases: []expectedRelease{{button: leftButton, wantHeld: true}},
		},
		{
			name: "keeps the keys the user is still holding",
			why: "Only the released key loses its restore. A modifier held " +
				"through the whole drag still has to be pressed back, or the " +
				"drag drops it out of everything the user does next.",
			presses: []stashedPress{{
				button: leftButton,
				plan:   modifierstate.Plan{Suppress: edits(shiftLeft, controlLeft)},
			}},
			forgets: []uint32{shiftLeft},
			releases: []expectedRelease{{
				button:       leftButton,
				wantHeld:     true,
				wantSuppress: edits(controlLeft),
			}},
		},
		{
			name: "leaves what the hold pressed itself alone",
			why: "A key Neru pressed to present a requested modifier is " +
				"released unconditionally at the end, and the user releasing " +
				"the same key does not change where it has to land.",
			presses: []stashedPress{{
				button: leftButton,
				plan:   modifierstate.Plan{Press: edits(shiftLeft)},
			}},
			forgets: []uint32{shiftLeft},
			releases: []expectedRelease{{
				button:    leftButton,
				wantHeld:  true,
				wantPress: edits(shiftLeft),
			}},
		},
		{
			name: "ignores a key no drag suppressed",
			why: "The hook reports every modifier release, most of them during " +
				"no drag at all, so an unrelated key must not disturb a plan.",
			presses: []stashedPress{{
				button: leftButton,
				plan:   modifierstate.Plan{Suppress: edits(controlLeft)},
			}},
			forgets: []uint32{altLeft},
			releases: []expectedRelease{{
				button:       leftButton,
				wantHeld:     true,
				wantSuppress: edits(controlLeft),
			}},
		},
		{
			name: "reaches every button still down",
			why: "Two buttons can be down at once and the user has one set of " +
				"modifier keys: a release belongs to both drags.",
			presses: []stashedPress{
				{
					button: leftButton,
					plan:   modifierstate.Plan{Suppress: edits(shiftLeft)},
				},
				{
					button: rightButton,
					plan:   modifierstate.Plan{Suppress: edits(shiftLeft, altLeft)},
				},
			},
			forgets: []uint32{shiftLeft},
			releases: []expectedRelease{
				{button: leftButton, wantHeld: true},
				{
					button:       rightButton,
					wantHeld:     true,
					wantSuppress: edits(altLeft),
				},
			},
		},
	})
}

// TestStash_ForgetKey_LeavesTheMergedPlanItSharesWith pins that forgetting edits
// no plan but the stashed one. A merged plan appends onto the slice the first
// press handed over, so a forget that wrote in place would reach back into the
// caller's own plan value.
func TestStash_ForgetKey_LeavesTheMergedPlanItSharesWith(t *testing.T) {
	var stash modifierstate.Stash

	first := modifierstate.Plan{Suppress: edits(shiftLeft, controlLeft)}

	stash.Put(leftButton, first)
	stash.Put(rightButton, modifierstate.Plan{Suppress: edits(shiftLeft)})
	stash.ForgetKey(shiftLeft)

	if !equalEdits(first.Suppress, edits(shiftLeft, controlLeft)) {
		t.Fatalf("ForgetKey rewrote the caller's plan to %v", first.Suppress)
	}
}

// TestStash_Put_SerializesConcurrentPresses pins the lock. Presses and releases
// arrive on whichever goroutine ran the action, so the map behind the stash is
// reachable from more than one at a time.
//
// This one stays off the scenario table: what it asserts is that concurrent
// callers do not lose each other's plans, which no ordered list of presses and
// releases can describe.
func TestStash_Put_SerializesConcurrentPresses(t *testing.T) {
	var (
		stash modifierstate.Stash
		wait  sync.WaitGroup
	)

	const buttons = 8

	// One slot per goroutine, so the answers say which button lost its plan.
	restored := make([]bool, buttons)

	for button := range uint32(buttons) {
		wait.Go(func() {
			stash.Put(button, modifierstate.Plan{Suppress: edits(controlLeft)})

			plan, held := stash.Take(button)
			restored[button] = held && equalEdits(plan.Suppress, edits(controlLeft))
		})
	}

	wait.Wait()

	for button, ok := range restored {
		if !ok {
			t.Fatalf("button %d did not get its own plan back", button)
		}
	}
}

// runStashScenarios plays each scenario against a stash of its own: every press
// in order, then every release in order, checking what each one is answered
// with.
func runStashScenarios(t *testing.T, scenarios []stashScenario) {
	t.Helper()

	for _, testCase := range scenarios {
		t.Run(testCase.name, func(t *testing.T) {
			// The rationale is only worth reading when the case breaks, and
			// that is exactly when a t.Log reaches whoever broke it.
			t.Cleanup(func() {
				if t.Failed() {
					t.Log(testCase.why)
				}
			})

			var stash modifierstate.Stash

			for _, press := range testCase.presses {
				stash.Put(press.button, press.plan)
			}

			for _, keycode := range testCase.forgets {
				stash.ForgetKey(keycode)
			}

			for step, release := range testCase.releases {
				plan, held := stash.Take(release.button)
				if held != release.wantHeld {
					t.Fatalf(
						"Take #%d on button %d reported plan %v, held=%t, want held=%t",
						step+1, release.button, plan, held, release.wantHeld,
					)
				}

				if !held {
					continue
				}

				if !equalEdits(plan.Suppress, release.wantSuppress) {
					t.Fatalf(
						"Take #%d on button %d returned suppress %v, want %v",
						step+1, release.button, plan.Suppress, release.wantSuppress,
					)
				}

				if !equalEdits(plan.Press, release.wantPress) {
					t.Fatalf(
						"Take #%d on button %d returned press %v, want %v",
						step+1, release.button, plan.Press, release.wantPress,
					)
				}
			}
		})
	}
}
