package keeper

import (
	"context"

	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/participation/types"
)

// warpRecord renders a stored row for the queries.
func warpRecord(w types.WarpState, p types.WarpParams) types.WarpRecord {
	return types.WarpRecord{
		ValidatorAddress: w.ValidatorAddress,
		Value:            w.Value,
		LastEpoch:        w.LastEpoch,
		Eligible:         !w.Value.IsNil() && !p.Threshold.IsNil() && types.WarpEligible(w.Value, p.Threshold),
	}
}

// Warp returns a validator's WARP and the parameters it is judged by.
func (q queryServer) Warp(ctx context.Context, req *types.QueryWarpRequest) (*types.QueryWarpResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator address: %v", err)
	}

	params, err := q.k.WarpParams(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	w, found, err := q.k.GetWarp(ctx, valAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if !found {
		return nil, status.Errorf(codes.NotFound, "no WARP for validator %s", req.ValidatorAddress)
	}

	return &types.QueryWarpResponse{Warp: warpRecord(w, params), Params: params}, nil
}

// Warps lists every stored WARP, paginated, in operator address byte order.
func (q queryServer) Warps(ctx context.Context, req *types.QueryWarpsRequest) (*types.QueryWarpsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	params, err := q.k.WarpParams(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	warps, pageRes, err := query.CollectionPaginate(ctx, q.k.Warp, req.Pagination,
		func(_ []byte, w types.WarpState) (types.WarpRecord, error) {
			return warpRecord(w, params), nil
		})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &types.QueryWarpsResponse{Warps: warps, Params: params, Pagination: pageRes}, nil
}
