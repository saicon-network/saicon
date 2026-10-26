package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/participation/types"
)

// SetSnapshot persists the per-epoch participation snapshot for a validator,
// keyed by (epoch, operator address bytes).
func (k Keeper) SetSnapshot(ctx context.Context, epoch uint64, addr sdk.ValAddress, snap types.ParticipationScoreSnapshot) error {
	return k.ParticipationScore.Set(ctx, collections.Join(epoch, addr.Bytes()), snap)
}

// GetSnapshot returns the participation snapshot for (epoch, validator).
// Returns (snap, true, nil) when present, (zero, false, nil) when absent.
func (k Keeper) GetSnapshot(ctx context.Context, epoch uint64, addr sdk.ValAddress) (types.ParticipationScoreSnapshot, bool, error) {
	snap, err := k.ParticipationScore.Get(ctx, collections.Join(epoch, addr.Bytes()))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.ParticipationScoreSnapshot{}, false, nil
		}
		return types.ParticipationScoreSnapshot{}, false, err
	}
	return snap, true, nil
}

// SetEP persists the accumulated empirical-points state for a validator,
// keyed by operator address bytes.
func (k Keeper) SetEP(ctx context.Context, addr sdk.ValAddress, ep types.EmpiricalPointsState) error {
	return k.EmpiricalPoints.Set(ctx, addr.Bytes(), ep)
}

// EpochLengthBlocks returns Params.EpochLengthBlocks. It is a thin getter so
// dependent modules (x/rewards, WI-016) can read the single source of truth for
// the epoch length without redeclaring the param, avoiding epoch-boundary drift.
func (k Keeper) EpochLengthBlocks(ctx context.Context) (uint64, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	return p.EpochLengthBlocks, nil
}

// GetEP returns the empirical-points state for a validator.
// Returns (ep, true, nil) when present, (zero, false, nil) when absent.
func (k Keeper) GetEP(ctx context.Context, addr sdk.ValAddress) (types.EmpiricalPointsState, bool, error) {
	ep, err := k.EmpiricalPoints.Get(ctx, addr.Bytes())
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.EmpiricalPointsState{}, false, nil
		}
		return types.EmpiricalPointsState{}, false, err
	}
	return ep, true, nil
}
