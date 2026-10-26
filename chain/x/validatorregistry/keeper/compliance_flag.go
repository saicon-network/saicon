package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// SetComplianceFlag updates the cached ComplianceFlag on a validator's
// metadata. It is the mirror target for x/compliance (invariant V-4.1:
// ValidatorMetadata.ComplianceFlag must always equal the compliance module's
// source-of-truth C flag).
//
// Returns ErrValidatorNotFound if the validator has no metadata.
func (k Keeper) SetComplianceFlag(ctx context.Context, addr sdk.ValAddress, compliant bool) error {
	md, found, err := k.GetValidatorMetadata(ctx, addr)
	if err != nil {
		return err
	}
	if !found {
		return errorsmod.Wrapf(types.ErrValidatorNotFound, "validator %s", addr.String())
	}

	md.ComplianceFlag = compliant
	return k.SetValidatorMetadata(ctx, addr, md)
}

// GetValidatorStatus returns the current status of a validator.
// Returns (status, true, nil) when present, (STATUS_UNSPECIFIED, false, nil)
// when absent. Exposed for cross-module consumers (e.g. x/compliance) that
// need the current status to decide a transition.
func (k Keeper) GetValidatorStatus(ctx context.Context, addr sdk.ValAddress) (types.Status, bool, error) {
	md, found, err := k.GetValidatorMetadata(ctx, addr)
	if err != nil {
		return types.Status_STATUS_UNSPECIFIED, false, err
	}
	if !found {
		return types.Status_STATUS_UNSPECIFIED, false, nil
	}
	return md.Status, true, nil
}
