package keeper

import (
	"context"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
)

// InitGenesis restores the module's parameters.
func (k Keeper) InitGenesis(ctx context.Context, gs types.GenesisState) error {
	return k.Params.Set(ctx, gs.Params)
}

// ExportGenesis returns the module's parameters.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.GenesisState{Params: params}, nil
}
