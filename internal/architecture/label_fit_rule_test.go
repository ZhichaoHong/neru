package architecture_test

import (
	"fmt"
	"image"
	"maps"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/y3owk1n/neru/internal/adapter/overlay/render/recursivegrid"
)

// The two sides of the recursive-grid label-fit rule.
const (
	labelFitNativeSource = "internal/adapter/platform/darwin/overlay_darwin.m"

	// labelFitNativeMethod is the Objective-C method the rule lives in, named in
	// failure messages so a reader lands on the copy rather than on this test.
	labelFitNativeMethod = "fittedGridLabelFontSizeFor:inCellRect:"

	// labelFitGoDeclaration names the Go side the same way.
	labelFitGoDeclaration = "recursivegrid.Style.LabelFontSizeIn"

	// labelFitTolerance is how far apart the two answers may land before this pin
	// calls it a disagreement.
	//
	// Both sides do the same divisions on IEEE doubles, so the honest expectation
	// is exact equality. The slack is there so a reassociation that cannot change
	// what a user sees, a factor moved across a multiplication, is not reported as
	// drift, while every substantive change to the rule remains a difference of
	// whole points.
	labelFitTolerance = 1e-9
)

// TestLabelFitRuleIsPinnedAcrossTheLanguageBoundary keeps the macOS overlay
// drawing recursive-grid labels at the same size as every other backend.
//
// How big a label is drawn in a cell that cannot hold it at the configured size
// is one question with one shared answer: recursivegrid.Style.LabelFontSizeIn,
// which the Cairo and GDI backends both call. macOS answers it in Objective-C,
// in fittedGridLabelFontSizeFor:inCellRect:, and cannot call Go. This is ADR
// 0007's deliberate exception to the one-implementation rule
// (docs/adr/0007-a-shared-derivation-has-one-implementation.md): where the
// second implementation is in another language, what the rule asks for is a test
// holding the copies together rather than a deletion.
//
// The copy is a rule and not a constant, so it is pinned by running it. The
// Objective-C source is read into something this test can evaluate, with its
// multipliers and its floor read out of the same file so a constant edited on
// its own is a disagreement and not a silent pass. Its answer is then compared
// against the shared Go one over cells that make each limit the binding one in
// turn. A disagreement is one configuration drawing a legible label on macOS and
// a clipped one everywhere else, or the reverse.
//
// The shared rule carries a monitor-scale term the copy has none of, and this
// pin asks it at scale 1. That is not a gap: AppKit measures the cell and draws
// the glyph in the same points, so there is nothing for the copy to convert.
// What the term does for the two backends that measure device pixels is covered
// where it is used, in the render packages' own tests.
func TestLabelFitRuleIsPinnedAcrossTheLanguageBoundary(t *testing.T) {
	t.Parallel()

	rule := readNativeLabelFitRule(t)
	constants := objcFloatConstants(t, labelFitNativeSource)

	for _, disagreement := range labelFitDisagreements(rule, constants) {
		t.Errorf(
			"%s: %s and %s disagree on %s\n\t%s draws it at: %g\n\t%s draws it at: %g\n\tthe rule read from %s is: %s",
			labelFitNativeSource,
			labelFitNativeMethod,
			labelFitGoDeclaration,
			disagreement.testCase.describe(),
			labelFitNativeMethod,
			disagreement.native,
			labelFitGoDeclaration,
			disagreement.shared,
			labelFitNativeSource,
			rule,
		)
	}
}

