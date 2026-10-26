package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
)

// SetRewardSnapshot records the per-epoch emission decision for a validator,
// keyed by (epochID, operator address bytes).
func (k Keeper) SetRewardSnapshot(ctx context.Context, epochID uint64, addr sdk.ValAddress, snap types.RewardSnapshot) error {
	return k.RewardSnapshot.Set(ctx, collections.Join(epochID, addr.Bytes()), snap)
}

// GetRewardSnapshot returns the emission record for a validator at an epoch.
// Returns (snap, true, nil) when present, (zero, false, nil) when absent.
func (k Keeper) GetRewardSnapshot(ctx context.Context, epochID uint64, addr sdk.ValAddress) (types.RewardSnapshot, bool, error) {
	snap, err := k.RewardSnapshot.Get(ctx, collections.Join(epochID, addr.Bytes()))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.RewardSnapshot{}, false, nil
		}
		return types.RewardSnapshot{}, false, err
	}
	return snap, true, nil
}
