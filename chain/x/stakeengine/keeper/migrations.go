package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// Migrator holds the stake engine's store migrations.
type Migrator struct{ k Keeper }

// NewMigrator returns the migrator for a keeper.
func NewMigrator(k Keeper) Migrator { return Migrator{k: k} }

// Migrate1to2 is the v0.10.0 upgrade (S8): the entity cap parameters get their
// defaults — the floor equal to the validator floor, which is what keeps the
// devnet's single validator able to take stake — and the stored cap snapshot
// gets its entity cap from the EWMA already stored, without advancing it.
func (m Migrator) Migrate1to2(ctx sdk.Context) error {
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("stakeengine migration 1 to 2: reading params: %w", err)
	}
	params.EntityCapFraction = math.LegacyNewDecWithPrec(5, 2)
	params.EntityCapFloor = params.ValidatorCapFloor
	if err := params.Validate(); err != nil {
		return fmt.Errorf("stakeengine migration 1 to 2: params: %w", err)
	}
	if err := m.k.Params.Set(ctx, params); err != nil {
		return err
	}
	st, err := m.k.CapState.Get(ctx)
	switch {
	case errors.Is(err, collections.ErrNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("stakeengine migration 1 to 2: reading the cap snapshot: %w", err)
	}
	st.EntityCap = types.EntityCap(params, st.EwmaBonded)
	return m.k.CapState.Set(ctx, st)
}
