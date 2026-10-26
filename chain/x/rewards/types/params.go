package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// DefaultRewardPerEpochAmount is the flat per-validator per-epoch reward minted
// to each eligible validator in the v0.1 MOCK emission model, in base denom
// "nscn" (1 SCN = 100_000_000 nscn). See proto/chain/rewards/v1/params.proto.
const DefaultRewardPerEpochAmount uint64 = 1000000

// RewardDenom is the base denom rewards are minted in (v0.1: hardcoded).
const RewardDenom = "nscn"

// maxSupplySCN is SAiCoN's fixed maximum supply in display units.
const maxSupplySCN = 21_000_000

// MaxSupplyNscn is the hard supply ceiling in base units — 21,000,000 SCN, or
// 2.1e15 nscn. Emission is clamped to the headroom below this value before any
// mint (ADR-0011 D4); the cap binds on ISSUANCE only, never on the reward pool
// as a whole, because gas fees routed into the pool are already in circulation
// and re-routing them creates nothing.
func MaxSupplyNscn() math.Int {
	return math.NewInt(maxSupplySCN).Mul(math.NewInt(1).MulRaw(1e8))
}

// DefaultBlocksPerYear is the x/mint precedent for 5-second blocks. Epochs per
// year are DERIVED from it and x/participation's epoch length, never stored
// separately — see Params.blocks_per_year.
const DefaultBlocksPerYear uint64 = 6_311_520

// The three clip constants of the annual security budget (ADR-0011 D1) are the
// Register v1.0 values divided by seven — S6, accepted 2026-09-24 at the
// application team's request. The quotient is computed rather than written out,
// so each constant is exactly "the Register value / 7" at LegacyDec precision
// and no one chooses a rounding. On the devnet, where the ceiling binds, this
// takes issuance from ~205 to ~29 SCN per day.
func DefaultBudgetBondedRate() math.LegacyDec  { return math.LegacyNewDecWithPrec(15, 2).QuoInt64(7) } // 0.15 / 7
func DefaultBudgetFloorRate() math.LegacyDec   { return math.LegacyNewDecWithPrec(5, 3).QuoInt64(7) }  // 0.005 / 7
func DefaultBudgetCeilingRate() math.LegacyDec { return math.LegacyNewDecWithPrec(75, 4).QuoInt64(7) } // 0.0075 / 7

// NewParams creates a new Params instance with the budget constants at the
// Register v1.0 values divided by seven (see above).
func NewParams(rewardPerEpochAmount uint64, enabled bool) Params {
	return Params{
		RewardPerEpochAmount: rewardPerEpochAmount,
		Enabled:              enabled,
		BlocksPerYear:        DefaultBlocksPerYear,
		BudgetBondedRate:     DefaultBudgetBondedRate(),
		BudgetFloorRate:      DefaultBudgetFloorRate(),
		BudgetCeilingRate:    DefaultBudgetCeilingRate(),
	}
}

// DefaultParams returns a default set of parameters: emission enabled with the
// default flat per-epoch reward and the budget constants at the Register v1.0
// values divided by seven.
func DefaultParams() Params {
	return NewParams(DefaultRewardPerEpochAmount, true)
}

// Validate validates the set of params.
//
// The budget rates are the only values here that can make emission nonsensical
// rather than merely zero, so they are the only ones with real constraints: each
// must be a set, non-negative fraction, and the ceiling must not sit below the
// floor — an inverted clip would silently emit the floor forever.
func (p Params) Validate() error {
	// reward_per_epoch_amount of 0 is permitted (emission of nothing), and the
	// uint64 type already bounds the value. enabled is a plain bool.
	for _, r := range []struct {
		name string
		v    math.LegacyDec
	}{
		{"budget_bonded_rate", p.BudgetBondedRate},
		{"budget_floor_rate", p.BudgetFloorRate},
		{"budget_ceiling_rate", p.BudgetCeilingRate},
	} {
		if r.v.IsNil() {
			return fmt.Errorf("%s is not set", r.name)
		}
		if r.v.IsNegative() {
			return fmt.Errorf("%s must not be negative, got %s", r.name, r.v)
		}
		if r.v.GT(math.LegacyOneDec()) {
			return fmt.Errorf("%s must not exceed 1, got %s", r.name, r.v)
		}
	}
	if p.BudgetCeilingRate.LT(p.BudgetFloorRate) {
		return fmt.Errorf("budget_ceiling_rate %s is below budget_floor_rate %s: an inverted clip would emit the floor forever",
			p.BudgetCeilingRate, p.BudgetFloorRate)
	}
	return nil
}