// TestLabelFitRulePinCatchesNativeDrift keeps the pin above from passing over a
// rule that has moved.
//
// A pin is only worth its line count if the cases it runs can tell the copies
// apart, and the cases are chosen by hand: drop the width limit and every cell
// that is short before it is narrow still agrees. So each way the Objective-C
// rule could plausibly drift is applied to the rule this pin actually read, and
// the mutant has to disagree with the shared implementation somewhere. Mutating
// the rule rather than the source text keeps this honest across a reformat of
// the .m file.
func TestLabelFitRulePinCatchesNativeDrift(t *testing.T) {
	t.Parallel()

	rule := readNativeLabelFitRule(t)
	constants := objcFloatConstants(t, labelFitNativeSource)

	drifted := []struct {
		name  string
		apply func(nativeLabelFitRule) nativeLabelFitRule
	}{
		{
			name:  "the width limit no longer measured",
			apply: func(rule nativeLabelFitRule) nativeLabelFitRule { return rule.withoutLimit(0) },
		},
		{
			name:  "the height limit no longer measured",
			apply: func(rule nativeLabelFitRule) nativeLabelFitRule { return rule.withoutLimit(1) },
		},
		{
			name: "a limit taken when it is larger, so the label grows past the cell",
			apply: func(rule nativeLabelFitRule) nativeLabelFitRule {
				return rule.withClampOperator(">")
			},
		},
		{
			name:  "the floor dropped, so a label shrinks to a smudge",
			apply: func(rule nativeLabelFitRule) nativeLabelFitRule { return rule.withFloor("0") },
		},
		{
			name: "the fit started from the floor rather than the configured size",
			apply: func(rule nativeLabelFitRule) nativeLabelFitRule {
				return rule.withBase("kGridLabelMinFontSize")
			},
		},
		{
			name: "the width limit no longer divided by the glyph count",
			apply: func(rule nativeLabelFitRule) nativeLabelFitRule {
				return rule.withLimitDivisors(0, []string{"kGridLabelWidthMultiplier"})
			},
		},
		{
			name: "the height limit no longer divided by the line-height multiplier",
			apply: func(rule nativeLabelFitRule) nativeLabelFitRule {
				return rule.withLimitDivisors(1, []string{"1"})
			},
		},
		{
			name: "the empty-label guard inverted, so every label is drawn unfitted",
			apply: func(rule nativeLabelFitRule) nativeLabelFitRule {
				return rule.withEmptyGuardOperator(">=")
			},
		},
	}

	for _, drift := range drifted {
		mutant := drift.apply(rule)

		if len(labelFitDisagreements(mutant, constants)) == 0 {
			t.Errorf(
				"no case tells %s apart from %s: %s would pass the pin\n\tthe drifted rule is: %s",
				drift.name, labelFitGoDeclaration, labelFitNativeSource, mutant,
			)
		}
	}
}

// TestLabelFitRulePinReportsARuleItCannotRead pins the other half of the
// guardrail: a native rule this pin cannot read must be reported, never skipped.
// A pin that reads nothing and passes is worse than no pin, because it reads as
// coverage.
//
// This is where the pin's one deliberate cost sits. It reads a single shape, and
// a rewrite that keeps the behavior, folding the two limits into one MIN
// expression say, fails here rather than being understood. Teaching it every
// equivalent spelling of the same arithmetic is more machinery than the copy is
// worth; failing loudly and naming the shape it expected leaves the next author a
// one-line change to this file, which the same author is already making to the
// .m one.
func TestLabelFitRulePinReportsARuleItCannotRead(t *testing.T) {
	t.Parallel()

	unreadable := []struct {
		name   string
		source string
	}{
		{
			name:   nativeRuleRenamedCase,
			source: "- (CGFloat)gridLabelSizeFor:(NSString *)label {\n\treturn 10.0;\n}\n",
		},
		{
			name: "the floor folded away, so nothing bounds how small a label gets",
			source: nativeLabelFitMethodSource(
				"\tCGFloat glyphs = (CGFloat)[label length];\n" +
					"\tif (glyphs <= 0.0) {\n\t\treturn self.gridFont.pointSize;\n\t}\n" +
					"\tCGFloat fitted = self.gridFont.pointSize;\n" +
					"\tCGFloat widthLimit = cellRect.size.width / (glyphs * kGridLabelWidthMultiplier);\n" +
					"\tif (widthLimit < fitted) {\n\t\tfitted = widthLimit;\n\t}\n" +
					"\tCGFloat heightLimit = cellRect.size.height / kGridLabelHeightMultiplier;\n" +
					"\tif (heightLimit < fitted) {\n\t\tfitted = heightLimit;\n\t}\n" +
					"\treturn fitted;",
			),
		},
		{
			name: "a limit measured against an operand this pin cannot value",
			source: nativeLabelFitMethodSource(
				"\tCGFloat glyphs = (CGFloat)[label length];\n" +
					"\tif (glyphs <= 0.0) {\n\t\treturn self.gridFont.pointSize;\n\t}\n" +
					"\tCGFloat fitted = self.gridFont.pointSize;\n" +
					"\tCGFloat widthLimit = self.gridSubKeyFont.pointSize / (glyphs * kGridLabelWidthMultiplier);\n" +
					"\tif (widthLimit < fitted) {\n\t\tfitted = widthLimit;\n\t}\n" +
					"\tCGFloat heightLimit = cellRect.size.height / kGridLabelHeightMultiplier;\n" +
					"\tif (heightLimit < fitted) {\n\t\tfitted = heightLimit;\n\t}\n" +
					"\treturn MAX(fitted, kGridLabelMinFontSize);",
			),
		},
		{
			name: "only one limit left, so a cell is measured on one axis",
			source: nativeLabelFitMethodSource(
				"\tCGFloat glyphs = (CGFloat)[label length];\n" +
					"\tif (glyphs <= 0.0) {\n\t\treturn self.gridFont.pointSize;\n\t}\n" +
					"\tCGFloat fitted = self.gridFont.pointSize;\n" +
					"\tCGFloat widthLimit = cellRect.size.width / (glyphs * kGridLabelWidthMultiplier);\n" +
					"\tif (widthLimit < fitted) {\n\t\tfitted = widthLimit;\n\t}\n" +
					"\treturn MAX(fitted, kGridLabelMinFontSize);",
			),
		},
	}

	for _, source := range unreadable {
		if _, problem := parseNativeLabelFitRule(source.source); problem == "" {
			t.Errorf(
				"parsing accepted a source with %s; the pin would then run a rule it never read",
				source.name,
			)
		}
	}
}

