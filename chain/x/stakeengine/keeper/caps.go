package keeper

import (
	"context"
	"errors"
	"fmt"
	gomath "math"
	"strconv"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	rewardstypes "github.com/saicon-network/saicon/chain/x/rewards/types"
	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// PublicCirculating computes ADR-0012's Option B over ownership, not custody:
//
//	totalSupply − balances of NonCirculatingModules
//	            − everything excluded addresses own: liquid + bonded + unbonding + queued
//
// The bonded pool, the not-bonded pool and the escrow are never subtracted —
// their tokens are publicly owned. IBC escrow is not subtracted either: a known
// gap until MainNet (the devnet has no IBC channels).
func (k Keeper) PublicCirculating(ctx context.Context) (math.Int, error) {
	supply := k.bankKeeper.GetSupply(ctx, types.BondDenom).Amount
	var held []math.Int
	for _, name := range types.NonCirculatingModules {
		if addr := k.authKeeper.GetModuleAddress(name); addr != nil {
			held = append(held, k.bankKeeper.GetBalance(ctx, addr, types.BondDenom).Amount)
		}
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return math.Int{}, err
	}
	excluded := make(map[string]struct{}, len(params.ExcludedAddresses))
	for _, a := range params.ExcludedAddresses {
		bz, err := types.DecodeAccount(a)
		if err != nil {
			return math.Int{}, err
		}
		excluded[string(bz)] = struct{}{}
		h, err := k.holdingsOf(ctx, sdk.AccAddress(bz))
		if err != nil {
			return math.Int{}, err
		}
		held = append(held, h)
	}
	if len(excluded) > 0 {
		// One pass over the queue for all excluded addresses, not one per address.
		queued := math.ZeroInt()
		err = k.Queue.Walk(ctx, nil, func(_ uint64, e types.QueueEntry) (bool, error) {
			owner, err := k.addressCodec.StringToBytes(e.Delegator)
			if err != nil {
				return true, fmt.Errorf("queue entry %d: %w", e.Id, err)
			}
			if _, ok := excluded[string(owner)]; ok {
				queued = queued.Add(e.Amount)
			}
			return false, nil
		})
		if err != nil {
			return math.Int{}, err
		}
		held = append(held, queued)
	}
	return types.PublicCirculating(supply, held...), nil
}

// holdingsOf is what addr owns in the bond denom outside the queue: liquid,
// delegated and unbonding. Queued stake is summed once for all excluded
// addresses in PublicCirculating.
func (k Keeper) holdingsOf(ctx context.Context, addr sdk.AccAddress) (math.Int, error) {
	total := k.bankKeeper.GetBalance(ctx, addr, types.BondDenom).Amount

	dels, err := k.stakingKeeper.GetDelegatorDelegations(ctx, addr, gomath.MaxUint16)
	if err != nil {
		return math.Int{}, err
	}
	for _, d := range dels {
		va, err := sdk.ValAddressFromBech32(d.ValidatorAddress)
		if err != nil {
			return math.Int{}, err
		}
		v, err := k.stakingKeeper.GetValidator(ctx, va)
		if err != nil {
			return math.Int{}, err
		}
		total = total.Add(v.TokensFromShares(d.Shares).TruncateInt())
	}

	ubds, err := k.stakingKeeper.GetUnbondingDelegations(ctx, addr, gomath.MaxUint16)
	if err != nil {
		return math.Int{}, err
	}
	for _, u := range ubds {
		for _, e := range u.Entries {
			total = total.Add(e.Balance)
		}
	}

	return total, nil
}

// RecomputeCaps computes and stores the snapshot for epoch. first starts the EWMA
// from the current total bonded (genesis); otherwise it advances the stored one.
func (k Keeper) RecomputeCaps(ctx context.Context, epoch uint64, first bool) (types.CapState, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.CapState{}, err
	}
	pc, err := k.PublicCirculating(ctx)
	if err != nil {
		return types.CapState{}, err
	}
	bonded, err := k.stakingKeeper.TotalBondedTokens(ctx)
	if err != nil {
		return types.CapState{}, err
	}
	var prev math.LegacyDec
	if !first {
		old, err := k.CapState.Get(ctx)
		switch {
		case err == nil:
			prev = old.EwmaBonded
		case errors.Is(err, collections.ErrNotFound):
			first = true
		default:
			return types.CapState{}, err
		}
	}
	ewma := types.NextEWMA(prev, bonded, params.EwmaWindowEpochs, first)
	st := types.CapState{
		Epoch:             epoch,
		PublicCirculating: pc,
		GlobalCap:         types.GlobalCap(params, rewardstypes.MaxSupplyNscn(), pc),
		EwmaBonded:        ewma,
		ValidatorCap:      types.ValidatorCap(params, ewma),
	}
	if err := k.CapState.Set(ctx, st); err != nil {
		return types.CapState{}, err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeStakeCapsComputed,
		sdk.NewAttribute(types.AttributeKeyEpoch, strconv.FormatUint(epoch, 10)),
		sdk.NewAttribute(types.AttributeKeyPublicCirculating, pc.String()),
		sdk.NewAttribute(types.AttributeKeyGlobalCap, st.GlobalCap.String()),
		sdk.NewAttribute(types.AttributeKeyValidatorCap, st.ValidatorCap.String()),
	))
	return st, nil
}

