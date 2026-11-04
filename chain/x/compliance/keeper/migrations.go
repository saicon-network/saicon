package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

// Migrator holds the compliance module's store migrations.
type Migrator struct{ k Keeper }

// NewMigrator returns the migrator for a keeper.
func NewMigrator(k Keeper) Migrator { return Migrator{k: k} }

// Migrate1to2 is the v0.10.0 upgrade (S8 W3): the unjail cooldown is kept, the
// coordinated-attack fraction and the appeal window get their defaults, and the
// sanction sequence starts at 1 (a v0.9.1 store has none). It reads no other
// module and runs before applyV0100 (RunMigrations is the handler's first
// step), so aligning the fraction with a raised double-sign fraction is the
// handler's, after it (spec §14). Nothing is written unless the migrated
// parameters validate.
func (m Migrator) Migrate1to2(ctx sdk.Context) error {
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("compliance migration 1 to 2: reading params: %w", err)
	}
	params.CoordinatedAttackFraction = types.DefaultCoordinatedAttackFraction()
	params.AppealWindowSeconds = types.DefaultAppealWindowSeconds
	if err := params.Validate(); err != nil {
		return fmt.Errorf("compliance migration 1 to 2: params: %w", err)
	}
	if err := m.k.Params.Set(ctx, params); err != nil {
		return fmt.Errorf("compliance migration 1 to 2: writing params: %w", err)
	}
	if err := m.k.SanctionSeq.Set(ctx, 1); err != nil {
		return fmt.Errorf("compliance migration 1 to 2: starting the sanction sequence: %w", err)
	}
	return nil
}
