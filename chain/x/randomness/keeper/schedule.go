package keeper

import (
	"context"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

// EndBlock advances the round state machine (spec §7–§9): it closes a round
// whose rotation height has been reached, schedules a round that has no
// heights yet, and opens the window one block before it starts.
//
// It always returns nil. An EndBlock error halts the chain, and this module
// awards nothing, so every failure is logged and retried at the next block (or,
// for a round that cannot be closed, left for the next block to close).
func (k Keeper) EndBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	h := sdkCtx.BlockHeight()
	r, err := k.CurrentRound.Get(ctx)
	if err != nil {
		sdkCtx.Logger().Error("randomness: no current round", "err", err)
		return nil
	}
	if r.RotationHeight != 0 && h >= r.RotationHeight {
		next, err := k.closeRound(ctx, r)
		if err != nil {
			sdkCtx.Logger().Error("randomness: round not closed; retried next block", "round", r.Number, "err", err)
			return nil
		}
		r = next
	}
	if r.RotationHeight == 0 {
		next, err := k.schedule(ctx, r, h)
		if err != nil {
			sdkCtx.Logger().Error("randomness: round not scheduled", "round", r.Number, "err", err)
			return nil
		}
		r = next
	}
	if r.Status == types.RoundStatus_ROUND_STATUS_SCHEDULED && r.RotationHeight != 0 &&
		h >= r.WindowOpenHeight-1 && h < r.RotationHeight {
		if err := k.openRound(ctx, r); err != nil {
			sdkCtx.Logger().Error("randomness: window not opened; the snapshot is retried next block",
				"round", r.Number, "err", err)
		}
	}
	return nil
}

// schedule fixes the heights of a round that has none (spec §7.2) from the
// epoch length and the parameters as they are now; they are never recomputed
// for this round. It returns r unchanged, with no error, when the epoch grid is
// off (epoch length 0, or the arithmetic overflows).
func (k Keeper) schedule(ctx context.Context, r types.Round, h int64) (types.Round, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return r, err
	}
	epochLen, err := k.pk.EpochLengthBlocks(ctx)
	if err != nil {
		return r, err
	}
	rotation, open, ok := types.ScheduleRotation(h, epochLen, params.RotationEpochs, params.WindowBlocks)
	if !ok {
		return r, nil
	}
	r.RotationHeight = rotation
	r.WindowOpenHeight = open
	if err := k.CurrentRound.Set(ctx, r); err != nil {
		return r, err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeRoundScheduled,
		sdk.NewAttribute(types.AttributeKeyRound, strconv.FormatUint(r.Number, 10)),
		sdk.NewAttribute(types.AttributeKeyKeyCutoffHeight, strconv.FormatInt(r.KeyCutoffHeight, 10)),
		sdk.NewAttribute(types.AttributeKeyWindowOpenHeight, strconv.FormatInt(r.WindowOpenHeight, 10)),
		sdk.NewAttribute(types.AttributeKeyRotationHeight, strconv.FormatInt(r.RotationHeight, 10)),
	))
	return r, nil
}
