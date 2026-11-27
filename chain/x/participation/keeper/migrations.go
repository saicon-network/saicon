package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/participation/types"
)

// Migrator holds the participation module's store migrations.
type Migrator struct{ k Keeper }

// NewMigrator returns the migrator for a keeper.
func NewMigrator(k Keeper) Migrator { return Migrator{k: k} }

// Migrate1to2 brings in the WARP engine (S10, ruling R9): the four WARP
// parameters get the Register v1.0 defaults, and the parameters already stored
// are kept. No WARP rows are written: every validator starts from warp_initial
// at its first scored epoch after the upgrade, exactly as a new one would, and
// the snapshots written before it keep an absent warp.
func (m Migrator) Migrate1to2(ctx sdk.Context) error {
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("participation migration 1 to 2: reading params: %w", err)
	}
	params.WarpFall = types.DefaultWarpFall()
	params.WarpRise = types.DefaultWarpRise()
	params.WarpInitial = types.DefaultWarpInitial()
	params.WarpThreshold = types.DefaultWarpThreshold()
	if err := params.Validate(); err != nil {
		return fmt.Errorf("participation migration 1 to 2: params: %w", err)
	}
	return m.k.Params.Set(ctx, params)
}
