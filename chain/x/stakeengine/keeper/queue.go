package keeper

import (
	"bytes"
	"context"
	"errors"
	"strconv"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func bond(amount math.Int) sdk.Coins { return sdk.NewCoins(sdk.NewCoin(types.BondDenom, amount)) }

func (k Keeper) queueLen(ctx context.Context) (uint64, error) {
	n, err := k.QueueLen.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return 0, nil
	}
	return n, err
}

// Enqueue moves amount from the delegator into the escrow and records an entry.
// There is one path for everyone: an entry that fits now is filled at the end
// of this same block.
func (k Keeper) Enqueue(ctx context.Context, delegator sdk.AccAddress, validator sdk.ValAddress, amount math.Int) (uint64, error) {
	return k.enqueue(ctx, delegator, validator, amount, true)
}

// EnqueueRouted is Enqueue for the delegation router: the same slot rule and
// escrow, without the min_queue_entry floor (the router enforces its own minimum).
func (k Keeper) EnqueueRouted(ctx context.Context, delegator sdk.AccAddress, validator sdk.ValAddress, amount math.Int) (uint64, error) {
	return k.enqueue(ctx, delegator, validator, amount, false)
}

func (k Keeper) enqueue(ctx context.Context, delegator sdk.AccAddress, validator sdk.ValAddress, amount math.Int, enforceMin bool) (uint64, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	if enforceMin && amount.LT(params.MinQueueEntry) {
		return 0, errorsmod.Wrapf(types.ErrQueueEntryTooSmall, "%s nscn, minimum %s", amount, params.MinQueueEntry)
	}
	n, err := k.queueLen(ctx)
	if err != nil {
		return 0, err
	}
	if n >= params.MaxQueueEntries {
		return 0, errorsmod.Wrapf(types.ErrQueueFull, "%d entries", n)
	}
	if _, err := k.stakingKeeper.GetValidator(ctx, validator); err != nil {
		return 0, errorsmod.Wrapf(err, "validator %s", validator)
	}
	delegatorStr, err := k.addressCodec.BytesToString(delegator)
	if err != nil {
		return 0, err
	}
	if !bytes.Equal(validator, delegator) {
		slots, err := k.DelegatorSlots(ctx, delegator)
		if err != nil {
			return 0, err
		}
		if err := types.CheckNewSlot(slots, validator.String(), params.MaxValidatorsPerDelegator); err != nil {
			return 0, err
		}
	}
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, delegator, types.ModuleName, bond(amount)); err != nil {
		return 0, err
	}
	id, err := k.QueueSeq.Next(ctx)
	if err != nil {
		return 0, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	entry := types.QueueEntry{
		Id:             id,
		Delegator:      delegatorStr,
		Validator:      validator.String(),
		Amount:         amount,
		EnqueuedHeight: sdkCtx.BlockHeight(),
	}
	if err := k.Queue.Set(ctx, id, entry); err != nil {
		return 0, err
	}
	if err := k.QueueLen.Set(ctx, n+1); err != nil {
		return 0, err
	}
	if err := k.adjustIndex(ctx, entry.Delegator, entry.Validator, 1); err != nil {
		return 0, err
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeStakeEnqueued,
		sdk.NewAttribute(types.AttributeKeyID, strconv.FormatUint(id, 10)),
		sdk.NewAttribute(types.AttributeKeyDelegator, entry.Delegator),
		sdk.NewAttribute(types.AttributeKeyValidator, entry.Validator),
		sdk.NewAttribute(types.AttributeKeyAmount, amount.String()),
	))
	return id, nil
}

// Cancel returns what remains of an entry to its delegator and removes it.
func (k Keeper) Cancel(ctx context.Context, delegator sdk.AccAddress, id uint64) (math.Int, error) {
	e, err := k.Queue.Get(ctx, id)
	if errors.Is(err, collections.ErrNotFound) {
		return math.Int{}, errorsmod.Wrapf(types.ErrQueueEntryNotFound, "id %d", id)
	}
	if err != nil {
		return math.Int{}, err
	}
	owner, err := k.addressCodec.StringToBytes(e.Delegator)
	if err != nil {
		return math.Int{}, err
	}
	if !bytes.Equal(owner, delegator) {
		return math.Int{}, errorsmod.Wrapf(types.ErrNotQueueOwner, "id %d", id)
	}
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, delegator, bond(e.Amount)); err != nil {
		return math.Int{}, err
	}
	if err := k.removeEntry(ctx, id); err != nil {
		return math.Int{}, err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeQueuedDelegationCancelled,
		sdk.NewAttribute(types.AttributeKeyID, strconv.FormatUint(id, 10)),
		sdk.NewAttribute(types.AttributeKeyDelegator, e.Delegator),
		sdk.NewAttribute(types.AttributeKeyAmount, e.Amount.String()),
	))
	return e.Amount, nil
}

// adjustIndex moves the (delegator, validator) entry count by delta and drops
// the row when it reaches zero. A count below zero is a bug and an error.
func (k Keeper) adjustIndex(ctx context.Context, delegator, validator string, delta int64) error {
	key := collections.Join(delegator, validator)
	cur, err := k.QueueByDelegator.Get(ctx, key)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if delta < 0 && cur < uint64(-delta) {
		return errors.New("stakeengine: queue index underflow")
	}
	next := uint64(int64(cur) + delta)
	if next == 0 {
		if err := k.QueueByDelegator.Remove(ctx, key); err != nil && !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		return nil
	}
	return k.QueueByDelegator.Set(ctx, key, next)
}

