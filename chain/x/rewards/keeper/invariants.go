package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
)

// RewardGateInvariant asserts invariant V-2 for a single epoch:
//
//	a validator that received a POSITIVE reward MUST have EP > 0.
//
// It scans every RewardSnapshot stored for epochID and, for each snapshot that
// was minted (Eligible with a non-empty Amount), reads that validator's
// accumulated Empirical Points via x/participation. If EP is missing or not
// positive the invariant is broken and the returned message names the offending
// validator.
//
// This is defense-in-depth: DistributeEpochRewards only mints when EP>0, so this
// should never fire. It exists to catch a future regression in the emission gate
// (a positive reward that escaped the EP check). It is a pure read-only function,
// also driven directly by unit tests. The rewards EndBlock panics when broken
// (fail-loud), the same gate+EndBlock pattern as WI-006's V-1.
func (k Keeper) RewardGateInvariant(ctx context.Context, epochID uint64) (string, bool) {
	var (
		msg    string
		broken bool
	)

	// Range over the (epochID, *) prefix of the RewardSnapshot map.
	rng := collections.NewPrefixedPairRange[uint64, []byte](epochID)
	iterErr := k.RewardSnapshot.Walk(ctx, rng, func(key collections.Pair[uint64, []byte], snap types.RewardSnapshot) (stop bool, err error) {
		// Only minted (positive) snapshots are gated by V-2; ineligible/zero ones
		// are exactly what EP=0 is supposed to produce.
		if !snap.Eligible || snap.Amount == "" {
			return false, nil
		}

		valAddr := sdk.ValAddress(key.K2())
		epState, found, gErr := k.participationKeeper.GetEP(ctx, valAddr)
		if gErr != nil {
			msg = fmt.Sprintf("rewards V-2: failed to read EP for %s (epoch %d): %v", valAddr.String(), epochID, gErr)
			broken = true
			return true, nil
		}
		if !found || epState.AccumulatedEp.IsNil() || !epState.AccumulatedEp.IsPositive() {
			msg = fmt.Sprintf("rewards V-2 broken: validator %s received a positive reward (%s) at epoch %d but EP is not positive", valAddr.String(), snap.Amount, epochID)
			broken = true
			return true, nil
		}
		return false, nil
	})
	if iterErr != nil && !broken {
		msg = fmt.Sprintf("rewards V-2: failed to scan reward snapshots for epoch %d: %v", epochID, iterErr)
		broken = true
	}

	return msg, broken
}
