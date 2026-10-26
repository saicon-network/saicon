package keeper

import (
	"context"
	"fmt"
	"strings"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of the MsgServer interface
// for the provided Keeper.
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

// DelegateRouted plans the route and executes it in the message's state, so any
// error reverts every leg.
func (k msgServer) DelegateRouted(ctx context.Context, msg *types.MsgDelegateRouted) (*types.MsgDelegateRoutedResponse, error) {
	delegator, err := k.addressCodec.StringToBytes(msg.Delegator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	primary, err := sdk.ValAddressFromBech32(msg.Validator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	if msg.Amount.Denom != types.BondDenom {
		return nil, errorsmod.Wrapf(types.ErrInvalidDenom, "got %q, want %q", msg.Amount.Denom, types.BondDenom)
	}
	if msg.Amount.Amount.IsNil() || !msg.Amount.Amount.IsPositive() {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidCoins, "amount must be positive")
	}

	route, err := k.Plan(ctx, delegator, primary, msg.Amount.Amount)
	if err != nil {
		return nil, err
	}

	res := &types.MsgDelegateRoutedResponse{}
	var legs, queued []string
	for _, l := range route.Direct {
		addr, err := sdk.ValAddressFromBech32(l.Validator)
		if err != nil {
			return nil, err
		}
		validator, err := k.stakingKeeper.GetValidator(ctx, addr)
		if err != nil {
			return nil, errorsmod.Wrapf(err, "validator %s", l.Validator)
		}
		if _, err := k.stakingKeeper.Delegate(ctx, delegator, l.Amount, stakingtypes.Unbonded, validator, true); err != nil {
			return nil, errorsmod.Wrapf(err, "delegating %s nscn to %s", l.Amount, l.Validator)
		}
		res.Legs = append(res.Legs, types.RouteLeg(l))
		legs = append(legs, fmt.Sprintf("%s:%s", l.Validator, l.Amount))
	}
	for _, l := range route.Queued {
		addr, err := sdk.ValAddressFromBech32(l.Validator)
		if err != nil {
			return nil, err
		}
		id, err := k.stakeEngine.EnqueueRouted(ctx, delegator, addr, l.Amount)
		if err != nil {
			return nil, errorsmod.Wrapf(err, "queueing %s nscn for %s", l.Amount, l.Validator)
		}
		res.Queued = append(res.Queued, types.QueuedLeg{Validator: l.Validator, Amount: l.Amount, EntryId: id})
		queued = append(queued, fmt.Sprintf("%s:%s:%d", l.Validator, l.Amount, id))
	}

	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeDelegationRouted,
		sdk.NewAttribute(types.AttributeKeyDelegator, msg.Delegator),
		sdk.NewAttribute(types.AttributeKeyPrimary, msg.Validator),
		sdk.NewAttribute(types.AttributeKeyAmount, msg.Amount.Amount.String()),
		sdk.NewAttribute(types.AttributeKeyLegs, strings.Join(legs, ",")),
		sdk.NewAttribute(types.AttributeKeyQueued, strings.Join(queued, ",")),
	))
	return res, nil
}