func (k Keeper) removeEntry(ctx context.Context, id uint64) error {
	e, err := k.Queue.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := k.Queue.Remove(ctx, id); err != nil {
		return err
	}
	if err := k.adjustIndex(ctx, e.Delegator, e.Validator, -1); err != nil {
		return err
	}
	n, err := k.queueLen(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("stakeengine: queue length underflow")
	}
	return k.QueueLen.Set(ctx, n-1)
}

// qualityAt is the queue's ordering key for the last closed epoch. A read that
// fails counts as "no data" — every node reads the same state, so the order
// stays deterministic.
func (k Keeper) qualityAt(ctx context.Context, epoch uint64) func(string) types.Quality {
	return func(validator string) types.Quality {
		var q types.Quality
		va, err := sdk.ValAddressFromBech32(validator)
		if err != nil {
			return q
		}
		if snap, found, err := k.participationKeeper.GetSnapshot(ctx, epoch, va); err == nil && found {
			q.Score = snap.ComputedScore
		}
		if ep, found, err := k.participationKeeper.GetEP(ctx, va); err == nil && found {
			q.EP = ep.AccumulatedEp
		}
		return q
	}
}

// FillQueue delegates queued stake while there is room under the global cap. It
// runs at the end of every block (spec §6.3).
func (k Keeper) FillQueue(ctx context.Context) error {
	headroom, err := k.Headroom(ctx)
	if err != nil {
		return err
	}
	if !headroom.IsPositive() {
		return nil
	}
	var entries []types.QueueEntry
	if err := k.Queue.Walk(ctx, nil, func(_ uint64, e types.QueueEntry) (bool, error) {
		entries = append(entries, e)
		return false, nil
	}); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	st, err := k.capState(ctx)
	if err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	tokens := map[string]math.Int{} // validator → tokens, updated as fills land
	active := map[string]bool{}
	var attempts uint64
	for _, e := range types.SortQueue(entries, k.qualityAt(ctx, st.Epoch)) {
		if attempts >= params.MaxFillsPerBlock || !headroom.IsPositive() {
			break
		}
		va, err := sdk.ValAddressFromBech32(e.Validator)
		if err != nil {
			return err
		}
		isActive, seen := active[e.Validator]
		if !seen {
			status, found, err := k.registryKeeper.GetValidatorStatus(ctx, va)
			if err != nil {
				return err
			}
			isActive = found && status == vrtypes.Status_STATUS_ACTIVE
			active[e.Validator] = isActive
		}
		if !isActive {
			continue // waits, keeps its place
		}
		tok, seen := tokens[e.Validator]
		if !seen {
			if tok, err = k.ValidatorTokens(ctx, va); err != nil {
				return err
			}
			tokens[e.Validator] = tok
		}
		room := st.ValidatorCap.Sub(tok)
		if !room.IsPositive() {
			continue // waits, keeps its place
		}
		fill := math.MinInt(e.Amount, math.MinInt(headroom, room))
		delegator, err := k.addressCodec.StringToBytes(e.Delegator)
		if err != nil {
			return err
		}

		attempts++
		// The whole fill — coins, delegation, the entry, the counter and the event —
		// is one unit in a cache context. Written only if all of it succeeded, so a
		// failure leaves the escrow and the queue as they were and V-10 intact.
		cacheCtx, write := sdkCtx.CacheContext()
		if err := k.fill(cacheCtx, e, delegator, va, fill); err != nil {
			sdkCtx.Logger().Error("stakeengine: queued delegation not filled; it stays queued",
				"id", e.Id, "amount", fill.String(), "err", err)
			continue
		}
		write()

		headroom = headroom.Sub(fill)
		tokens[e.Validator] = tok.Add(fill)
	}
	return nil
}

// fill delegates amount of entry e for its delegator and updates the queue to
// match: the entry, the counter and the event. Callers run it in a cache context.
func (k Keeper) fill(ctx sdk.Context, e types.QueueEntry, delegator sdk.AccAddress, va sdk.ValAddress, amount math.Int) error {
	if err := k.fillOne(ctx, delegator, va, amount); err != nil {
		return err
	}
	e.Amount = e.Amount.Sub(amount)
	var err error
	if e.Amount.IsZero() {
		err = k.removeEntry(ctx, e.Id)
	} else {
		err = k.Queue.Set(ctx, e.Id, e)
	}
	if err != nil {
		return err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeQueuedDelegationFilled,
		sdk.NewAttribute(types.AttributeKeyID, strconv.FormatUint(e.Id, 10)),
		sdk.NewAttribute(types.AttributeKeyDelegator, e.Delegator),
		sdk.NewAttribute(types.AttributeKeyValidator, e.Validator),
		sdk.NewAttribute(types.AttributeKeyAmount, amount.String()),
		sdk.NewAttribute(types.AttributeKeyRemaining, e.Amount.String()),
	))
	return nil
}

// fillOne is one fill: escrow → delegator, then a normal delegation from the
// delegator's account. It runs in a cache context, so a failure leaves nothing.
func (k Keeper) fillOne(ctx context.Context, delegator sdk.AccAddress, va sdk.ValAddress, amount math.Int) error {
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, delegator, bond(amount)); err != nil {
		return err
	}
	validator, err := k.stakingKeeper.GetValidator(ctx, va)
	if err != nil {
		return err
	}
	_, err = k.stakingKeeper.Delegate(ctx, delegator, amount, stakingtypes.Unbonded, validator, true)
	return err
}
