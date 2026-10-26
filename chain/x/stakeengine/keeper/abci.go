package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// EndBlock recomputes the caps on an epoch boundary, fills the queue, then
// asserts V-10. A failed recompute leaves last epoch's caps in force and a
// failed fill leaves the queue for the next block — both are logged, neither
// halts. A broken V-10 halts, like V-1…V-9: coins the queue cannot account for
// are a bug, and continuing would compound it.
func (k Keeper) EndBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if err := k.MaybeRecomputeCaps(ctx, sdkCtx.BlockHeight()); err != nil {
		sdkCtx.Logger().Error("stakeengine: caps not recomputed; the previous epoch's stay in force", "err", err)
	}
	if err := k.FillQueue(ctx); err != nil {
		sdkCtx.Logger().Error("stakeengine: queue not filled this block", "err", err)
	}
	if msg, broken := k.EscrowInvariant(ctx); broken {
		panic(msg)
	}
	return nil
}
