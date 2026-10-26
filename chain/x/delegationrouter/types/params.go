package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// DefaultParams returns the spec defaults.
func DefaultParams() Params {
	return Params{
		MinRouteScore: math.LegacyNewDecWithPrec(75, 2),
		MaxRouteLegs:  2,
	}
}

// Validate enforces the tier-2 bounds.
func (p Params) Validate() error {
	if p.MinRouteScore.IsNil() {
		return fmt.Errorf("min_route_score is not set")
	}
	lo, hi := math.LegacyNewDecWithPrec(50, 2), math.LegacyOneDec()
	if p.MinRouteScore.LT(lo) || p.MinRouteScore.GT(hi) {
		return fmt.Errorf("min_route_score must be within [%s, %s], got %s", lo, hi, p.MinRouteScore)
	}
	if p.MaxRouteLegs < 1 || p.MaxRouteLegs > 4 {
		return fmt.Errorf("max_route_legs must be within [1, 4], got %d", p.MaxRouteLegs)
	}
	return nil
}
