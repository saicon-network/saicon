package types

import (
	"sort"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
)

// The predicates below are the S6/S7 stake rules in one place. The ante
// decorator calls them on messages; the staking hooks call them on what a
// message actually did. One rule, two call sites (S7 spec §6.2).

// CheckGrowth fails only when value grew AND now exceeds limit. A value already
// above a limit that falls is fine: caps freeze, they never force-shed (ADR-0012).
func CheckGrowth(before, after, limit math.Int, err *errorsmod.Error, what string) error {
	if after.GT(before) && after.GT(limit) {
		return errorsmod.Wrapf(err, "%s would reach %s nscn; the limit is %s nscn", what, after, limit)
	}
	return nil
}

// CheckSelfBondFloor: an operator's own stake is either zero (it left) or at
// least the entry floor.
func CheckSelfBondFloor(after math.Int) error {
	if after.IsPositive() && after.LT(EntryFloor()) {
		return errorsmod.Wrapf(ErrSelfBondBelowFloor,
			"the operator would keep %s nscn of its own stake, below the %s nscn entry floor; withdraw all of it to exit", after, EntryFloor())
	}
	return nil
}

// CheckNewSlot: validator may be added unless it is new and the delegator is full.
// slots must be sorted.
func CheckNewSlot(slots []string, validator string, max uint32) error {
	i := sort.SearchStrings(slots, validator)
	if i < len(slots) && slots[i] == validator {
		return nil
	}
	if uint32(len(slots)) >= max {
		return errorsmod.Wrapf(ErrTooManyValidators,
			"a delegator may hold %d validators; it holds %d, and %s would be another — move all stake from one first", max, len(slots), validator)
	}
	return nil
}

// CheckMinDelegation: the smallest delegation a message may make.
func CheckMinDelegation(amount, min math.Int) error {
	if amount.LT(min) {
		return errorsmod.Wrapf(ErrBelowMinDelegation, "%s nscn; the minimum is %s", amount, describeNscn(min))
	}
	return nil
}

// describeNscn renders an amount in SCN when it divides evenly, else in nscn.
func describeNscn(n math.Int) string {
	unit := math.NewInt(scn)
	if n.IsPositive() && n.Mod(unit).IsZero() {
		return n.Quo(unit).String() + " SCN"
	}
	return n.String() + " nscn"
}