// MaybeRecomputeCaps runs RecomputeCaps on x/participation's epoch boundary —
// h % epochLen == 0 and h > 1, epoch h/epochLen — and does nothing otherwise.
func (k Keeper) MaybeRecomputeCaps(ctx context.Context, height int64) error {
	epochLen, err := k.participationKeeper.EpochLengthBlocks(ctx)
	if err != nil {
		return err
	}
	if epochLen == 0 || height <= 1 || height%int64(epochLen) != 0 {
		return nil
	}
	_, err = k.RecomputeCaps(ctx, uint64(height)/epochLen, false)
	return err
}

func (k Keeper) capState(ctx context.Context) (types.CapState, error) {
	st, err := k.CapState.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return types.CapState{}, types.ErrCapsNotComputed
	}
	return st, err
}

// Headroom is the global cap minus what is bonded right now. It may be negative:
// the cap is recomputed per epoch and can fall below existing stake, which is
// frozen rather than shed (ADR-0012).
func (k Keeper) Headroom(ctx context.Context) (math.Int, error) {
	st, err := k.capState(ctx)
	if err != nil {
		return math.Int{}, err
	}
	bonded, err := k.stakingKeeper.TotalBondedTokens(ctx)
	if err != nil {
		return math.Int{}, err
	}
	return st.GlobalCap.Sub(bonded), nil
}

// ValidatorCap is this epoch's per-validator cap.
func (k Keeper) ValidatorCap(ctx context.Context) (math.Int, error) {
	st, err := k.capState(ctx)
	if err != nil {
		return math.Int{}, err
	}
	return st.ValidatorCap, nil
}

// ValidatorTokens is a validator's total tokens whatever its status (bonded,
// unbonding or unbonded), zero if it does not exist.
func (k Keeper) ValidatorTokens(ctx context.Context, v sdk.ValAddress) (math.Int, error) {
	validator, err := k.stakingKeeper.GetValidator(ctx, v)
	if errors.Is(err, stakingtypes.ErrNoValidatorFound) {
		return math.ZeroInt(), nil
	}
	if err != nil {
		return math.Int{}, err
	}
	return validator.GetTokens(), nil
}

// SelfBond is what the operator itself has delegated to its validator, in tokens.
func (k Keeper) SelfBond(ctx context.Context, v sdk.ValAddress) (math.Int, error) {
	del, err := k.stakingKeeper.GetDelegation(ctx, sdk.AccAddress(v), v)
	if errors.Is(err, stakingtypes.ErrNoDelegation) {
		return math.ZeroInt(), nil
	}
	if err != nil {
		return math.Int{}, err
	}
	validator, err := k.stakingKeeper.GetValidator(ctx, v)
	if err != nil {
		return math.Int{}, fmt.Errorf("self-bond of %s: %w", v, err)
	}
	return validator.TokensFromShares(del.Shares).TruncateInt(), nil
}
