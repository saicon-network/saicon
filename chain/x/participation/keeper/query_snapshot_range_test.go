package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/participation/keeper"
	"github.com/saicon-network/saicon/chain/x/participation/types"
)

// snapshotAt builds a minimal, valid ParticipationScoreSnapshot for addr at
// epoch, distinguishable by BlocksSigned so ordering/identity is checkable
// without depending on ComputedAt.
func snapshotAt(addr sdk.ValAddress, epoch uint64, blocksSigned uint64) types.ParticipationScoreSnapshot {
	return types.ParticipationScoreSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          epoch,
		BlocksSigned:     blocksSigned,
		UptimeFraction:   math.LegacyNewDecWithPrec(95, 2),
		ComputedScore:    math.LegacyNewDecWithPrec(95, 2),
		ComputedAt:       time.Now().UTC(),
	}
}

// 1. Contiguous range returns every epoch, ascending.
func TestSnapshotRangeQuery_Contiguous(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("snap-range-contig-20"))[:20]

	// Insert out of order to make sure the response orders by epoch, not by
	// insertion order.
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 7, addr, snapshotAt(addr, 7, 70)))
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 5, addr, snapshotAt(addr, 5, 50)))
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 6, addr, snapshotAt(addr, 6, 60)))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.SnapshotRange(f.ctx, &types.QuerySnapshotRangeRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        5,
		ToEpoch:          7,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Snapshots, 3)
	require.Equal(t, uint64(5), resp.Snapshots[0].EpochId)
	require.Equal(t, uint64(6), resp.Snapshots[1].EpochId)
	require.Equal(t, uint64(7), resp.Snapshots[2].EpochId)
	require.Equal(t, uint64(50), resp.Snapshots[0].BlocksSigned)
	require.Equal(t, uint64(60), resp.Snapshots[1].BlocksSigned)
	require.Equal(t, uint64(70), resp.Snapshots[2].BlocksSigned)
}

// 2. A gap in the middle is omitted, not zero-filled.
func TestSnapshotRangeQuery_GapOmitted(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("snap-range-gap-20byt"))[:20]

	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 10, addr, snapshotAt(addr, 10, 10)))
	// Epoch 11 deliberately has no snapshot.
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 12, addr, snapshotAt(addr, 12, 12)))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.SnapshotRange(f.ctx, &types.QuerySnapshotRangeRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        10,
		ToEpoch:          12,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Snapshots, 2)
	require.Equal(t, uint64(10), resp.Snapshots[0].EpochId)
	require.Equal(t, uint64(12), resp.Snapshots[1].EpochId)
}

// 3. to_epoch < from_epoch is InvalidArgument.
func TestSnapshotRangeQuery_ToBeforeFrom_InvalidArgument(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("snap-range-order-20b"))[:20]

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.SnapshotRange(f.ctx, &types.QuerySnapshotRangeRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        10,
		ToEpoch:          5,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// 4. A range wider than maxRangeEpochs is InvalidArgument.
func TestSnapshotRangeQuery_TooWide_InvalidArgument(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("snap-range-wide-20by"))[:20]

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.SnapshotRange(f.ctx, &types.QuerySnapshotRangeRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        1,
		ToEpoch:          202, // width 202 > maxRangeEpochs (200)
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// 5. (Added beyond the brief's four cases.) An unparsable validator address
// is InvalidArgument, not a silently empty result. The loop body never runs
// GetSnapshot with a garbage address today because parseValAddr is checked
// first, but that ordering is exactly the kind of thing a future edit could
// invert by accident — e.g. someone hoists the loop above the address check
// while refactoring, and a malformed address would then just look like "no
// snapshots" (empty slice, no error) instead of a rejected request. Chosen
// over an exact-cap-boundary test (to_epoch-from_epoch+1 == maxRangeEpochs
// succeeds) because this exercises a branch of SnapshotRange the brief's
// four cases never reach at all, and mirrors the sibling
// x/rewards/keeper/query_snapshot_test.go::TestRewardSnapshotQuery_InvalidAddress
// idiom already established in this codebase.
func TestSnapshotRangeQuery_InvalidAddress(t *testing.T) {
	f := initFixture(t)

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.SnapshotRange(f.ctx, &types.QuerySnapshotRangeRequest{
		ValidatorAddress: "not-a-valid-bech32-address",
		FromEpoch:        1,
		ToEpoch:          2,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// --- Fix Round 1: review found the width guard overflows for pathological
// inputs (ToEpoch-FromEpoch+1 wraps to 0 for FromEpoch=0, ToEpoch=MaxUint64,
// sailing past the cap into a loop whose exit condition is then a tautology).
// TestSnapshotRangeQuery_TooWide_InvalidArgument above only used 1..202, which
// can't distinguish a correct guard from an off-by-one, and never approaches
// the values that actually overflow. These four pin both the fencepost and
// the overflow case.

// 6. Exactly maxRangeEpochs wide is accepted — the upper edge of the cap.
func TestSnapshotRangeQuery_ExactlyMaxWidth_Accepted(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("snap-range-exact-20b"))[:20]

	const from, to = 1, 1 + 200 - 1 // width (to-from+1) == maxRangeEpochs, 200

	require.NoError(t, f.keeper.SetSnapshot(f.ctx, from, addr, snapshotAt(addr, from, 1)))
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, to, addr, snapshotAt(addr, to, 2)))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.SnapshotRange(f.ctx, &types.QuerySnapshotRangeRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        from,
		ToEpoch:          to,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Snapshots, 2)
	require.Equal(t, uint64(from), resp.Snapshots[0].EpochId)
	require.Equal(t, uint64(to), resp.Snapshots[1].EpochId)
}

// 7. maxRangeEpochs+1 wide is rejected — the other side of the same fencepost
// as case 6. Neither case alone catches a `>` vs `>=` (or `>=` vs `>`) mistake
// in the guard; only having both pins the exact boundary.
func TestSnapshotRangeQuery_OneOverMaxWidth_InvalidArgument(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("snap-range-over-20by"))[:20]

	const from, to = 1, 1 + 201 - 1 // width (to-from+1) == maxRangeEpochs+1, 201

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.SnapshotRange(f.ctx, &types.QuerySnapshotRangeRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        from,
		ToEpoch:          to,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// 8. from_epoch == to_epoch is a valid single-epoch range, and returns that
// epoch when it has a snapshot.
func TestSnapshotRangeQuery_SingleEpoch(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("snap-range-single-2b"))[:20]

	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 9, addr, snapshotAt(addr, 9, 42)))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.SnapshotRange(f.ctx, &types.QuerySnapshotRangeRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        9,
		ToEpoch:          9,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Snapshots, 1)
	require.Equal(t, uint64(9), resp.Snapshots[0].EpochId)
	require.Equal(t, uint64(42), resp.Snapshots[0].BlocksSigned)
}

