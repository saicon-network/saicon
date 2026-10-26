package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

const (
	defaultQueueLimit = 100
	maxQueueLimit     = 500
)

func (q queryServer) Queue(ctx context.Context, req *types.QueryQueueRequest) (*types.QueryQueueResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	offset, limit := uint64(0), uint64(defaultQueueLimit)
	if p := req.Pagination; p != nil {
		if len(p.Key) > 0 {
			return nil, status.Error(codes.InvalidArgument, "the queue is paginated by offset: its fill order is computed, not stored")
		}
		offset = p.Offset
		if p.Limit > 0 {
			limit = p.Limit
		}
	}
	if limit > maxQueueLimit {
		limit = maxQueueLimit
	}

	var entries []types.QueueEntry
	if err := q.k.Queue.Walk(ctx, nil, func(_ uint64, e types.QueueEntry) (bool, error) {
		entries = append(entries, e)
		return false, nil
	}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	var epoch uint64
	if st, err := q.k.capState(ctx); err == nil {
		epoch = st.Epoch
	}
	ordered := types.SortQueue(entries, q.k.qualityAt(ctx, epoch))

	total := uint64(len(ordered))
	start := min(offset, total)
	end := min(start+limit, total)
	return &types.QueryQueueResponse{
		Entries:    ordered[start:end],
		Pagination: &query.PageResponse{Total: total},
	}, nil
}

func (q queryServer) QueueEntry(ctx context.Context, req *types.QueryQueueEntryRequest) (*types.QueryQueueEntryResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	e, err := q.k.Queue.Get(ctx, req.Id)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "no queue entry %d", req.Id)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryQueueEntryResponse{Entry: e}, nil
}

func (q queryServer) DelegatorQueue(ctx context.Context, req *types.QueryDelegatorQueueRequest) (*types.QueryDelegatorQueueResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	want, err := q.k.addressCodec.StringToBytes(req.DelegatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	out := []types.QueueEntry{}
	if err := q.k.Queue.Walk(ctx, nil, func(_ uint64, e types.QueueEntry) (bool, error) {
		if owner, err := q.k.addressCodec.StringToBytes(e.Delegator); err == nil && bytes.Equal(owner, want) {
			out = append(out, e)
		}
		return false, nil
	}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryDelegatorQueueResponse{Entries: out}, nil
}
