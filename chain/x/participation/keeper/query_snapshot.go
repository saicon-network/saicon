package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/participation/types"
)

// Snapshot returns the Participation Score snapshot for a validator at a given
// epoch.
func (q queryServer) Snapshot(ctx context.Context, req *types.QuerySnapshotRequest) (*types.QuerySnapshotResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator address: %v", err)
	}

	snap, found, err := q.k.GetSnapshot(ctx, req.EpochId, valAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if !found {
		return nil, status.Errorf(codes.NotFound, "no snapshot for validator %s at epoch %d", req.ValidatorAddress, req.EpochId)
	}

	return &types.QuerySnapshotResponse{Snapshot: snap}, nil
}

// maxRangeEpochs bounds SnapshotRange. The explorer's card draws 24 epochs; 200
// leaves room for a wider view without letting one request walk the whole chain.
const maxRangeEpochs = 200

// SnapshotRange returns the snapshots a validator has across a closed epoch
// range, ascending. Epochs with no record are skipped: absence means the
// validator was not processed that epoch, which a zero score would misreport.
func (q queryServer) SnapshotRange(ctx context.Context, req *types.QuerySnapshotRangeRequest) (*types.QuerySnapshotRangeResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if req.ToEpoch < req.FromEpoch {
		return nil, status.Error(codes.InvalidArgument, "to_epoch is before from_epoch")
	}
	// The width is `to - from + 1`, but computing it that way overflows: for
	// to = MaxUint64 it wraps to 0, sails past the cap, and enters a loop whose
	// exit condition `e <= MaxUint64` is a tautology. `to >= from` is already
	// established above, so `to - from` cannot wrap — compare that instead.
	// Equivalent for every non-overflowing input: width > cap ⟺ to-from ≥ cap.
	width := req.ToEpoch - req.FromEpoch
	if width >= maxRangeEpochs {
		return nil, status.Errorf(codes.InvalidArgument, "range wider than %d epochs", maxRangeEpochs)
	}

	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator address: %v", err)
	}

	// width < maxRangeEpochs was just established, so width+1 (the inclusive
	// epoch count) cannot overflow — reuse it instead of recomputing the same
	// subtraction a second time.
	out := make([]types.ParticipationScoreSnapshot, 0, width+1)
	// Counting with width rather than comparing e to req.ToEpoch: that exit
	// condition, `e <= req.ToEpoch`, is a tautology the instant e reaches
	// MaxUint64, since e++ wraps it to 0 and 0 <= MaxUint64 is still true — an
	// unbounded scan the width guard above cannot catch, because the range
	// that triggers it is legally narrow and merely ends at the top of the
	// type. i only climbs from 0 to width, so it cannot wrap, and
	// req.FromEpoch+i cannot overflow since i <= width == to-from.
	for i := uint64(0); i <= width; i++ {
		e := req.FromEpoch + i
		snap, found, err := q.k.GetSnapshot(ctx, e, valAddr)
		if err != nil {
			return nil, status.Error(codes.Internal, "internal error")
		}
		if found {
			out = append(out, snap)
		}
	}
	return &types.QuerySnapshotRangeResponse{Snapshots: out}, nil
}
