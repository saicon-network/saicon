package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
	setypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

var _ types.QueryServer = queryServer{}

// NewQueryServerImpl returns an implementation of the QueryServer interface
// for the provided Keeper.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return queryServer{k}
}

type queryServer struct {
	k Keeper
}

func (q queryServer) Params(ctx context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryParamsResponse{Params: params}, nil
}

// Route previews MsgDelegateRouted. A route that cannot be made is a successful
// answer: reason says why and fits says how much would have fit.
func (q queryServer) Route(ctx context.Context, req *types.QueryRouteRequest) (*types.QueryRouteResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	delegator, err := q.k.addressCodec.StringToBytes(req.Delegator)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid delegator: %s", err)
	}
	primary, err := sdk.ValAddressFromBech32(req.Validator)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator: %s", err)
	}
	amount, ok := math.NewIntFromString(req.Amount)
	if !ok || !amount.IsPositive() {
		return nil, status.Errorf(codes.InvalidArgument, "amount must be a positive integer in nscn, got %q", req.Amount)
	}

	route, err := q.k.Plan(ctx, delegator, primary, amount)
	if err != nil {
		if errors.Is(err, types.ErrNoRoute) || errors.Is(err, types.ErrPrimaryNotEligible) ||
			errors.Is(err, setypes.ErrBelowMinDelegation) || errors.Is(err, setypes.ErrTooManyValidators) {
			return &types.QueryRouteResponse{Fits: route.Fits, Reason: err.Error()}, nil
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	res := &types.QueryRouteResponse{Fits: route.Fits}
	for _, l := range route.Direct {
		res.Legs = append(res.Legs, types.RouteLeg(l))
	}
	for _, l := range route.Queued {
		res.Queued = append(res.Queued, types.RouteLeg(l))
	}
	return res, nil
}