// labelFitInputs are the values the rule is a function of.
type labelFitInputs struct {
	fontSize   float64
	glyphs     float64
	cellWidth  float64
	cellHeight float64
}

// labelFitOperands binds every name the Objective-C rule is allowed to mention
// to the input it stands for. It is the pin's vocabulary, alongside the glyph
// count the rule names for itself and the constants read out of the same source:
// a rule reading anything else is one this test cannot evaluate, and parsing
// says so rather than guessing a value.
var labelFitOperands = map[string]func(labelFitInputs) float64{
	"self.gridFont.pointSize": func(inputs labelFitInputs) float64 { return inputs.fontSize },
	nativeRuleCellWidth:       func(inputs labelFitInputs) float64 { return inputs.cellWidth },
	nativeRuleCellHeight:      func(inputs labelFitInputs) float64 { return inputs.cellHeight },
}

// labelFitCase is one question put to both implementations of the rule.
//
// The cell is an integer rectangle and the font size a whole number of points,
// because those are the units the shared implementation is actually asked in.
type labelFitCase struct {
	name       string
	label      string
	fontSize   int
	cellWidth  int
	cellHeight int
}

// inputs values the case for the native rule.
func (testCase labelFitCase) inputs() labelFitInputs {
	return labelFitInputs{
		fontSize: float64(testCase.fontSize),
		// UTF-16 units, which is what [NSString length] counts. The shared Go rule
		// counts runes; for every label a grid cell can carry, one ASCII key,
		// they are the same number, and spelling the native side's unit out here
		// means a case that ever disagrees shows the difference rather than hiding
		// it behind a rune count.
		glyphs:     float64(len(utf16.Encode([]rune(testCase.label)))),
		cellWidth:  float64(testCase.cellWidth),
		cellHeight: float64(testCase.cellHeight),
	}
}

// describe spells the case out in full, so a failure carries the configuration
// that produced it rather than a case name to go looking for.
func (testCase labelFitCase) describe() string {
	return fmt.Sprintf(
		"%s (label %q, font size %d, cell %dx%d)",
		testCase.name, testCase.label, testCase.fontSize,
		testCase.cellWidth, testCase.cellHeight,
	)
}