// 9. FromEpoch=0, ToEpoch=MaxUint64 must be rejected, and rejected fast. This
// is the exact input that overflowed the original guard: ToEpoch-FromEpoch+1
// wraps to 0, "0 > maxRangeEpochs" is false, and the request falls into a loop
// whose "e <= ToEpoch" exit condition never goes false for any representable
// e — an unauthenticated, effectively-infinite scan.
//
// Asserting only "returns an error" is not sufficient: a future change could
// keep the guard's verdict correct while making it slow (e.g. validating after
// a partial scan, or restructuring so the check no longer runs before the
// loop), and this test would still pass while reintroducing the DoS. The
// elapsed-time budget is what actually pins "rejected before the loop runs."
//
// The call runs in a goroutine with a bounded wait rather than a direct call,
// so that if the guard regresses, this test fails within ~2s instead of
// hanging the suite for however long a MaxUint64-iteration loop takes (not a
// real duration on human timescales).
func TestSnapshotRangeQuery_UnboundedRange_RejectedQuickly(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("snap-range-unbound-2"))[:20]

	q := keeper.NewQueryServerImpl(f.keeper)

	type result struct {
		resp *types.QuerySnapshotRangeResponse
		err  error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		resp, err := q.SnapshotRange(f.ctx, &types.QuerySnapshotRangeRequest{
			ValidatorAddress: addr.String(),
			FromEpoch:        0,
			ToEpoch:          ^uint64(0), // math.MaxUint64, without colliding with the cosmossdk.io/math import
		})
		done <- result{resp, err}
	}()

	select {
	case r := <-done:
		elapsed := time.Since(start)
		require.Error(t, r.err)
		require.Nil(t, r.resp)
		require.Equal(t, codes.InvalidArgument, status.Code(r.err))
		if elapsed >= 100*time.Millisecond {
			t.Fatalf("SnapshotRange(0, MaxUint64) took %s to be rejected; the width guard must reject an unbounded range before it ever reaches the scan loop", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SnapshotRange(0, MaxUint64) did not return within 2s — the width guard likely overflowed and let an unbounded range reach the scan loop (this is the DoS the cap exists to prevent)")
	}
}

// --- Fix Round 2 (whole-branch review): the width guard above is correct and
// stays as it is -- see case 9. But the loop's own exit condition, `e <=
// req.ToEpoch`, has a second, independent overflow no width guard can catch:
// for a range that is legally narrow (width well under the 200 cap) but
// happens to end at MaxUint64, e reaches MaxUint64 inside the loop body, e++
// wraps it to 0, and `0 <= req.ToEpoch` (still MaxUint64) is true again -- the
// loop never terminates. Case 9's input is rejected before the loop ever
// starts (its width computation itself overflows); this input sails past the
// same guard legitimately (width 199 is well under the cap) and then hangs
// inside the loop the guard correctly let it reach.

// 10. A range of exactly maxRangeEpochs width that ends at MaxUint64 must
// return quickly, not hang, even with nothing stored at those epochs. Same
// goroutine + buffered channel + select shape as case 9, so a regression
// fails this test instead of blocking the suite.
func TestSnapshotRangeQuery_NarrowRangeEndingAtMaxUint64_ReturnsQuickly(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("snap-range-wrap-20by"))[:20]

	const (
		to   = ^uint64(0)   // MaxUint64, the top of the type
		from = to - 200 + 1 // width (to-from) == 199, one under maxRangeEpochs (200): legal
	)

	q := keeper.NewQueryServerImpl(f.keeper)

	type result struct {
		resp *types.QuerySnapshotRangeResponse
		err  error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		resp, err := q.SnapshotRange(f.ctx, &types.QuerySnapshotRangeRequest{
			ValidatorAddress: addr.String(),
			FromEpoch:        from,
			ToEpoch:          to,
		})
		done <- result{resp, err}
	}()

	select {
	case r := <-done:
		elapsed := time.Since(start)
		require.NoError(t, r.err)
		require.NotNil(t, r.resp)
		require.Empty(t, r.resp.Snapshots, "no snapshots were stored at these epochs")
		if elapsed >= 500*time.Millisecond {
			t.Fatalf("SnapshotRange(MaxUint64-199, MaxUint64) took %s to return; a 200-epoch scan measures in microseconds, not this", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SnapshotRange(MaxUint64-199, MaxUint64) did not return within 2s — the loop's own exit condition likely wrapped past MaxUint64 back to 0, turning this legal 200-epoch range into an unbounded scan")
	}
}
