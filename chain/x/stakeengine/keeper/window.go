package keeper

import (
	"context"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// The backstop sleeps through BeginBlock (S7 spec §6.4). Slashing a redelegation
// calls x/staking's Unbond on the destination, so the delegation hooks fire
// inside a slash; a self-bond slashed below the floor or inside its lock-up
// would otherwise make a hook return an error out of BeginBlock and halt the
// chain. No user message runs in BeginBlock: transactions come after it and
// x/gov executes proposals in EndBlock. PreBlock opens the window; this
// module's BeginBlocker, ordered last, closes it.

func (k Keeper) OpenBeginBlockWindow(ctx context.Context) error {
	return k.transientService.OpenTransientStore(ctx).Set(types.BeginBlockWindow, []byte{1})
}

func (k Keeper) CloseBeginBlockWindow(ctx context.Context) error {
	return k.transientService.OpenTransientStore(ctx).Delete(types.BeginBlockWindow)
}

func (k Keeper) inBeginBlock(ctx context.Context) (bool, error) {
	return k.transientService.OpenTransientStore(ctx).Has(types.BeginBlockWindow)
}
