package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/participation/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// PSActiveOnlyInvariant implements invariant V-7
// (chain/docs/validators/README.md §8): a Participation Score snapshot for the
// given epoch may exist ONLY for a validator whose SAiCoN status is ACTIVE.
//
// ComputeEpoch enforces this by construction — it skips any bonded validator
// whose status is not ACTIVE (e.g. quarantined). This invariant is fail-loud
// defense-in-depth: if a snapshot for a non-active validator ever appears in the
// just-computed epoch, the participation EndBlocker panics rather than letting
// the bad state persist. (Operational ComputeEpoch errors remain swallowed to
// avoid halting on a transient failure; an invariant BREACH is different — it is
// a correctness violation and halts, mirroring V-1/V-2/V-3.)
//
// Returns (message, broken). broken == true indicates a violation.
func (k Keeper) PSActiveOnlyInvariant(ctx context.Context, epochID uint64) (string, bool) {
	var (
		broken bool
		count  int
		detail string
	)

	rng := collections.NewPrefixedPairRange[uint64, []byte](epochID)
	err := k.ParticipationScore.Walk(ctx, rng, func(key collections.Pair[uint64, []byte], _ types.ParticipationScoreSnapshot) (bool, error) {
		valAddr := sdk.ValAddress(key.K2())
		status, found, e := k.validatorregistryKeeper.GetValidatorStatus(ctx, valAddr)
		if e != nil {
			return false, e
		}
		if !found || status != vrtypes.Status_STATUS_ACTIVE {
			broken = true
			count++
			detail += fmt.Sprintf("  validator %s has a PS snapshot in epoch %d but is not ACTIVE\n", valAddr.String(), epochID)
		}
		return false, nil
	})
	if err != nil {
		return fmt.Sprintf("participation V-7 invariant: iteration error: %v", err), true
	}
	if broken {
		return fmt.Sprintf("participation V-7 invariant broken: %d non-active validator(s) with a PS snapshot in epoch %d\n%s", count, epochID, detail), true
	}
	return "participation V-7 invariant: all epoch PS snapshots belong to active validators", false
}
