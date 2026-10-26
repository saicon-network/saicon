package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/saicon-network/saicon/chain/x/participation/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
//
// Everything past Params is restored verbatim. Without it an export/import cycle
// silently erased the module: score snapshots and accumulated EP both vanished,
// and since eligibility is ACTIVE and compliant and EP > 0 (V-2), every
// validator came back ineligible with nothing reporting why.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return err
	}

	for i, s := range genState.Scores {
		addr, err := valAddrOf(s.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("participation genesis: scores[%d] (%s): %w", i, s.ValidatorAddress, err)
		}
		if err := k.ParticipationScore.Set(ctx, collections.Join(s.EpochId, addr.Bytes()), s); err != nil {
			return fmt.Errorf("participation genesis: scores[%d] (%s): %w", i, s.ValidatorAddress, err)
		}
	}

	for i, e := range genState.EmpiricalPoints {
		addr, err := valAddrOf(e.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("participation genesis: empirical_points[%d] (%s): %w", i, e.ValidatorAddress, err)
		}
		if err := k.EmpiricalPoints.Set(ctx, addr.Bytes(), e); err != nil {
			return fmt.Errorf("participation genesis: empirical_points[%d] (%s): %w", i, e.ValidatorAddress, err)
		}
	}

	for i, c := range genState.EpochBlocksSigned {
		addr, err := valAddrOf(c.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("participation genesis: epoch_blocks_signed[%d] (%s): %w", i, c.ValidatorAddress, err)
		}
		if err := k.EpochBlocksSigned.Set(ctx, addr.Bytes(), c.Count); err != nil {
			return fmt.Errorf("participation genesis: epoch_blocks_signed[%d] (%s): %w", i, c.ValidatorAddress, err)
		}
	}

	for i, c := range genState.EpochBlocksMissed {
		addr, err := valAddrOf(c.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("participation genesis: epoch_blocks_missed[%d] (%s): %w", i, c.ValidatorAddress, err)
		}
		if err := k.EpochBlocksMissed.Set(ctx, addr.Bytes(), c.Count); err != nil {
			return fmt.Errorf("participation genesis: epoch_blocks_missed[%d] (%s): %w", i, c.ValidatorAddress, err)
		}
	}

	return nil
}

// ExportGenesis returns the module's exported genesis.
//
// Every collection is walked in store-key order, so two nodes exporting the same
// state produce byte-identical output — a requirement for genesis, which is
// consensus-critical state.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	genesis := types.DefaultGenesis()

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	genesis.Params = params

	if err := k.ParticipationScore.Walk(ctx, nil,
		func(_ collections.Pair[uint64, []byte], snap types.ParticipationScoreSnapshot) (bool, error) {
			genesis.Scores = append(genesis.Scores, snap)
			return false, nil
		}); err != nil {
		return nil, err
	}

	if err := k.EmpiricalPoints.Walk(ctx, nil,
		func(_ []byte, ep types.EmpiricalPointsState) (bool, error) {
			genesis.EmpiricalPoints = append(genesis.EmpiricalPoints, ep)
			return false, nil
		}); err != nil {
		return nil, err
	}

	// Two walks, not one merged record: the chain writes exactly one side per
	// block, so a validator with no misses has no entry on the missed side.
	// Emitting a zero for it would create a key on import that the exported
	// chain never had — which is precisely how this was caught, with the store
	// coming back larger than it went out.
	if err := k.EpochBlocksSigned.Walk(ctx, nil, func(addr []byte, n uint64) (bool, error) {
		genesis.EpochBlocksSigned = append(genesis.EpochBlocksSigned, types.EpochCount{
			ValidatorAddress: sdk.ValAddress(addr).String(),
			Count:            n,
		})
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.EpochBlocksMissed.Walk(ctx, nil, func(addr []byte, n uint64) (bool, error) {
		genesis.EpochBlocksMissed = append(genesis.EpochBlocksMissed, types.EpochCount{
			ValidatorAddress: sdk.ValAddress(addr).String(),
			Count:            n,
		})
		return false, nil
	}); err != nil {
		return nil, err
	}

	return genesis, nil
}

// valAddrOf decodes an address prefix-agnostically. A record exported by this
// module carries whatever prefix its codec produced, while a hand-written
// genesis may reasonably use the canonical valoper form; both resolve to the
// same bytes, and rejecting one would mean a genesis that passes
// `validate-genesis` and then fails to start.
func valAddrOf(s string) (sdk.ValAddress, error) {
	_, raw, err := bech32.DecodeAndConvert(s)
	if err != nil {
		return nil, err
	}
	return sdk.ValAddress(raw), nil
}
