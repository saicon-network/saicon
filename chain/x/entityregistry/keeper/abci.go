package keeper

import (
	"bytes"
	"context"
	"errors"
	"sort"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// EndBlock promotes held validators that may now activate, sweeps frozen seats
// that no longer hold an active seat, and asserts V-12. The first two steps log
// their errors and carry on: a validator that cannot be promoted this block
// stays held and is tried again. A broken V-12 panics and halts the chain.
func (k Keeper) EndBlock(ctx context.Context) error {
	logger := sdk.UnwrapSDKContext(ctx).Logger()
	if err := k.releaseHeld(ctx); err != nil {
		logger.Error("entityregistry: promoting held validators failed", "err", err)
	}
	// The sweep writes two collections: written only if it all succeeds.
	cacheCtx, write := sdk.UnwrapSDKContext(ctx).CacheContext()
	if err := k.sweepFrozen(cacheCtx); err != nil {
		logger.Error("entityregistry: sweeping frozen seats failed", "err", err)
	} else {
		write()
	}
	if msg, broken := k.RegistryInvariant(ctx); broken {
		panic(msg)
	}
	return nil
}

// releaseHeld promotes held validators that may now activate (spec §7.3): bonded
// in x/staking, BONDING or UNBONDING in the registry, attested, with a free
// seat. Tokens descending, then operator bytes, decides who takes a seat first.
func (k Keeper) releaseHeld(ctx context.Context) error {
	type held struct {
		addr   sdk.ValAddress
		tokens math.Int
	}
	var list []held
	var drop []sdk.ValAddress
	err := k.Pending.Walk(ctx, nil, func(key []byte) (bool, error) {
		v := sdk.ValAddress(key)
		sv, err := k.stakingKeeper.GetValidator(ctx, v)
		if errors.Is(err, stakingtypes.ErrNoValidatorFound) || (err == nil && !sv.IsBonded()) {
			drop = append(drop, v)
			return false, nil
		}
		if err != nil {
			return true, err
		}
		st, ok, err := k.registryKeeper.GetValidatorStatus(ctx, v)
		if err != nil {
			return true, err
		}
		if !ok || (st != vrtypes.Status_STATUS_BONDING && st != vrtypes.Status_STATUS_UNBONDING) {
			drop = append(drop, v)
			return false, nil
		}
		list = append(list, held{v, sv.GetTokens()})
		return false, nil
	})
	if err != nil {
		return err
	}
	// Collected first, removed after: never mutate a collection while walking it.
	for _, v := range drop {
		if err := k.Pending.Remove(ctx, v); err != nil {
			return err
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].tokens.Equal(list[j].tokens) {
			return list[i].tokens.GT(list[j].tokens)
		}
		return bytes.Compare(list[i].addr, list[j].addr) < 0
	})
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	for _, h := range list {
		reason, entityID, err := k.activationBlock(ctx, h.addr)
		if err != nil {
			return err
		}
		if reason != "" {
			continue
		}
		cacheCtx, write := sdkCtx.CacheContext()
		if err := k.registryKeeper.ActivateOnBond(cacheCtx, h.addr); err != nil {
			sdkCtx.Logger().Error("entityregistry: held validator not promoted; it stays held",
				"validator", h.addr.String(), "err", err)
			continue
		}
		write()
		// The registry may have resumed it to QUARANTINED; either way it is no longer held.
		if err := k.Pending.Remove(ctx, h.addr); err != nil {
			return err
		}
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
			types.EventTypeActivationReleased,
			sdk.NewAttribute(types.AttributeKeyValidator, h.addr.String()),
			sdk.NewAttribute(types.AttributeKeyEntityID, entityID),
		))
	}
	return nil
}

// sweepFrozen deletes every frozen seat whose validator is no longer ACTIVE: it
// cannot return to ACTIVE without an attestation, so the row has done its job.
// The seat's charge on its former entity ends at the current height (spec §6.2).
func (k Keeper) sweepFrozen(ctx context.Context) error {
	var gone []sdk.ValAddress
	err := k.Frozen.Walk(ctx, nil, func(key []byte, _ types.FrozenSeat) (bool, error) {
		st, ok, err := k.registryKeeper.GetValidatorStatus(ctx, sdk.ValAddress(key))
		if err != nil {
			return true, err
		}
		if !ok || st != vrtypes.Status_STATUS_ACTIVE {
			gone = append(gone, sdk.ValAddress(key))
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	for _, v := range gone {
		if err := k.endCharge(ctx, v, height); err != nil {
			return err
		}
		if err := k.Frozen.Remove(ctx, v); err != nil {
			return err
		}
	}
	return nil
}
