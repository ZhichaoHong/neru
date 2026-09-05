//go:build windows

package windows

import (
	"errors"
	"fmt"

	"github.com/y3owk1n/neru/internal/adapter/platform/modifierstate"
	"github.com/y3owk1n/neru/internal/domain/action"
)

// windowsModifierKeys is every key Windows reads as a modifier, canonical key
// first for each one.
//
// Both sides of the keyboard are here because either of them presents the
// modifier, and a key event addressed to VK_SHIFT lands on the left key — so
// releasing that while the user holds the right one suppresses nothing.
//
// extended marks the keys Windows only resolves to the right scancode with
// KEYEVENTF_EXTENDEDKEY. VK_RCONTROL and VK_RMENU share a scancode with their
// left counterparts and are told apart by nothing else, and both Windows keys
// live in the extended range.
var windowsModifierKeys = []struct {
	virtualKey uint16
	modifier   action.Modifiers
	canonical  bool
	extended   bool
}{
	{virtualKey: vkLShift, modifier: action.ModShift, canonical: true},
	{virtualKey: vkRShift, modifier: action.ModShift},
	{virtualKey: vkLControl, modifier: action.ModCtrl, canonical: true},
	{virtualKey: vkRControl, modifier: action.ModCtrl, extended: true},
	{virtualKey: vkLMenu, modifier: action.ModAlt, canonical: true},
	{virtualKey: vkRMenu, modifier: action.ModAlt, extended: true},
	{virtualKey: vkLWin, modifier: action.ModCmd, canonical: true, extended: true},
	{virtualKey: vkRWin, modifier: action.ModCmd, extended: true},
}

// modifierHold is one injection's worth of falsified modifier state: the keys it
// released so the injection would not carry them, and the keys it pressed so the
// injection would.
type modifierHold struct {
	plan modifierstate.Plan
}

// holdModifiers makes the keyboard present exactly modifiers, and returns the
// hold that undoes it.
//
// A SendInput mouse event has no modifier field the way a CGEvent does: Windows
// reads the live key state when the event is dispatched and stamps that onto
// every message the target window gets. So an unmodified left_click bound to
// Shift+L arrives as a shift+click, which selects a range in a list and extends
// a selection in a text field, and a Ctrl+J scroll_down arrives as ctrl+scroll,
// which most applications read as zoom. The only way to present a set here is to
// make the keyboard actually hold it.
//
// The caller must release the hold on every path, including failure: a modifier
// left released while the user is still holding it drops that modifier out of
// everything they do next.
//
// Injection errors are dropped rather than reported. A modifier key Windows
// refuses is one this hold cannot present, and there is nothing better to try —
// reporting it would mask the outcome of the click or scroll it wraps, which is
// the part the user asked for.
func holdModifiers(modifiers action.Modifiers) modifierHold {
	hold := modifierHold{plan: modifierstate.PlanFor(modifierKeyState(), modifiers)}

	for _, edit := range hold.plan.Suppress {
		injectModifier(edit, false)
	}

	for _, edit := range hold.plan.Press {
		injectModifier(edit, true)
	}

	return hold
}

// release lets go of what the hold pressed and presses back what it suppressed,
// against the keyboard as it reads now rather than as it read when the hold was
// taken — the user may have let go of a key or taken hold of another while the
// injection was in flight.
//
// What it pressed goes out unconditionally, where what it suppressed is checked
// first. The asymmetry is not a preference: a key this hold pressed reads down
// whether or not the user has since taken hold of it too, so there is nothing to
// check it against.
func (h modifierHold) release() {
	for _, edit := range h.plan.Press {
		injectModifier(edit, false)
	}

	if len(h.plan.Suppress) == 0 {
		return
	}

	for _, edit := range h.plan.Restore(heldModifierVirtualKeys()) {
		injectModifier(edit, true)
	}
}

// dragModifiers is the hold a mouse-down took, kept until the mouse-up that
// ends the drag comes to undo it.
//
// It is package state because the two calls share nothing else, and because a
// press has to keep presenting its modifiers for as long as the button is down:
// the drag in between is what carries them, and releasing them when the press
// returns would make a shift+drag an unmodified one from the first pixel of
// movement.
//
// That would stretch the tradeoff the hold already carries — a suppressed key
// the user lets go of mid-drag is pressed back at the end and reads as held
// until they press and release it once more — over a window as long as the drag,
// which for a keyboard-driven drag is every drag: the chord that presses the
// button has to be let go of before the keys that steer it can be typed. So the
// keyboard hook tells the stash about those releases as they happen
// (noteModifierReleased), and the end of the drag presses back only what the
// user still has hold of.
var dragModifiers modifierstate.Stash

