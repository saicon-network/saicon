package keeper

import (
	"context"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func capsError(err error) error {
	if errors.Is(err, types.ErrCapsNotComputed) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

func (q queryServer) Caps(ctx context.Context, req *types.QueryCapsRequest) (*types.QueryCapsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	st, err := q.k.capState(ctx)
	if err != nil {
		return nil, capsError(err)
	}
	bonded, err := q.k.stakingKeeper.TotalBondedTokens(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryCapsResponse{CapState: st, TotalBonded: bonded, Headroom: st.GlobalCap.Sub(bonded)}, nil
}

func (q queryServer) ValidatorCap(ctx context.Context, req *types.QueryValidatorCapRequest) (*types.QueryValidatorCapResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	va, err := sdk.ValAddressFromBech32(req.ValidatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	vcap, err := q.k.ValidatorCap(ctx)
	if err != nil {
		return nil, capsError(err)
	}
	tokens, err := q.k.ValidatorTokens(ctx, va)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryValidatorCapResponse{Tokens: tokens, ValidatorCap: vcap, Room: vcap.Sub(tokens)}, nil
}