// labelFitDisagreement is one case the two implementations answer differently.
type labelFitDisagreement struct {
	testCase labelFitCase
	native   float64
	shared   float64
}

// labelFitCases are the questions both implementations are asked.
//
// They exist to separate the rules that could plausibly be written here, not to
// cover an input space: each limit is made the binding one on its own, because a
// rule that measures only one axis agrees wherever the other is the tighter of
// the two; a multi-character label is asked, because a width limit that forgot
// the glyph count agrees on every single-character one; and a cell small enough
// for the floor to bind is asked, because that is the only place the floor shows
// up at all. TestLabelFitRulePinCatchesNativeDrift is what keeps this list
// honest.
func labelFitCases() []labelFitCase {
	return []labelFitCase{
		{
			name:       "a cell with room for the configured size",
			label:      "A",
			fontSize:   20,
			cellWidth:  400,
			cellHeight: 400,
		},
		{
			name:       "a narrow cell, width the binding limit",
			label:      "A",
			fontSize:   20,
			cellWidth:  7,
			cellHeight: 400,
		},
		{
			name:       "a short cell, height the binding limit",
			label:      "A",
			fontSize:   20,
			cellWidth:  400,
			cellHeight: 21,
		},
		{
			name:       "a short cell tight enough for the floor to bind",
			label:      "A",
			fontSize:   20,
			cellWidth:  400,
			cellHeight: 7,
		},
		{
			name:       "a cell small on both axes",
			label:      "A",
			fontSize:   20,
			cellWidth:  3,
			cellHeight: 3,
		},
		{
			name:       "the deepest layer of a 5x5 grid on a 1080p screen",
			label:      "A",
			fontSize:   10,
			cellWidth:  15,
			cellHeight: 8,
		},
		{
			name:       "a two-character label in a cell one would fit",
			label:      "AB",
			fontSize:   20,
			cellWidth:  14,
			cellHeight: 400,
		},
		{
			name:       "a two-character label with room to spare",
			label:      "AB",
			fontSize:   10,
			cellWidth:  400,
			cellHeight: 400,
		},
		{
			name:       "an empty label",
			label:      "",
			fontSize:   20,
			cellWidth:  1,
			cellHeight: 1,
		},
		{
			name:       "a cell exactly as wide as the configured size needs",
			label:      "A",
			fontSize:   20,
			cellWidth:  14,
			cellHeight: 400,
		},
		{
			name:       "a cell exactly as tall as the configured size needs",
			label:      "A",
			fontSize:   10,
			cellWidth:  400,
			cellHeight: 14,
		},
		{
			name:       "a one-pixel cell",
			label:      "A",
			fontSize:   10,
			cellWidth:  1,
			cellHeight: 1,
		},
	}
}

// labelFitDisagreements runs every case through the native rule and through the
// shared Go implementation, and returns the cases they answer differently.
func labelFitDisagreements(
	rule nativeLabelFitRule,
	constants map[string]float64,
) []labelFitDisagreement {
	var disagreements []labelFitDisagreement

	for _, testCase := range labelFitCases() {
		style := recursivegrid.NewStyle(recursivegrid.StyleOptions{FontSize: testCase.fontSize})

		shared := style.LabelFontSizeIn(
			testCase.label,
			image.Rect(0, 0, testCase.cellWidth, testCase.cellHeight),
			1,
		)

		native := rule.fittedSize(testCase.inputs(), constants)
		if math.Abs(native-shared) <= labelFitTolerance {
			continue
		}

		disagreements = append(disagreements, labelFitDisagreement{
			testCase: testCase,
			native:   native,
			shared:   shared,
		})
	}

	return disagreements
}

// nativeLabelFitRule is the Objective-C label-fit rule in the only form a Go
// test can hold it to: something it can run.
//
// Nothing here is an expectation. Every field is read out of the .m file, and
// the shared Go implementation is the only expectation this pin has, which is
// what makes the pin bidirectional: change either side alone and the two stop
// answering alike.
type nativeLabelFitRule struct {
	// glyphCountName is what the rule calls the number of glyphs it measures the
	// label as, and emptyGuard is the comparison that returns emptyResult before
	// anything is divided by it.
	glyphCountName string
	emptyGuard     nativeRuleComparison
	emptyResult    string

	// fittedName is what the rule calls the size it narrows, starting at base.
	fittedName string
	base       string

	// limits are the bounds the size is narrowed to, applied in source order.
	limits []nativeLabelFitLimit

	// floor is the size the rule will not go below.
	floor string
}

