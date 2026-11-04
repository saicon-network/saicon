package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

var _ types.QueryServer = queryServer{}

// NewQueryServerImpl returns an implementation of the QueryServer interface
// for the provided Keeper. Draw records are read here, from inside the package
// that owns them, and nowhere else (spec §10.3).
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return queryServer{k}
}

type queryServer struct {
	k Keeper
}

// Params returns the module parameters.
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

// Round returns the current round with its pool, its submissions and the
// quorum the pool requires.
func (q queryServer) Round(ctx context.Context, req *types.QueryRoundRequest) (*types.QueryRoundResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	round, err := q.k.CurrentRound.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res := &types.QueryRoundResponse{Round: round}
	if err := q.k.Pool.Walk(ctx, nil, func(_ string, e types.PoolEntry) (bool, error) {
		res.Pool = append(res.Pool, e)
		return false, nil
	}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := q.k.Submissions.Walk(ctx, nil, func(_ string, s types.Submission) (bool, error) {
		res.Submissions = append(res.Submissions, s)
		return false, nil
	}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res.Required = types.Required(uint32(len(res.Pool)))
	return res, nil
}

// Draw returns the record of one closed round.
func (q queryServer) Draw(ctx context.Context, req *types.QueryDrawRequest) (*types.QueryDrawResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	d, err := q.k.draws.Get(ctx, req.Round)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "no record for round %d", req.Round)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryDrawResponse{Draw: d}, nil
}

// Draws pages through the stored records; pagination.reverse gives newest first.
func (q queryServer) Draws(ctx context.Context, req *types.QueryDrawsRequest) (*types.QueryDrawsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	draws, pageRes, err := query.CollectionPaginate(ctx, q.k.draws, req.Pagination,
		func(_ uint64, d types.Draw) (types.Draw, error) { return d, nil })
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryDrawsResponse{Draws: draws, Pagination: pageRes}, nil
}

// LatestDraw returns the newest record and the newest DRAWN record; each is nil
// when there is none yet.
func (q queryServer) LatestDraw(ctx context.Context, req *types.QueryLatestDrawRequest) (*types.QueryLatestDrawResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	res := &types.QueryLatestDrawResponse{}
	err := q.k.draws.Walk(ctx, new(collections.Range[uint64]).Descending(), func(_ uint64, d types.Draw) (bool, error) {
		if res.Latest == nil {
			res.Latest = &d
		}
		if d.Outcome == types.DrawOutcome_DRAW_OUTCOME_DRAWN {
			res.LatestDrawn = &d
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return res, nil
}