// noteModifierReleased records that the user let go of a modifier key
// themselves, so the drag it was suppressed for does not press it back.
//
// Only the keyboard hook can see this. Once holdModifiers has released a
// suppressed key, the live keyboard reads it up whether the user is still
// holding it or not, so the release of the drag cannot tell the two apart and
// biases towards restoring. A key event says which it was.
//
// Non-modifier keys are dropped here rather than at the call site: the hook
// reports every key, and which ones present a modifier on Windows is this file's
// to know.
func noteModifierReleased(virtualKey uint32) {
	if !isModifierVirtualKey(virtualKey) {
		return
	}

	dragModifiers.ForgetKey(virtualKey)
}

// isModifierVirtualKey reports whether virtualKey is one of the keys Windows
// reads as a modifier.
func isModifierVirtualKey(virtualKey uint32) bool {
	for _, key := range windowsModifierKeys {
		if uint32(key.virtualKey) == virtualKey {
			return true
		}
	}

	return false
}

// keepForRelease keeps this hold for the release of button to pick up, instead
// of undoing it when the call that took it returns.
func (h modifierHold) keepForRelease(button action.MouseButton) {
	dragModifiers.Put(uint32(button), h.plan)
}

// resumeModifierHold picks up the hold the press of button kept, and takes a
// fresh hold of modifiers when there was no such press.
//
// The fallback is what a release with nothing behind it needs — a bare mouse-up
// action, or one whose press this process never made. It presents the modifiers
// the caller named for the length of the release event and undoes them
// afterwards, which is the plain non-drag shape.
//
// What the release replays is the press's plan rather than a fresh reading, so a
// modifier the user takes hold of or lets go of mid-drag is presented on the
// release as it was on the press. Reading again here would decide the drag's
// modifiers twice, which is the one thing a drag cannot have.
func resumeModifierHold(button action.MouseButton, modifiers action.Modifiers) modifierHold {
	plan, held := dragModifiers.Take(uint32(button))
	if held {
		return modifierHold{plan: plan}
	}

	return holdModifiers(modifiers)
}

// injectModifier presses or releases one of the plan's keys.
//
// The extended flag is recovered from the key table rather than carried in the
// plan, because it is a property of the key on this platform rather than of the
// decision to touch it.
func injectModifier(edit modifierstate.Edit, pressed bool) {
	virtualKey := uint16(edit.Keycode)

	var extended bool

	for _, key := range windowsModifierKeys {
		if key.virtualKey == virtualKey {
			extended = key.extended

			break
		}
	}

	_ = sendKeyboardInput(virtualKey, !pressed, extended)
}

var errUnknownModifier = errors.New("unknown modifier")

// PostModifierKey presses or releases the canonical key for one modifier, named
// in Neru's vocabulary (shift, ctrl, alt, cmd), as if the user had.
//
// It is what the event tap's PostModifierEvent injects with: a sticky modifier
// is a real key held on the user's behalf, and SendInput is the only way to hold
// one. The event carries neruInjectedTag like every other injection here, so the
// keyboard hook hands it on rather than reading it back as a toggle.
//
// The canonical key for each modifier is the left one, and its extended flag
// comes from the key table rather than a literal: VK_LWIN is in the extended
// range, and the other three are not.
func PostModifierKey(modifier string, isDown bool) error {
	var virtualKey uint16

	switch modifier {
	case modNameShift:
		virtualKey = vkLShift
	case modNameCtrl:
		virtualKey = vkLControl
	case modNameAlt:
		virtualKey = vkLMenu
	case modNameCmd:
		virtualKey = vkLWin
	default:
		return fmt.Errorf("%w: %q", errUnknownModifier, modifier)
	}

	var extended bool

	for _, key := range windowsModifierKeys {
		if key.virtualKey == virtualKey {
			extended = key.extended

			break
		}
	}

	return sendKeyboardInput(virtualKey, !isDown, extended)
}

// modifierKeyState reads the live keyboard and reports every modifier key on it.
func modifierKeyState() []modifierstate.Key {
	keys := make([]modifierstate.Key, 0, len(windowsModifierKeys))

	for _, key := range windowsModifierKeys {
		keys = append(keys, modifierstate.Key{
			Keycode:   uint32(key.virtualKey),
			Modifier:  key.modifier,
			Held:      isVirtualKeyDown(uint32(key.virtualKey)),
			Canonical: key.canonical,
		})
	}

	return keys
}

// heldModifierVirtualKeys reports which modifier keys the keyboard has down
// right now.
func heldModifierVirtualKeys() []uint32 {
	var held []uint32

	for _, key := range modifierKeyState() {
		if key.Held {
			held = append(held, key.Keycode)
		}
	}

	return held
}
