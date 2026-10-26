package types

import "cosmossdk.io/math"

// GlobalCap is min(max_supply_fraction × maxSupply, public_circulating_fraction ×
// publicCirculating), truncated to whole nscn (ADR-0012 D2).
func GlobalCap(p Params, maxSupply, publicCirculating math.Int) math.Int {
	bySupply := p.MaxSupplyFraction.MulInt(maxSupply).TruncateInt()
	byCirculating := p.PublicCirculatingFraction.MulInt(publicCirculating).TruncateInt()
	return math.MinInt(bySupply, byCirculating)
}

// NextEWMA advances the exponentially weighted moving average of total bonded by
// one epoch, alpha = 2 / (window + 1). A first observation — or a missing previous
// value — returns the observation itself: an EWMA that started from zero would
// make the per-validator cap near zero for the first thirty epochs.
func NextEWMA(prev math.LegacyDec, bonded math.Int, window uint64, first bool) math.LegacyDec {
	b := math.LegacyNewDecFromInt(bonded)
	if first || prev.IsNil() || window == 0 {
		return b
	}
	alpha := math.LegacyNewDec(2).QuoInt64(int64(window) + 1)
	return alpha.Mul(b).Add(math.LegacyOneDec().Sub(alpha).Mul(prev))
}

// ValidatorCap is max(validator_cap_fraction × ewma, validator_cap_floor).
func ValidatorCap(p Params, ewma math.LegacyDec) math.Int {
	byFraction := p.ValidatorCapFraction.Mul(ewma).TruncateInt()
	return math.MaxInt(byFraction, p.ValidatorCapFloor)
}

// PublicCirculating is supply minus what is not publicly owned, floored at zero.
// The caller passes module balances and excluded holdings; it must NOT pass the
// bonded pool, the not-bonded pool or the queue escrow (see NonCirculatingModules).
func PublicCirculating(supply math.Int, nonCirculating ...math.Int) math.Int {
	pc := supply
	for _, x := range nonCirculating {
		if !x.IsNil() {
			pc = pc.Sub(x)
		}
	}
	if pc.IsNegative() {
		return math.ZeroInt()
	}
	return pc
}
