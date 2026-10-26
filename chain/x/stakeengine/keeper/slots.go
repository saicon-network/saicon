package keeper

import (
	"bytes"
	"context"
	"sort"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// DelegatorSlots lists the distinct validators delegator holds a delegation to
// or a queue entry for, sorted by bech32 string. The operator's self-delegation
// to its own validator is admission stake, not a delegation choice, and is left
// out.
func (k Keeper) DelegatorSlots(ctx context.Context, delegator sdk.AccAddress) ([]string, error) {
	seen := map[string]bool{}
	// The fetch is bounded by the parameter's UPPER BOUND, not its current value:
	// governance may lower the parameter below what a delegator already holds,
	// and a list truncated at the lowered value would make a top-up to a held
	// validator look like a new slot. The rule holds from genesis and the
	// parameter never exceeds the bound, so nobody holds more than the bound
	// (+1 for the self-bond, +1 to see "one too many").
	dels, err := k.stakingKeeper.GetDelegatorDelegations(ctx, delegator, uint16(types.MaxValidatorsPerDelegatorBound)+2)
	if err != nil {
		return nil, err
	}
	for _, d := range dels {
		va, err := sdk.ValAddressFromBech32(d.ValidatorAddress)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(va, delegator) {
			continue
		}
		seen[d.ValidatorAddress] = true
	}
	delStr, err := k.addressCodec.BytesToString(delegator)
	if err != nil {
		return nil, err
	}
	self := sdk.ValAddress(delegator).String()
	rng := collections.NewPrefixedPairRange[string, string](delStr)
	if err := k.QueueByDelegator.Walk(ctx, rng, func(key collections.Pair[string, string], n uint64) (bool, error) {
		if n > 0 && key.K2() != self {
			seen[key.K2()] = true
		}
		return false, nil
	}); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}

func (k Keeper) MaxValidatorsPerDelegator(ctx context.Context) (uint32, error) {
	p, err := k.Params.Get(ctx)
	return p.MaxValidatorsPerDelegator, err
}

func (k Keeper) MinDelegation(ctx context.Context) (math.Int, error) {
	p, err := k.Params.Get(ctx)
	return p.MinDelegation, err
}

// LatestEpoch is the epoch of the stored caps snapshot — the last closed epoch,
// whose participation snapshots the router reads.
func (k Keeper) LatestEpoch(ctx context.Context) (uint64, error) {
	st, err := k.capState(ctx)
	return st.Epoch, err
}
