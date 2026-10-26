package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
)

// RewardSnapshot returns the stored reward decision for a validator at an epoch.
func (q queryServer) RewardSnapshot(ctx context.Context, req *types.QueryRewardSnapshotRequest) (*types.QueryRewardSnapshotResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator address: %v", err)
	}
	snap, found, err := q.k.GetRewardSnapshot(ctx, req.EpochId, valAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if !found {
		return nil, status.Errorf(codes.NotFound, "no reward snapshot for %s at epoch %d", req.ValidatorAddress, req.EpochId)
	}
	return &types.QueryRewardSnapshotResponse{Snapshot: snap}, nil
}

// maxSummaryEpochs bounds RewardSummary. Thirty days is ~5 200 epochs at 100
// blocks and ~8 minutes per epoch; 10 000 covers that with headroom and still
// keeps one request bounded.
const maxSummaryEpochs = 10_000

// RewardSummary sums a validator's emissions over a closed epoch range and
// reports the coverage alongside the total. The counts matter as much as the
// sum: the same total means something different over 100 paid epochs than over
// 100 epochs of which 40 were denied.
func (q queryServer) RewardSummary(ctx context.Context, req *types.QueryRewardSummaryRequest) (*types.QueryRewardSummaryResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if req.ToEpoch < req.FromEpoch {
		return nil, status.Error(codes.InvalidArgument, "to_epoch is before from_epoch")
	}
	// `to - from + 1` overflows for to = MaxUint64 and wraps to 0, which passes
	// the cap and enters a loop whose exit condition is a tautology. This exact
	// bug shipped in Task 3 and was caught in review; do not reintroduce it.
	// `to >= from` is established above, so `to - from` cannot wrap.
	if req.ToEpoch-req.FromEpoch >= maxSummaryEpochs {
		return nil, status.Errorf(codes.InvalidArgument, "range wider than %d epochs", maxSummaryEpochs)
	}

	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator address: %v", err)
	}

	resp := &types.QueryRewardSummaryResponse{
		TotalAmount:     math.ZeroInt(),
		EpochsRequested: req.ToEpoch - req.FromEpoch + 1,
	}
	// width cannot overflow: the guard above already rejected any range with
	// to-from >= maxSummaryEpochs, so to-from fits comfortably under 10 000.
	// The loop must count with it rather than compare e to req.ToEpoch: that
	// exit condition, `e <= req.ToEpoch`, is a tautology the instant e reaches
	// MaxUint64, since e++ wraps it to 0 and 0 <= MaxUint64 is still true — an
	// unbounded scan the guard above cannot catch, because the range that
	// triggers it is legally narrow and merely ends at the top of the type.
	width := req.ToEpoch - req.FromEpoch
	for i := uint64(0); i <= width; i++ {
		e := req.FromEpoch + i
		snap, err := q.k.RewardSnapshot.Get(ctx, collections.Join(e, valAddr.Bytes()))
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				continue
			}
			return nil, status.Error(codes.Internal, "internal error")
		}
		resp.EpochsWithRecord++
		if snap.Eligible {
			resp.EpochsPaid++
		}
		// Three cases, and they must stay distinguishable. A denial records an
		// empty Amount AND Eligible false (emission.go:299-303) — that pair is
		// the module's sentinel, not corruption, and it is skipped in silence.
		// Both halves are checked: an eligible snapshot with an empty Amount is
		// not a denial, it is a paid epoch whose money went missing, and letting
		// it through this branch would count it in EpochsPaid while adding
		// nothing to the total — the same contradiction the denom check below
		// exists to prevent. Anything else that fails to
		// parse, or that carries a second denom, means the store holds something
		// this module never wrote: fail rather than return a total that silently
		// omits money while the counters beside it still call the epoch paid.
		if snap.Amount == "" && !snap.Eligible {
			continue
		}
		coin, perr := sdk.ParseCoinNormalized(snap.Amount)
		if perr != nil {
			return nil, status.Error(codes.Internal, "malformed reward amount in store")
		}
		if resp.Denom == "" {
			resp.Denom = coin.Denom
		}
		if coin.Denom != resp.Denom {
			return nil, status.Error(codes.Internal, "reward snapshots in range carry more than one denom")
		}
		resp.TotalAmount = resp.TotalAmount.Add(coin.Amount)
	}
	return resp, nil
}

// parseValAddr accepts a bech32 string and returns the operator address bytes.
// Accepts both valoper- and account-prefixed forms (v0.1 leniency; the module
// keys on the raw 20 bytes regardless of prefix). Mirrors the validatorregistry
// query helper.
func parseValAddr(s string) (sdk.ValAddress, error) {
	if va, err := sdk.ValAddressFromBech32(s); err == nil {
		return va, nil
	}
	acc, err := sdk.AccAddressFromBech32(s)
	if err != nil {
		return nil, err
	}
	return sdk.ValAddress(acc), nil
}