// nativeLabelFitLimit is one `CGFloat x = a / b; if (x < fitted) fitted = x;` of
// the rule.
type nativeLabelFitLimit struct {
	name     string
	dividend string
	divisors []string
	clamp    nativeRuleComparison
	assigned string
}

// String renders the limit the way its source writes it.
func (limit nativeLabelFitLimit) String() string {
	return fmt.Sprintf(
		"%s = %s / (%s), taken when %s",
		limit.name, limit.dividend, strings.Join(limit.divisors, " * "), limit.clamp,
	)
}

// String renders the rule back as one sentence, so a failure shows what was read
// out of the source rather than only that it disagreed.
func (rule nativeLabelFitRule) String() string {
	limits := make([]string, 0, len(rule.limits))
	for _, limit := range rule.limits {
		limits = append(limits, limit.String())
	}

	return fmt.Sprintf(
		"%s = %s length; when %s, %s; otherwise %s starts at %s, then %s, floored at %s",
		rule.glyphCountName, rule.glyphCountName, rule.emptyGuard, rule.emptyResult,
		rule.fittedName, rule.base, strings.Join(limits, "; then "), rule.floor,
	)
}

// fittedSize answers the question the shared implementation answers: how big is
// this label drawn in this cell?
func (rule nativeLabelFitRule) fittedSize(
	inputs labelFitInputs,
	constants map[string]float64,
) float64 {
	values := make(map[string]float64, len(labelFitOperands)+len(constants)+len(rule.limits)+2)
	for name, read := range labelFitOperands {
		values[name] = read(inputs)
	}

	maps.Copy(values, constants)

	values[rule.glyphCountName] = inputs.glyphs

	if rule.emptyGuard.holds(values) {
		return nativeRuleValue(rule.emptyResult, values)
	}

	values[rule.fittedName] = nativeRuleValue(rule.base, values)

	for _, limit := range rule.limits {
		divisor := 1.0
		for _, factor := range limit.divisors {
			divisor *= nativeRuleValue(factor, values)
		}

		values[limit.name] = nativeRuleValue(limit.dividend, values) / divisor

		if limit.clamp.holds(values) {
			values[rule.fittedName] = nativeRuleValue(limit.assigned, values)
		}
	}

	return math.Max(values[rule.fittedName], nativeRuleValue(rule.floor, values))
}

// withoutLimit drops one bound, standing for a rule that stopped measuring that
// axis.
func (rule nativeLabelFitRule) withoutLimit(index int) nativeLabelFitRule {
	kept := make([]nativeLabelFitLimit, 0, len(rule.limits))

	for position, limit := range rule.limits {
		if position != index {
			kept = append(kept, limit)
		}
	}

	rule.limits = kept

	return rule
}

// withClampOperator rewrites how every limit decides whether to apply.
func (rule nativeLabelFitRule) withClampOperator(op string) nativeLabelFitRule {
	rewritten := make([]nativeLabelFitLimit, 0, len(rule.limits))

	for _, limit := range rule.limits {
		limit.clamp.op = op
		rewritten = append(rewritten, limit)
	}

	rule.limits = rewritten

	return rule
}

// withLimitDivisors rewrites what one limit divides by.
func (rule nativeLabelFitRule) withLimitDivisors(
	index int,
	divisors []string,
) nativeLabelFitRule {
	rewritten := make([]nativeLabelFitLimit, len(rule.limits))
	copy(rewritten, rule.limits)
	rewritten[index].divisors = divisors
	rule.limits = rewritten

	return rule
}

// withFloor rewrites the size the rule will not go below.
func (rule nativeLabelFitRule) withFloor(floor string) nativeLabelFitRule {
	rule.floor = floor

	return rule
}

