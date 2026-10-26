package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
)

// ObserveGasFees records the gas revenue sitting in the fee collector at the
// start of a block, so the epoch's mint can be reduced by it (ADR-0011 D2).
//
// # It observes; it does not take
//
// x/distribution sweeps the whole fee collector every block and pays it out
// stake-weighted. If x/rewards ALSO moved those coins into its pool, the same
// fees would be paid twice — once by distribution and once here. So this reads
// the balance and leaves it alone.
//
// The consequence is that the fee half of a validator's income is paid
// stake-weighted and is NOT subject to the PoP gate (ACTIVE ∧ C=1 ∧ EP>0), while
// the issuance half is. That is the pre-existing behaviour of the chain, not
// something introduced here — but it is a real gap, and closing it is the open
// F1/F3 decision in ADR-0011 D2: route the pool through
// x/distribution.AllocateTokensToValidator so one gate covers both halves.
//
// # Why this must run first in the block
//
// x/distribution is BeginBlockers[1] and empties the collector. To see the
// revenue at all, x/rewards is ordered ahead of it — reading only, so the
// reorder cannot change what distribution does.
//
// Errors are returned to the caller, which logs rather than halts: a missed
// observation means one block's fees fail to offset issuance, which is a small
// over-issuance, not a reason to stop the chain.
func (k Keeper) ObserveGasFees(ctx context.Context) error {
	collector := k.authKeeper.GetModuleAddress(authtypes.FeeCollectorName)
	if collector == nil {
		return nil
	}
	return k.AddObservedGasFees(ctx, k.bankKeeper.GetBalance(ctx, collector, types.RewardDenom).Amount)
}

// AddObservedGasFees adds to the current epoch's observed revenue.
//
// A non-positive amount is ignored rather than applied: this is fed from a
// balance read, and a counter that can go backwards would silently INCREASE
// issuance — the opposite of what the offset is for.
func (k Keeper) AddObservedGasFees(ctx context.Context, amount math.Int) error {
	if amount.IsNil() || !amount.IsPositive() {
		return nil
	}
	current, err := k.GetObservedGasFees(ctx)
	if err != nil {
		return err
	}
	return k.ObservedGasFees.Set(ctx, current.Add(amount))
}

// GetObservedGasFees returns the revenue observed so far this epoch. Absent
// reads as zero.
func (k Keeper) GetObservedGasFees(ctx context.Context) (math.Int, error) {
	v, err := k.ObservedGasFees.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return math.ZeroInt(), nil
		}
		return math.ZeroInt(), err
	}
	return v, nil
}

// resetObservedGasFees clears the counter at an epoch boundary. Called once the
// epoch's mint has been sized, so revenue is spent against exactly one epoch.
func (k Keeper) resetObservedGasFees(ctx context.Context) error {
	return k.ObservedGasFees.Set(ctx, math.ZeroInt())
}

// feeCollectorAddress is a small indirection so tests can assert the module
// reads the account the SDK actually credits.
var _ = sdk.AccAddress(nil)
