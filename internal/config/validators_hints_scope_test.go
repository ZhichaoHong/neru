package config_test

import (
	"strings"
	"testing"

	"github.com/y3owk1n/neru/internal/config"
	"github.com/y3owk1n/neru/internal/domain"
)

// TestValidateHints_RefusesAScopeNothingRecognizes keeps hints.scope on the
// refusal side of the tier line: a word no strategy can act on is a typo, and a
// typo that loads would silently read the wrong region.
func TestValidateHints_RefusesAScopeNothingRecognizes(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Hints.Scope = "monitor"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() accepted a scope nothing recognizes")
	}

	if !strings.Contains(err.Error(), "hints.scope") {
		t.Errorf("error = %q, want it to name hints.scope", err)
	}
}

// TestValidateHints_AcceptsTheScopeVocabulary pins both words and the empty
// value, which is what a file that says nothing leaves behind.
func TestValidateHints_AcceptsTheScopeVocabulary(t *testing.T) {
	t.Parallel()

	for _, scope := range []string{domain.HintScopeWindow, domain.HintScopeScreen, ""} {
		t.Run("scope="+scope, func(t *testing.T) {
			t.Parallel()

			cfg := config.DefaultConfig()
			cfg.Hints.Scope = scope
			cfg.Hints.Strategy = domain.StrategyVision

			err := cfg.Validate()
			if err != nil {
				t.Errorf("Validate() refused scope %q: %v", scope, err)
			}
		})
	}
}

// TestValidateWithWarnings_ReportsAScreenScopeNoStrategyReads is the cross-field
// rule: the setting loads, and under a tree walk it does nothing, so it warns
// rather than refusing (ADR 0002 — refusing costs the whole file).
func TestValidateWithWarnings_ReportsAScreenScopeNoStrategyReads(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Hints.Strategy = domain.StrategyAXTree
	cfg.Hints.Scope = domain.HintScopeScreen

	warnings := &config.Warnings{}

	err := cfg.ValidateWithWarnings(warnings, config.WrittenConfig{})
	if err != nil {
		t.Fatalf("ValidateWithWarnings() refused an inert combination: %v", err)
	}

	messages := warnings.Messages()
	if len(messages) != 1 || !strings.Contains(messages[0], "hints.scope") {
		t.Fatalf("warnings = %q, want exactly one naming hints.scope", messages)
	}
}

// TestValidateWithWarnings_LeavesAScreenScopeWithAScreenReaderSilent covers the
// two ways the wider scope does mean something: the global strategy reads the
// screen, or one application's does.
func TestValidateWithWarnings_LeavesAScreenScopeWithAScreenReaderSilent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutation func(*config.Config)
	}{
		{
			name: "the global strategy reads the screen",
			mutation: func(cfg *config.Config) {
				cfg.Hints.Strategy = domain.StrategyHybrid
			},
		},
		{
			name: "one application reads the screen",
			mutation: func(cfg *config.Config) {
				cfg.Hints.Strategy = domain.StrategyAXTree
				cfg.Hints.AppConfigs = []config.AppConfig{
					{BundleID: "mstsc.exe", Strategy: domain.StrategyContour},
				}
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.DefaultConfig()
			cfg.Hints.Scope = domain.HintScopeScreen
			testCase.mutation(cfg)

			warnings := &config.Warnings{}

			err := cfg.ValidateWithWarnings(warnings, config.WrittenConfig{})
			if err != nil {
				t.Fatalf("ValidateWithWarnings() refused a resolvable configuration: %v", err)
			}

			if got := warnings.Messages(); len(got) != 0 {
				t.Errorf("warnings = %q, want none", got)
			}
		})
	}
}

// TestValidateWithWarnings_LeavesTheDefaultScopeSilent pins that the rule reads
// the scope rather than the strategy: the shipped default is a tree walk at
// window scope and has nothing to say.
func TestValidateWithWarnings_LeavesTheDefaultScopeSilent(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()

	warnings := &config.Warnings{}

	err := cfg.ValidateWithWarnings(warnings, config.WrittenConfig{})
	if err != nil {
		t.Fatalf("ValidateWithWarnings() refused the defaults: %v", err)
	}

	if got := warnings.Messages(); len(got) != 0 {
		t.Errorf("warnings = %q, want none", got)
	}
}
