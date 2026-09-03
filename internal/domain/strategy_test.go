package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/y3owk1n/neru/internal/domain"
)

// The two predicates were one predicate until contour arrived, which reads the
// screen and finds no text. Asserting them side by side is what keeps the pair
// from collapsing back together.
func TestStrategyPredicates(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		strategy    string
		readsScreen bool
		readsText   bool
	}{
		"axtree":  {domain.StrategyAXTree, false, false},
		"vision":  {domain.StrategyVision, true, true},
		"hybrid":  {domain.StrategyHybrid, true, true},
		"contour": {domain.StrategyContour, true, false},
		"unset":   {"", false, false},
		"unknown": {"nonsense", false, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.readsScreen, domain.StrategyReadsScreen(tc.strategy),
				"StrategyReadsScreen decides the capture permission and the overlay clear")
			assert.Equal(t, tc.readsText, domain.StrategyReadsText(tc.strategy),
				"StrategyReadsText decides whether --split-word means anything")
		})
	}
}
