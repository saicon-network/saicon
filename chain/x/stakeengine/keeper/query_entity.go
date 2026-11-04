package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// EntityStake reports an attested entity's aggregate stake against its cap.
func (q queryServer) EntityStake(ctx context.Context, req *types.QueryEntityStakeRequest) (*types.QueryEntityStakeResponse, error) {
	if req == nil || req.EntityId == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	members, err := q.k.entityKeeper.MemberValidators(ctx, req.EntityId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if len(members) == 0 {
		return nil, status.Errorf(codes.NotFound, "entity %s has no members", req.EntityId)
	}
	s, err := q.k.EntityStanding(ctx, members[0])
	if err != nil {
		return nil, capsError(err)
	}
	seats, err := q.k.entityKeeper.ActiveSeats(ctx, req.EntityId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	limit, err := q.k.entityKeeper.MaxActiveSeats(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryEntityStakeResponse{
		Tokens:         s.Tokens,
		EntityCap:      s.Cap,
		Room:           s.Room(),
		ActiveSeats:    seats,
		MaxActiveSeats: limit,
		Frozen:         s.Frozen,
		Reason:         s.Reason,
	}, nil
}
