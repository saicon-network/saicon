package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
//
// Everything past Params is restored verbatim. Without it an export/import cycle
// erased the payment record — the only audit trail of what the chain minted and
// to whom — along with two accumulators that decide the next mint: the carried
// remainder (ADR-0011 D5) and the gas fees already observed in the epoch in
// progress (D2). Losing the second makes the chain mint more than the security
// budget allows for that epoch.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	// The 21M ceiling binds the initial allocation too, not only what the chain
	// mints later. mintHeadroom clamps a negative headroom to zero, so a genesis
	// funded above the ceiling would start and simply never pay — the excess
	// would stand. x/bank initialises before this module (genesis order in
	// app_config.go), so its supply is the one the chain starts with.
	if supply := k.bankKeeper.GetSupply(ctx, types.RewardDenom).Amount; supply.GT(types.MaxSupplyNscn()) {
		return fmt.Errorf("rewards genesis: initial %s supply %s exceeds the maximum supply %s",
			types.RewardDenom, supply, types.MaxSupplyNscn())
	}
	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return err
	}

	for i, s := range genState.Snapshots {
		_, raw, err := bech32.DecodeAndConvert(s.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("rewards genesis: snapshots[%d] (%s): %w", i, s.ValidatorAddress, err)
		}
		if err := k.RewardSnapshot.Set(ctx, collections.Join(s.EpochId, sdk.ValAddress(raw).Bytes()), s); err != nil {
			return fmt.Errorf("rewards genesis: snapshots[%d] (%s): %w", i, s.ValidatorAddress, err)
		}
	}

	if err := k.Remainder.Set(ctx, genState.Remainder); err != nil {
		return err
	}
	return k.ObservedGasFees.Set(ctx, genState.ObservedGasFees)
}

// ExportGenesis returns the module's exported genesis.
//
// Snapshots are walked in store-key order, so two nodes exporting the same state
// produce byte-identical output — a requirement for genesis, which is
// consensus-critical state.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	genesis := types.DefaultGenesis()

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	genesis.Params = params

	if err := k.RewardSnapshot.Walk(ctx, nil,
		func(_ collections.Pair[uint64, []byte], snap types.RewardSnapshot) (bool, error) {
			genesis.Snapshots = append(genesis.Snapshots, snap)
			return false, nil
		}); err != nil {
		return nil, err
	}

	// Both accumulators are Items that may legitimately have never been written
	// — a chain exported before its first epoch boundary has neither. Absent
	// means zero here, which is what DefaultGenesis already put in place.
	if remainder, err := k.Remainder.Get(ctx); err == nil {
		genesis.Remainder = remainder
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	if fees, err := k.ObservedGasFees.Get(ctx); err == nil {
		genesis.ObservedGasFees = fees
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}

	return genesis, nil
}
