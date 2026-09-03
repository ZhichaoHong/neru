// Package contour is a contour-based target detector. The algorithm is a
// faithful port of the one in wl-kbptr (https://github.com/moverest/wl-kbptr,
// MIT) and its heuristics are theirs: grayscale, Gaussian blur, Sobel, Canny
// hysteresis, dilation and connected-component labeling over an RGBA frame,
// returning the bounding boxes of things that look like buttons, icons and
// text. It is pure Go with no cgo, so it builds everywhere Neru does,
// CGO_ENABLED=0 included; each vision adapter supplies the frame from its own
// capture backend and maps the boxes back to global coordinates.
//
// The frame is screen content. Detect reads it and returns rectangles; nothing
// here logs, copies or retains the pixels.
//
// # Provenance
//
// The Go translation is adopted, not written here. It comes from upstream neru
// at commit cf1a5dfe: detect.go and its test are byte-identical to that commit
// so a future diff against upstream stays readable, and findings from reviewing
// the detector are recorded below rather than as comments inside it, for the
// same reason. The fork's own addition is MergeRuns in runmerge.go, which
// upstream has no equivalent of.
//
// elements.go is upstream's with one deviation, which is documented at Elements
// itself: the element role is the caller's rather than a hardcoded AX name,
// because the hint filter compares roles in the running platform's vocabulary
// and upstream's "AXButton" matches nothing off macOS.
//
// # Behaviour worth knowing before trusting the output
//
// Every size threshold in detect.go is a logical-pixel number, compared after
// filterTargets divides component boxes by scale. So the thresholds themselves
// are display-independent given a correct scale, and the dilation kernel is the
// only place scale changes the pixel work. Pass the scale of the monitor the
// captured window sits on. Inside an RDP or Citrix window that value is wrong
// and cannot be made right, because those pixels carry the remote session's DPI
// while the frame is measured in the local monitor's; a remote session at a
// lower DPI loses some of its smallest targets under the size floor.
//
// Low-contrast fills lose their outline. A filled button whose luma differs
// from its background by roughly 40 produces a Sobel magnitude near 150, which
// never reaches cannyHigh, so hysteresis drops the button edge and only the
// label inside survives. The returned rectangle is then the label, smaller than
// the control but still inside it. A low-contrast control with neither text nor
// an icon yields nothing at all.
//
// Detect finds text as readily as controls and cannot tell a link from a noun,
// which is why MergeRuns exists and why contour output is a poor substitute for
// an accessibility tree that works.
package contour
