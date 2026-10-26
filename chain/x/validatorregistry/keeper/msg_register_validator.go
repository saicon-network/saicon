package keeper

import (
	"context"
	"strconv"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// RegisterValidator onboards the signer as a validator: it creates
// ValidatorMetadata in STATUS_BONDING with the lock-up timer started.
//
// The validator operator identity is derived from the creator account bytes
// (acc and valoper share the same 20-byte key). v0.1 self-registration; real
// x/staking validator linkage is a later WI.
func (k msgServer) RegisterValidator(ctx context.Context, req *types.MsgRegisterValidator) (*types.MsgRegisterValidatorResponse, error) {
	accBytes, err := k.addressCodec.StringToBytes(req.Creator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid creator address")
	}
	valAddr := sdk.ValAddress(accBytes)

	exists, err := k.HasValidatorMetadata(ctx, valAddr)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errorsmod.Wrapf(types.ErrValidatorAlreadyRegistered, "validator %s", valAddr.String())
	}

	// A validator may commit to a LONGER lock-up than the chain minimum, never a
	// shorter one. This message is permissionless and the duration is
	// signer-supplied, so without a floor an operator could register with a
	// one-second lock-up and walk straight past V-3: IsLocked would be false, the
	// ante would wave the self-undelegate through, and the invariant would skip
	// the validator entirely. Zero keeps its meaning of "use the default".
	lockup := req.LockupDurationSeconds
	switch {
	case lockup == 0:
		lockup = types.DefaultLockupDurationSeconds
	case lockup < types.DefaultLockupDurationSeconds:
		return nil, errorsmod.Wrapf(types.ErrLockupTooShort,
			"requested %d seconds, minimum is %d", lockup, types.DefaultLockupDurationSeconds)
	case lockup > types.MaxLockupDurationSeconds:
		return nil, errorsmod.Wrapf(types.ErrLockupTooLong,
			"requested %d seconds, maximum is %d", lockup, types.MaxLockupDurationSeconds)
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)

	md := types.ValidatorMetadata{
		Status:                 types.Status_STATUS_BONDING,
		BondingStart:           sdkCtx.BlockTime(),
		BondingStartHeight:     types.LockStartHeight(sdkCtx.BlockHeight()),
		LockupDurationSeconds:  lockup,
		ComplianceFlag:         true,
		LastStatusChangeHeight: types.LockStartHeight(sdkCtx.BlockHeight()),
	}

	if err := k.SetValidatorMetadata(ctx, valAddr, md); err != nil {
		return nil, err
	}

	valAddrStr := valAddr.String()
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeStatusChanged,
		sdk.NewAttribute(types.AttributeKeyValidator, valAddrStr),
		sdk.NewAttribute(types.AttributeKeyFromStatus, "none"),
		sdk.NewAttribute(types.AttributeKeyToStatus, types.Status_STATUS_BONDING.HumanString()),
		sdk.NewAttribute(types.AttributeKeyHeight, strconv.FormatInt(sdkCtx.BlockHeight(), 10)),
	))

	return &types.MsgRegisterValidatorResponse{ValidatorAddress: valAddrStr}, nil
}
