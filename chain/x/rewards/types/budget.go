package types

import "cosmossdk.io/math"

// AnnualSecurityBudget is the Register's emission budget for one year, in base
// units (ADR-0011 D1):
//
//	clip( bonded_rate x TotalBonded,
//	      floor_rate   x CurrentSupply,     // floor
//	      ceiling_rate x CurrentSupply )    // ceiling
//
// The shape is the decision, not the constants. The budget tracks what the
// network actually secures, so it grows with real stake — but it may never fall
// below the floor, or security could be starved exactly when little is bonded
// and the chain is at its most vulnerable; and it may never exceed the ceiling,
// so a stake boom cannot turn into supply inflation. At the Register's global
// bonded cap the ceiling already binds, which is where the headline 357 SCN/day
// comes from.
//
// A zero supply yields a zero budget: a chain with nothing in it cannot fund
// anything, and the floor of nothing is nothing.
func AnnualSecurityBudget(p Params, totalBonded, currentSupply math.Int) math.Int {
	if currentSupply.IsNil() || !currentSupply.IsPositive() {
		return math.ZeroInt()
	}
	if totalBonded.IsNil() || totalBonded.IsNegative() {
		totalBonded = math.ZeroInt()
	}
	// An unset rate is reachable: Params.Validate is NOT on the InitChain path
	// (module.Manager never calls it), so a hand-written genesis omitting the
	// rewards params reaches the keeper with nil decimals. Multiplying a nil
	// LegacyDec panics — and this runs inside EndBlock, where a panic is a
	// network-wide halt. Emitting nothing is the safe answer.
	if p.BudgetFloorRate.IsNil() || p.BudgetCeilingRate.IsNil() || p.BudgetBondedRate.IsNil() {
		return math.ZeroInt()
	}

	supply := math.LegacyNewDecFromInt(currentSupply)
	floor := p.BudgetFloorRate.Mul(supply).TruncateInt()
	ceiling := p.BudgetCeilingRate.Mul(supply).TruncateInt()
	staked := p.BudgetBondedRate.Mul(math.LegacyNewDecFromInt(totalBonded)).TruncateInt()

	switch {
	case staked.LT(floor):
		return floor
	case staked.GT(ceiling):
		return ceiling
	default:
		return staked
	}
}

// EpochTarget converts an annual budget into one epoch's share.
//
// Epochs are counted in BLOCKS, never wall-clock (Register §2.1), so the
// conversion is too: epochs_per_year = blocks_per_year / epoch_length_blocks.
// The epoch length lives in x/participation and is passed in rather than stored
// here — one fact, one home, so the two cannot drift apart.
//
// Returns zero rather than dividing when the divisors make the question
// meaningless: a zero epoch length, a zero blocks_per_year, or an epoch longer
// than a year (integer division would make epochs_per_year zero). All three are
// reachable through governance, and an emission of nothing is a safe answer
// where a panic in the EndBlocker would not be.
func EpochTarget(p Params, annualBudget math.Int, epochLengthBlocks uint64) math.Int {
	if epochLengthBlocks == 0 || p.BlocksPerYear == 0 {
		return math.ZeroInt()
	}
	epochsPerYear := p.BlocksPerYear / epochLengthBlocks
	if epochsPerYear == 0 {
		return math.ZeroInt()
	}
	if annualBudget.IsNil() || !annualBudget.IsPositive() {
		return math.ZeroInt()
	}
	return annualBudget.Quo(math.NewIntFromUint64(epochsPerYear))
}