// withBase rewrites the size the fit starts from.
func (rule nativeLabelFitRule) withBase(base string) nativeLabelFitRule {
	rule.base = base

	return rule
}

// withEmptyGuardOperator rewrites how the rule decides a label has no glyphs to
// measure.
func (rule nativeLabelFitRule) withEmptyGuardOperator(op string) nativeLabelFitRule {
	rule.emptyGuard.op = op

	return rule
}

// nativeLabelFitMethodPattern matches the opening line of the method that
// carries the rule. The declaration in the @interface is excluded by refusing to
// cross a `;`.
var nativeLabelFitMethodPattern = regexp.MustCompile(
	`(?m)^- \(CGFloat\)fittedGridLabelFontSizeFor:[^{};]*inCellRect:\(NSRect\)cellRect[ \t]*\{`,
)

// nativeLabelFitGlyphCountPattern matches the glyph count the rule measures the
// label as.
var nativeLabelFitGlyphCountPattern = regexp.MustCompile(
	`CGFloat[ \t]+(\w+)[ \t]*=[ \t]*\(CGFloat\)\[[ \t]*(\w+)[ \t]+length\][ \t]*;`,
)

// nativeLabelFitEmptyGuardPattern matches the early return for a label with
// nothing to measure.
var nativeLabelFitEmptyGuardPattern = regexp.MustCompile(
	`if[ \t]*\([ \t]*([\w.]+)[ \t]*` + nativeRuleComparisonOperators +
		`[ \t]*([-\w.]+)[ \t]*\)[ \t]*\{\s*return[ \t]+([\w.]+)[ \t]*;\s*\}`,
)

// nativeLabelFitBasePattern matches the size the fit starts from: the one local
// the rule declares from a plain name rather than from an expression.
var nativeLabelFitBasePattern = regexp.MustCompile(
	`CGFloat[ \t]+(\w+)[ \t]*=[ \t]*([\w.]+)[ \t]*;`,
)

// nativeLabelFitLimitPattern matches one bound and the clamp that applies it.
var nativeLabelFitLimitPattern = regexp.MustCompile(
	`CGFloat[ \t]+(\w+)[ \t]*=[ \t]*([\w.]+)[ \t]*/[ \t]*([^;]+);\s*` +
		`if[ \t]*\([ \t]*(\w+)[ \t]*` + nativeRuleComparisonOperators +
		`[ \t]*(\w+)[ \t]*\)[ \t]*\{\s*(\w+)[ \t]*=[ \t]*(\w+)[ \t]*;\s*\}`,
)

// nativeLabelFitFloorPattern matches the floor the rule returns through.
var nativeLabelFitFloorPattern = regexp.MustCompile(
	`return[ \t]+MAX\([ \t]*(\w+)[ \t]*,[ \t]*([\w.]+)[ \t]*\)[ \t]*;`,
)

// labelFitLimitCount is how many bounds the rule narrows the size to: one per
// cell axis.
const labelFitLimitCount = 2

// readNativeLabelFitRule reads the rule out of the macOS overlay, failing the
// test when it cannot. A rule this pin cannot read is a rule it cannot hold to
// the shared one, and passing quietly there would be worse than having no pin at
// all.
func readNativeLabelFitRule(t *testing.T) nativeLabelFitRule {
	t.Helper()

	rule, problem := parseNativeLabelFitRule(readNativeSource(t, labelFitNativeSource))
	if problem != "" {
		t.Fatalf("%s: %s", labelFitNativeSource, problem)
	}

	return rule
}

