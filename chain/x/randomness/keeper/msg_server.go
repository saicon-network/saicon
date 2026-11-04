package keeper

import (
	"context"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/saicon-network/saicon/chain/x/randomness/types"
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

// SubmitVrf submits the operator's VRF proof for the current round (spec §8).
func (k msgServer) SubmitVrf(ctx context.Context, msg *types.MsgSubmitVrf) (*types.MsgSubmitVrfResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, fmt.Sprintf("invalid operator address: %s", err))
	}
	output, err := k.Keeper.SubmitVrf(ctx, operator, msg.Round, msg.Proof)
	if err != nil {
		return nil, err
	}
	return &types.MsgSubmitVrfResponse{Output: output}, nil
}
