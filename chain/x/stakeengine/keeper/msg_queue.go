package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func (k msgServer) EnqueueDelegation(ctx context.Context, msg *types.MsgEnqueueDelegation) (*types.MsgEnqueueDelegationResponse, error) {
	delegator, err := k.addressCodec.StringToBytes(msg.Delegator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	validator, err := sdk.ValAddressFromBech32(msg.Validator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	if msg.Amount.Denom != types.BondDenom {
		return nil, errorsmod.Wrapf(types.ErrInvalidDenom, "got %q, want %q", msg.Amount.Denom, types.BondDenom)
	}
	if msg.Amount.Amount.IsNil() || !msg.Amount.Amount.IsPositive() {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidCoins, "amount must be positive")
	}
	id, err := k.Enqueue(ctx, delegator, validator, msg.Amount.Amount)
	if err != nil {
		return nil, err
	}
	return &types.MsgEnqueueDelegationResponse{Id: id}, nil
}

func (k msgServer) CancelQueuedDelegation(ctx context.Context, msg *types.MsgCancelQueuedDelegation) (*types.MsgCancelQueuedDelegationResponse, error) {
	delegator, err := k.addressCodec.StringToBytes(msg.Delegator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	returned, err := k.Cancel(ctx, delegator, msg.Id)
	if err != nil {
		return nil, err
	}
	return &types.MsgCancelQueuedDelegationResponse{Returned: sdk.NewCoin(types.BondDenom, returned)}, nil
}