// parseNativeLabelFitRule reads the label-fit rule out of an Objective-C source.
// The second result describes why the rule could not be read, and is empty when
// it could. An error value would buy nothing here, since the only caller turns
// it straight into a test failure.
func parseNativeLabelFitRule(source string) (nativeLabelFitRule, string) {
	body, problem := nativeRuleMethodBody(
		source,
		nativeLabelFitMethodPattern,
		labelFitNativeMethod,
		"- (CGFloat)fittedGridLabelFontSizeFor:...inCellRect:(NSRect)cellRect {",
	)
	if problem != "" {
		return nativeLabelFitRule{}, problem
	}

	rule := nativeLabelFitRule{}

	rule.glyphCountName, problem = parseNativeLabelFitGlyphCount(body)
	if problem != "" {
		return nativeLabelFitRule{}, problem
	}

	rule.emptyGuard, rule.emptyResult, problem = parseNativeLabelFitEmptyGuard(body)
	if problem != "" {
		return nativeLabelFitRule{}, problem
	}

	rule.fittedName, rule.base, problem = parseNativeLabelFitBase(body, rule.glyphCountName)
	if problem != "" {
		return nativeLabelFitRule{}, problem
	}

	rule.limits, problem = parseNativeLabelFitLimits(body, rule.fittedName)
	if problem != "" {
		return nativeLabelFitRule{}, problem
	}

	rule.floor, problem = parseNativeLabelFitFloor(body, rule.fittedName)
	if problem != "" {
		return nativeLabelFitRule{}, problem
	}

	return rule, validateNativeLabelFitRule(rule)
}

// parseNativeLabelFitGlyphCount reads what the rule counts the label's glyphs
// into.
func parseNativeLabelFitGlyphCount(body string) (string, string) {
	matches := nativeLabelFitGlyphCountPattern.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		return "", fmt.Sprintf(
			"%s holds %d glyph counts shaped `CGFloat <name> = (CGFloat)[<label> length];`, want exactly 1 (rewritten?)",
			labelFitNativeMethod,
			len(matches),
		)
	}

	return matches[0][1], ""
}

// parseNativeLabelFitEmptyGuard reads the early return for a label with nothing
// to measure. It is the half of the rule that keeps the width limit from
// dividing by zero, so a source without it is not a source this pin will run.
func parseNativeLabelFitEmptyGuard(body string) (nativeRuleComparison, string, string) {
	matches := nativeLabelFitEmptyGuardPattern.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		return nativeRuleComparison{}, "", fmt.Sprintf(
			"%s holds %d empty-label guards shaped `if (<count> <op> <literal>) { return <size>; }`, want exactly 1 (rewritten?)",
			labelFitNativeMethod,
			len(matches),
		)
	}

	match := matches[0]

	return nativeRuleComparison{left: match[1], op: match[2], right: match[3]}, match[4], ""
}

// parseNativeLabelFitBase reads the size the fit starts from, which is the one
// local declared from a plain name rather than an expression. The glyph count is
// declared from an expression, so it is not a candidate; a second plain
// declaration is, and is reported rather than picked between.
func parseNativeLabelFitBase(body, glyphCountName string) (string, string, string) {
	var found [][]string

	for _, match := range nativeLabelFitBasePattern.FindAllStringSubmatch(body, -1) {
		if match[1] == glyphCountName {
			continue
		}

		found = append(found, match)
	}

	if len(found) != 1 {
		return "", "", fmt.Sprintf(
			"%s holds %d sizes shaped `CGFloat <name> = <size>;` for the fit to start from, want exactly 1 (rewritten?)",
			labelFitNativeMethod,
			len(found),
		)
	}

	return found[0][1], found[0][2], ""
}

// parseNativeLabelFitLimits reads the bounds the size is narrowed to, in source
// order, checking each one narrows the size the fit started from rather than
// some other local.
func parseNativeLabelFitLimits(body, fittedName string) ([]nativeLabelFitLimit, string) {
	matches := nativeLabelFitLimitPattern.FindAllStringSubmatch(body, -1)
	if len(matches) != labelFitLimitCount {
		return nil, fmt.Sprintf(
			"%s holds %d limits shaped `CGFloat <name> = <a> / <b>; if (<name> <op> %s) { %s = <name>; }`, want exactly %d, one per cell axis (rewritten?)",
			labelFitNativeMethod,
			len(matches),
			fittedName,
			fittedName,
			labelFitLimitCount,
		)
	}

	limits := make([]nativeLabelFitLimit, 0, len(matches))

	for _, match := range matches {
		divisors, problem := parseNativeLabelFitDivisors(match[3])
		if problem != "" {
			return nil, problem
		}

		limit := nativeLabelFitLimit{
			name:     match[1],
			dividend: match[2],
			divisors: divisors,
			clamp:    nativeRuleComparison{left: match[4], op: match[5], right: match[6]},
			assigned: match[8],
		}

		if match[7] != fittedName || limit.assigned != limit.name {
			return nil, fmt.Sprintf(
				"the limit `%s` assigns %s = %s; this pin reads a limit that narrows %s to itself",
				limit.name, match[7], limit.assigned, fittedName,
			)
		}

		limits = append(limits, limit)
	}

	return limits, ""
}

