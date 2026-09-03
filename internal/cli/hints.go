package cli

import "github.com/y3owk1n/neru/internal/domain"

// HintsCmd is the CLI hints command.
var HintsCmd = BuildModeCommand(ModeConfig{
	Mode:  domain.ModeHints,
	Short: "Launch hints mode for clickable elements",
	Long: `Assign letter hints to on-screen elements for keyboard-driven clicking.

  Hints mode scans the focused window for interactive elements (buttons,
  links, inputs, etc.) and overlays short letter codes on each one.
  Type the code to click that element.

  Use --action to perform an action immediately upon selecting a hint,
  and --repeat to stay in hints mode after the action (useful for
  multi-step workflows).

  When --search is enabled, the mode shows a search/filter input
  instead of navigating by hint keys directly.

  Use --hide-on-empty-search with --search to hide all hints when the
  search query is empty. Hints appear only as you type a query, making
  it easier to focus on matching results.

  Use --role and --text to filter which elements get hinted:
    --role button,link           Only hint buttons and links
    --text "Submit,Cancel"        Only hint elements containing "Submit" or "Cancel"

  Use --strategy vision to detect elements by recognizing what is on screen
  instead of walking the accessibility tree - the Vision framework on macOS,
  tesseract on Linux, Windows.Media.Ocr on Windows. Off macOS it finds text
  only, and both OCR platforms need language data installed.

  Use --strategy hybrid to do both and merge them, keeping every element the
  accessibility tree found and adding the recognized text it missed. Reach for
  it when a tree is partly usable; it costs the tree walk plus the recognition.

  Use --strategy contour when a window exposes nothing to walk and nothing to
  read - an RDP or Citrix client, a canvas app, custom-drawn UI. It finds shapes
  by edge detection over the capture, so its hints have no text and --search
  cannot match them, and each one reports as a button because pixels do not say
  what a rectangle is. It reads the focused window and asks the accessibility
  tree for nothing, so the menubar and dock are not hinted while it is active.

  Use --split-word to split detected text into word-level regions (requires
  the vision or hybrid strategy).

  Use --debug to probe the focused window and print the clickable elements
  that would be hinted (count plus a sample) without showing the overlay.
  This is handy for verifying the platform accessibility pipeline.

  Use --label-direction to override the configured hint label enumeration
  for this activation. "normal" (default) uses the prefix-avoidance
  algorithm and prefers shorter labels; "reverse" spreads labels across
  the alphabet so same-prefix labels never cluster.

  Examples:
    neru hints                               Activate hints mode
    neru hints --action left_click           Select a hint to click once
    neru hints --action left_click --repeat  Click multiple elements in sequence
    neru hints --search                      Start with search input shown
    neru hints --search --hide-on-empty-search  Start search with hints hidden until you type
    neru hints --role button                 Hint only buttons
    neru hints --strategy vision             Detect elements by screen recognition
    neru hints --strategy vision --split-word  Use vision strategy with word-level splitting
    neru hints --debug                       Print detected elements, no overlay (used on windows),
    neru hints --label-direction reverse     Use spread labels for this run`,

	SupportDebug: true,
})

func init() {
	RootCmd.AddCommand(HintsCmd)
}
