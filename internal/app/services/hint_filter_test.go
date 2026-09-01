package services

import (
	"slices"
	"testing"

	"go.uber.org/zap"

	"github.com/y3owk1n/neru/internal/config"
)

// The filter's text terms are folded here, at the one place the filter is built,
// because ports.ElementFilter.Matches compares against lowercased element text and
// does not fold the terms itself. This used to happen inside the accessibility
// adapter, which is why the vision half - reaching matching by another route - had
// no case folding and no text filtering at all.

func TestHintFilterFoldsTextTerms(t *testing.T) {
	t.Parallel()

	service := &HintService{logger: zap.NewNop()}

	filter, usable := service.hintFilter(
		config.HintsConfig{},
		"",
		nil,
		[]string{"Save As", "OK", "Cancel"},
	)
	if !usable {
		t.Fatal("hintFilter reported the filter unusable")
	}

	if filter.TitleContains != "save as" {
		t.Errorf("TitleContains = %q, want it folded", filter.TitleContains)
	}

	if filter.DescriptionContains != "save as" {
		t.Errorf("DescriptionContains = %q, want it folded", filter.DescriptionContains)
	}

	if filter.ValueContains != "save as" {
		t.Errorf("ValueContains = %q, want it folded", filter.ValueContains)
	}

	if want := []string{"ok", "cancel"}; !slices.Equal(filter.TextContainsList, want) {
		t.Errorf("TextContainsList = %v, want %v", filter.TextContainsList, want)
	}
}

// The caller's slice travels by reference even though the filter travels by value,
// so folding in place would rewrite terms the caller may still hold - the mode
// handler keeps them to replay on refresh.
func TestHintFilterLeavesTheCallersTermsAlone(t *testing.T) {
	t.Parallel()

	service := &HintService{logger: zap.NewNop()}
	terms := []string{"Save As", "OK"}

	service.hintFilter(config.HintsConfig{}, "", nil, terms)

	if want := []string{"Save As", "OK"}; !slices.Equal(terms, want) {
		t.Errorf("caller's terms = %v, want them untouched", terms)
	}
}

func TestHintFilterWithNoTermsConsultsNoText(t *testing.T) {
	t.Parallel()

	service := &HintService{logger: zap.NewNop()}

	filter, usable := service.hintFilter(config.HintsConfig{}, "", nil, nil)
	if !usable {
		t.Fatal("hintFilter reported the filter unusable")
	}

	if filter.TitleContains != "" || filter.DescriptionContains != "" ||
		filter.ValueContains != "" || len(filter.TextContainsList) != 0 {
		t.Errorf("filter carries text terms with none asked for: %+v", filter)
	}
}