// parseNativeLabelFitDivisors splits what a limit divides by into its factors.
func parseNativeLabelFitDivisors(expression string) ([]string, string) {
	trimmed := strings.TrimSpace(expression)
	trimmed = strings.TrimSuffix(strings.TrimPrefix(trimmed, "("), ")")

	var divisors []string

	for factor := range strings.SplitSeq(trimmed, "*") {
		factor = strings.TrimSpace(factor)
		if factor == "" {
			return nil, fmt.Sprintf(
				"`%s` is not a product of factors this pin reads",
				strings.TrimSpace(expression),
			)
		}

		divisors = append(divisors, factor)
	}

	return divisors, ""
}

// parseNativeLabelFitFloor reads the size the rule will not go below.
func parseNativeLabelFitFloor(body, fittedName string) (string, string) {
	matches := nativeLabelFitFloorPattern.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		return "", fmt.Sprintf(
			"%s holds %d floors shaped `return MAX(%s, <size>);`, want exactly 1 (rewritten?)",
			labelFitNativeMethod, len(matches), fittedName,
		)
	}

	if matches[0][1] != fittedName {
		return "", fmt.Sprintf(
			"the rule returns MAX(%s, %s), which is not the size it narrowed (%s)",
			matches[0][1], matches[0][2], fittedName,
		)
	}

	return matches[0][2], ""
}

// validateNativeLabelFitRule checks that every name and operator the rule was
// written with is one this pin can evaluate. It is what stops a rewritten rule
// from being run with a value invented for it.
func validateNativeLabelFitRule(rule nativeLabelFitRule) string {
	declared := map[string]bool{
		rule.glyphCountName: true,
		rule.fittedName:     true,
	}

	comparisons := make([]nativeRuleComparison, 0, 1+len(rule.limits))
	comparisons = append(comparisons, rule.emptyGuard)

	tokens := []string{rule.emptyResult, rule.base, rule.floor}

	for _, limit := range rule.limits {
		declared[limit.name] = true
		comparisons = append(comparisons, limit.clamp)
		tokens = append(tokens, limit.dividend, limit.assigned)
		tokens = append(tokens, limit.divisors...)
	}

	for _, comparison := range comparisons {
		if _, known := nativeRuleComparators[comparison.op]; !known {
			return fmt.Sprintf(
				"`%s` compares with %q, which this pin does not read",
				comparison, comparison.op,
			)
		}

		tokens = append(tokens, comparison.left, comparison.right)
	}

	// The constants the rule reads are declared in the same file and read from it,
	// so a name this pin cannot bind is a name that is neither a local, an
	// operand, a kGridLabel* constant, nor a literal.
	for _, token := range tokens {
		if declared[token] || strings.HasPrefix(token, "kGridLabel") {
			continue
		}

		if _, bound := labelFitOperands[token]; bound {
			continue
		}

		_, parseErr := strconv.ParseFloat(token, 64)
		if parseErr == nil {
			continue
		}

		return fmt.Sprintf(
			"the rule reads %s, which this pin cannot value; it knows %s, the kGridLabel* constants, the locals it declares, and numeric literals",
			token,
			strings.Join(sortedNativeRuleOperands(labelFitOperands), ", "),
		)
	}

	return ""
}

// nativeLabelFitMethodSource wraps a body in the method definition the parser
// looks for, so a test can hand it a rule shaped differently from the one in the
// tree.
func nativeLabelFitMethodSource(body string) string {
	return "- (CGFloat)fittedGridLabelFontSizeFor:(NSString *)label inCellRect:(NSRect)cellRect {\n" +
		body + "\n}\n"
}
