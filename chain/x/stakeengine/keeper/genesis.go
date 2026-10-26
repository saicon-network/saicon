package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// InitGenesis restores the module's state. A fresh genesis carries no cap
// state: it is computed here — after x/staking and x/genutil, so the genesis
// validators are bonded — and the caps exist from block 1 rather than from the
// first epoch close.
func (k Keeper) InitGenesis(ctx context.Context, gs types.GenesisState) error {
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}
	for _, e := range gs.Queue {
		if err := k.Queue.Set(ctx, e.Id, e); err != nil {
			return fmt.Errorf("stakeengine genesis: queue entry %d: %w", e.Id, err)
		}
		// The slot index is derived state; it has no genesis field.
		if err := k.adjustIndex(ctx, e.Delegator, e.Validator, 1); err != nil {
			return fmt.Errorf("stakeengine genesis: queue index for entry %d: %w", e.Id, err)
		}
	}
	if err := k.QueueLen.Set(ctx, uint64(len(gs.Queue))); err != nil {
		return err
	}
	if err := k.QueueSeq.Set(ctx, gs.NextQueueId); err != nil {
		return err
	}
	if gs.CapState != nil {
		if err := k.CapState.Set(ctx, *gs.CapState); err != nil {
			return err
		}
	} else if _, err := k.RecomputeCaps(ctx, 0, true); err != nil {
		return fmt.Errorf("stakeengine genesis: computing the first caps: %w", err)
	}
	if msg, broken := k.EscrowInvariant(ctx); broken {
		return errors.New(msg)
	}
	return nil
}

// ExportGenesis returns the module's state. The queue is walked in id order, so
// two nodes export byte-identical genesis.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	gs := types.DefaultGenesis()
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.Params = params

	st, err := k.CapState.Get(ctx)
	switch {
	case err == nil:
		gs.CapState = &st
	case !errors.Is(err, collections.ErrNotFound):
		return nil, err
	}

	if err := k.Queue.Walk(ctx, nil, func(_ uint64, e types.QueueEntry) (bool, error) {
		gs.Queue = append(gs.Queue, e)
		return false, nil
	}); err != nil {
		return nil, err
	}
	next, err := k.QueueSeq.Peek(ctx)
	if err != nil {
		return nil, err
	}
	gs.NextQueueId = next
	return gs, nil
}
