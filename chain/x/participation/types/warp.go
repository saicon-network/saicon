package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// WARP is the asymmetric smoother of the Participation Score (ADR-0009 §2):
//
//	PS <  WARP :  WARP ← WARP − fall·(WARP − PS)
//	PS >= WARP :  WARP ← WARP + rise·(PS − WARP)
//	eligible ⇔ WARP >= threshold ;  a new validator starts at initial
//
// on a 0-100 scale. The defaults are the Decision Register v1.0 baseline. They
// are asserted, not calibrated on real epochs (ADR-0009, blocker B2).

// WarpScale is the top of the scale WARP and the Participation Score it reads
// are measured on.
func WarpScale() math.LegacyDec { return math.LegacyNewDec(100) }

// DefaultWarpFall is the default warp_fall: 0.65.
func DefaultWarpFall() math.LegacyDec { return math.LegacyNewDecWithPrec(65, 2) }

// DefaultWarpRise is the default warp_rise: 0.10.
func DefaultWarpRise() math.LegacyDec { return math.LegacyNewDecWithPrec(10, 2) }

// DefaultWarpInitial is the default warp_initial: 80.
func DefaultWarpInitial() math.LegacyDec { return math.LegacyNewDec(80) }

// DefaultWarpThreshold is the default warp_threshold: 65.
func DefaultWarpThreshold() math.LegacyDec { return math.LegacyNewDec(65) }

// WarpInput is the Participation Score WARP reads, on 0-100, from an epoch's
// computed_score, which is on 0-1 (S10 ruling R1). The score is uptime alone
// today, so uptime carries the whole weight until BlockQuality and Governance
// have producers. This is the only place the two scales meet.
func WarpInput(computedScore math.LegacyDec) math.LegacyDec {
	return computedScore.Mul(WarpScale())
}

// NextWarp applies one epoch of the rule to warp with the epoch's score ps,
// both on 0-100, and clamps the result to [0, 100]. With valid parameters the
// rule cannot leave the range; the clamp makes that a property of the code.
func NextWarp(warp, ps, fall, rise math.LegacyDec) math.LegacyDec {
	var next math.LegacyDec
	if ps.LT(warp) {
		next = warp.Sub(fall.Mul(warp.Sub(ps)))
	} else {
		next = warp.Add(rise.Mul(ps.Sub(warp)))
	}
	return ClampWarp(next)
}

// ClampWarp bounds v to [0, 100].
func ClampWarp(v math.LegacyDec) math.LegacyDec {
	if v.IsNegative() {
		return math.LegacyZeroDec()
	}
	if v.GT(WarpScale()) {
		return WarpScale()
	}
	return v
}

// WarpEligible is WARP's own eligibility predicate: value >= threshold. The
// active-set pool also requires C = 1 and registry ACTIVE (ADR-0008 §1); that
// conjunction is the voting-power preview's, not this function's.
func WarpEligible(value, threshold math.LegacyDec) bool {
	return value.GTE(threshold)
}

// ValidateWarpValue checks a stored WARP: set, and within [0, 100].
func ValidateWarpValue(v math.LegacyDec) error {
	if v.IsNil() {
		return fmt.Errorf("value is not set")
	}
	if v.IsNegative() || v.GT(WarpScale()) {
		return fmt.Errorf("value %s is outside [0, 100]", v)
	}
	return nil
}

// Warp is the four WARP params as the queries and x/rewards read them.
func (p Params) Warp() WarpParams {
	return WarpParams{
		Fall:      p.WarpFall,
		Rise:      p.WarpRise,
		Initial:   p.WarpInitial,
		Threshold: p.WarpThreshold,
	}
}

// ValidateWarp checks the four WARP params: Warp().Validate().
func (p Params) ValidateWarp() error { return p.Warp().Validate() }

// Validate checks R8's rules: every value set, 0 < rise < fall <= 1 (the
// asymmetry is the decision) and 0 <= threshold <= initial <= 100 (a new
// validator starts eligible). It is the one gate for every reader: genesis,
// MsgUpdateParams, the epoch step and the voting-power preview. A store
// written from params without WARP fields reads back zeros, not nils, so a
// nil check alone does not catch it.
func (p WarpParams) Validate() error {
	for _, f := range []struct {
		name string
		v    math.LegacyDec
	}{
		{"warp_fall", p.Fall},
		{"warp_rise", p.Rise},
		{"warp_initial", p.Initial},
		{"warp_threshold", p.Threshold},
	} {
		if f.v.IsNil() {
			return fmt.Errorf("%s must not be nil", f.name)
		}
	}
	if !p.Rise.IsPositive() || !p.Rise.LT(p.Fall) || p.Fall.GT(math.LegacyOneDec()) {
		return fmt.Errorf("WARP rates must satisfy 0 < warp_rise < warp_fall <= 1, got warp_rise %s, warp_fall %s",
			p.Rise, p.Fall)
	}
	if p.Threshold.IsNegative() || p.Threshold.GT(p.Initial) || p.Initial.GT(WarpScale()) {
		return fmt.Errorf("WARP levels must satisfy 0 <= warp_threshold <= warp_initial <= 100, got warp_threshold %s, warp_initial %s",
			p.Threshold, p.Initial)
	}
	return nil
}
