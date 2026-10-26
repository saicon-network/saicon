package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// SetValidatorMetadata stores or replaces validator metadata for the given
// validator address. The metadata.ValidatorAddress field is overwritten with
// the bech32 form of addr for consistency.
//
// Returns error if the metadata fails basic validation (status range, etc.).
func (k Keeper) SetValidatorMetadata(
	ctx context.Context,
	addr sdk.ValAddress,
	metadata types.ValidatorMetadata,
) error {
	if addr.Empty() {
		return errors.New("validator address is empty")
	}
	if !metadata.Status.IsValid() {
		return errors.New("validator metadata: invalid status (must not be STATUS_UNSPECIFIED)")
	}

	// Normalize bech32 form of validator address into the stored value.
	addrStr, err := k.addressCodec.BytesToString(addr)
	if err != nil {
		return err
	}
	metadata.ValidatorAddress = addrStr

	return k.ValidatorMetadata.Set(ctx, addr.Bytes(), metadata)
}

// GetValidatorMetadata returns the metadata stored for addr.
// Returns (metadata, true, nil) when present, (zero, false, nil) when absent,
// and (zero, false, err) on storage errors.
func (k Keeper) GetValidatorMetadata(
	ctx context.Context,
	addr sdk.ValAddress,
) (types.ValidatorMetadata, bool, error) {
	if addr.Empty() {
		return types.ValidatorMetadata{}, false, errors.New("validator address is empty")
	}

	md, err := k.ValidatorMetadata.Get(ctx, addr.Bytes())
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.ValidatorMetadata{}, false, nil
		}
		return types.ValidatorMetadata{}, false, err
	}
	return md, true, nil
}

// HasValidatorMetadata reports whether metadata exists for addr.
func (k Keeper) HasValidatorMetadata(ctx context.Context, addr sdk.ValAddress) (bool, error) {
	if addr.Empty() {
		return false, errors.New("validator address is empty")
	}
	return k.ValidatorMetadata.Has(ctx, addr.Bytes())
}

// RemoveValidatorMetadata deletes the metadata for addr.
// No-op if no record exists.
func (k Keeper) RemoveValidatorMetadata(ctx context.Context, addr sdk.ValAddress) error {
	if addr.Empty() {
		return errors.New("validator address is empty")
	}
	return k.ValidatorMetadata.Remove(ctx, addr.Bytes())
}

// IterateValidatorMetadata invokes cb for every stored ValidatorMetadata entry,
// in unspecified order. The iteration stops if cb returns true.
//
// Returns the first error encountered (either from storage or propagated from cb).
func (k Keeper) IterateValidatorMetadata(
	ctx context.Context,
	cb func(addr sdk.ValAddress, metadata types.ValidatorMetadata) (stop bool),
) error {
	iter, err := k.ValidatorMetadata.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = iter.Close() }()

	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return err
		}
		value, err := iter.Value()
		if err != nil {
			return err
		}
		if cb(sdk.ValAddress(key), value) {
			return nil
		}
	}
	return nil
}
