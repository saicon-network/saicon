package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/rewards/keeper"
	"github.com/saicon-network/saicon/chain/x/rewards/types"
)

// summaryCoin formats a base-unit amount as the Coin string RewardSnapshot.Amount
// actually stores in production (DistributeEpochRewards writes reward.String()
// in emission.go), rather than a hand-typed literal that could drift from how
// sdk actually formats a Coins string.
func summaryCoin(denom string, amt int64) string {
	return sdk.NewCoins(sdk.NewCoin(denom, math.NewInt(amt))).String()
}

// 1. Three epochs, two eligible (100 + 250) and one denied (empty amount, the
// shape DistributeEpochRewards' denied() actually writes -- see emission.go):
// total_amount == 350, epochs_with_record == 3 (the denial IS a record),
// epochs_paid == 2, epochs_requested == 3.
func TestRewardSummaryQuery_MixedEligibleAndDenied(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(1)

	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 5, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          5,
		Amount:           summaryCoin(types.RewardDenom, 100),
		Eligible:         true,
		Reason:           "eligible",
	}))
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 6, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          6,
		Amount:           summaryCoin(types.RewardDenom, 250),
		Eligible:         true,
		Reason:           "eligible",
	}))
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 7, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          7,
		Amount:           "",
		Eligible:         false,
		Reason:           "EP=0",
	}))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        5,
		ToEpoch:          7,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.TotalAmount.Equal(math.NewInt(350)), "got %s", resp.TotalAmount)
	require.Equal(t, types.RewardDenom, resp.Denom)
	require.Equal(t, uint64(3), resp.EpochsRequested)
	require.Equal(t, uint64(3), resp.EpochsWithRecord)
	require.Equal(t, uint64(2), resp.EpochsPaid)
}

// 2. A range with no records at all is NOT an error -- a young chain
// legitimately has nothing to report yet.
func TestRewardSummaryQuery_NoRecords_NotAnError(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(2)

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        1000,
		ToEpoch:          1005,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.TotalAmount.IsZero(), "got %s", resp.TotalAmount)
	require.Equal(t, "", resp.Denom, "no snapshot was summed, so no denom should be invented")
	require.Equal(t, uint64(6), resp.EpochsRequested)
	require.Equal(t, uint64(0), resp.EpochsWithRecord)
	require.Equal(t, uint64(0), resp.EpochsPaid)
}

// 3. to_epoch < from_epoch is InvalidArgument.
func TestRewardSummaryQuery_ToBeforeFrom_InvalidArgument(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(3)

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        10,
		ToEpoch:          5,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// 4. A range wider than maxSummaryEpochs is InvalidArgument.
func TestRewardSummaryQuery_TooWide_InvalidArgument(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(4)

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        1,
		ToEpoch:          25_000, // width 24 999, well past maxSummaryEpochs (10 000)
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// 5. denom matches the denom actually on the summed snapshots, rather than a
// hardcoded types.RewardDenom. Proven by using a DIFFERENT denom than the
// module's own RewardDenom ("nscn"): if the implementation ever hardcoded
// RewardDenom instead of reading coin.Denom off the snapshot, this would catch
// it, where a same-as-production-denom test would not.
func TestRewardSummaryQuery_DenomMatchesSnapshots(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(5)
	const otherDenom = "footoken"

	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 40, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          40,
		Amount:           summaryCoin(otherDenom, 300),
		Eligible:         true,
		Reason:           "eligible",
	}))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        40,
		ToEpoch:          40,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, otherDenom, resp.Denom)
	require.True(t, resp.TotalAmount.Equal(math.NewInt(300)), "got %s", resp.TotalAmount)
}

// --- Boundary coverage carried over from Task 3's SnapshotRange, whose width
// guard this RewardSummary guard is a corrected copy of (see
// chain/x/participation/keeper/query_snapshot.go and
// query_snapshot_range_test.go). A "too wide" test using a value merely past
// the cap (case 4 above) cannot distinguish a correct guard from an
// off-by-one, and never approaches the values where uint64 arithmetic actually
// overflows -- these four close that gap.

// 6. Exactly maxSummaryEpochs wide is accepted -- the upper edge of the cap.
func TestRewardSummaryQuery_ExactlyMaxWidth_Accepted(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(6)

	const from, to = 1, 1 + 10_000 - 1 // epoch count (to-from+1) == maxSummaryEpochs, 10 000

	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, from, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          from,
		Amount:           summaryCoin(types.RewardDenom, 10),
		Eligible:         true,
		Reason:           "eligible",
	}))
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, to, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          to,
		Amount:           summaryCoin(types.RewardDenom, 20),
		Eligible:         true,
		Reason:           "eligible",
	}))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        from,
		ToEpoch:          to,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, uint64(10_000), resp.EpochsRequested)
	require.Equal(t, uint64(2), resp.EpochsWithRecord)
	require.Equal(t, uint64(2), resp.EpochsPaid)
	require.True(t, resp.TotalAmount.Equal(math.NewInt(30)), "got %s", resp.TotalAmount)
}

// 7. maxSummaryEpochs+1 wide is rejected -- the other side of the same
// fencepost as case 6. Neither test alone catches a `>` vs `>=` mistake in the
// guard; only having both pins the exact boundary.
func TestRewardSummaryQuery_OneOverMaxWidth_InvalidArgument(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(7)

	const from, to = 1, 1 + 10_001 - 1 // epoch count == maxSummaryEpochs+1, 10 001

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        from,
		ToEpoch:          to,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// 8. from_epoch == to_epoch is a valid single-epoch range.
func TestRewardSummaryQuery_SingleEpoch(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(8)

	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 9, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          9,
		Amount:           summaryCoin(types.RewardDenom, 42),
		Eligible:         true,
		Reason:           "eligible",
	}))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        9,
		ToEpoch:          9,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, uint64(1), resp.EpochsRequested)
	require.Equal(t, uint64(1), resp.EpochsWithRecord)
	require.Equal(t, uint64(1), resp.EpochsPaid)
	require.True(t, resp.TotalAmount.Equal(math.NewInt(42)), "got %s", resp.TotalAmount)
}

// 9. FromEpoch=0, ToEpoch=MaxUint64 must be rejected, and rejected fast. This is
// the exact input that overflows the naive `to - from + 1 > cap` guard: it
// wraps to 0, "0 > cap" is false, and the request falls into a loop whose
// `e <= ToEpoch` exit condition never goes false for any representable e -- an
// unauthenticated, effectively-infinite scan. This shipped in Task 3
// (x/participation SnapshotRange) and was caught in review; RewardSummary
// copies the same guard shape, so it inherits the same risk if the fix is ever
// "simplified" back to the naive form.
//
// Asserting only "returns an error" is not sufficient: a future change could
// keep the guard's verdict correct while making it slow (e.g. validating after
// a partial scan), and this test would still pass while reintroducing the DoS.
// The elapsed-time budget is what actually pins "rejected before the loop
// runs." The call runs in a goroutine with a bounded wait so that if the guard
// regresses, this test fails within ~2s instead of hanging the suite for
// however long a MaxUint64-iteration loop would take.
func TestRewardSummaryQuery_UnboundedRange_RejectedQuickly(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(9)

	q := keeper.NewQueryServerImpl(f.keeper)

	type result struct {
		resp *types.QueryRewardSummaryResponse
		err  error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
			ValidatorAddress: addr.String(),
			FromEpoch:        0,
			ToEpoch:          ^uint64(0), // math.MaxUint64, without colliding with this file's cosmossdk.io/math import
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
			t.Fatalf("RewardSummary(0, MaxUint64) took %s to be rejected; the width guard must reject an unbounded range before it ever reaches the scan loop", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RewardSummary(0, MaxUint64) did not return within 2s -- the width guard likely overflowed and let an unbounded range reach the scan loop (this is the DoS the cap exists to prevent)")
	}
}

// 10. An unparsable validator address is InvalidArgument. Mirrors the
// established idiom for this exact query pair already in this package
// (TestRewardSnapshotQuery_InvalidAddress in query_snapshot_test.go) and its
// sibling in x/participation (TestSnapshotRangeQuery_InvalidAddress) -- none of
// the cases above ever exercise parseValAddr's error branch.
func TestRewardSummaryQuery_InvalidAddress(t *testing.T) {
	f := initFixture(t)

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: "not-a-valid-bech32-address",
		FromEpoch:        1,
		ToEpoch:          2,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// 11. A nil request is InvalidArgument. Mirrors TestRewardSnapshotQuery_NilRequest
// in the same package.
func TestRewardSummaryQuery_NilRequest(t *testing.T) {
	f := initFixture(t)

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, nil)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// --- Fix Round 1: review found that a coin whose denom differs from the first
// one seen was dropped from the total in silence, while EpochsWithRecord and
// EpochsPaid (already incremented before the amount is even parsed) still
// counted that epoch as paid. total_amount and the coverage counters render
// side by side on the card ("N of M epochs with a reward"), so the two would
// contradict each other with no error to explain why. The same silent
// `continue` also swallowed a corrupted non-empty Amount, indistinguishable
// from the module's actual empty-string denial sentinel. Unreachable through
// today's writers (types.RewardDenom is a Go const and both emission.go sites
// build Amount from it), which is the argument for pinning it with a test, not
// against fixing it: the safety lives entirely outside this function.

// 12. Two eligible snapshots in one range that mint different denoms must fail
// closed (codes.Internal), not silently drop the second amount while still
// reporting its epoch as paid. Reproduces the reviewer's exact scenario: one
// epoch mints 100nscn, the next mints 999footoken. Before the fix this
// returned Denom=nscn, TotalAmount=100, EpochsPaid=2, err=nil -- the 999
// vanished with no signal.
func TestRewardSummaryQuery_MixedDenom_Internal(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(10)

	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 20, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          20,
		Amount:           summaryCoin(types.RewardDenom, 100),
		Eligible:         true,
		Reason:           "eligible",
	}))
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 21, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          21,
		Amount:           summaryCoin("footoken", 999),
		Eligible:         true,
		Reason:           "eligible",
	}))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        20,
		ToEpoch:          21,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.Internal, status.Code(err))
}

// 13. A non-empty Amount that fails to parse must fail closed (codes.Internal),
// not be treated as the module's empty-string denial sentinel. The two cases
// used to fall into the same silent `continue` but mean opposite things: one
// is "this validator earned nothing," the other is "the store holds something
// this module never wrote."
func TestRewardSummaryQuery_MalformedAmount_Internal(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(11)

	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 30, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          30,
		Amount:           "not-a-coin-string",
		Eligible:         true,
		Reason:           "eligible",
	}))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        30,
		ToEpoch:          30,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.Internal, status.Code(err))
}

// Note on the brief's third suggested case (a denied epoch still counts in
// EpochsWithRecord, is absent from EpochsPaid, contributes nothing to the
// total, and does NOT error): TestRewardSummaryQuery_MixedEligibleAndDenied
// above already asserts exactly these four facts in one place (require.NoError
// on the same response that has EpochsWithRecord==3, EpochsPaid==2, and
// TotalAmount==350 with epoch 7's Amount=="" contributing zero of it) and
// continues to hold under the fixed code -- see the fix-round verification in
// task-4-report.md. Not duplicated here per the review's own instruction to
// skip rather than duplicate when the existing test already covers it.

// --- Fix Round 2 (whole-branch review): the width guard above is correct and
// stays as it is -- it is a corrected copy of x/participation SnapshotRange's
// guard (see case 9's note). But the loop's own exit condition, `e <=
// req.ToEpoch`, has a second, independent overflow no width guard can catch:
// for a range that is legally narrow (width well under the 10 000 cap) but
// happens to end at MaxUint64, e reaches MaxUint64 inside the loop body, e++
// wraps it to 0, and `0 <= req.ToEpoch` (still MaxUint64) is true again -- the
// loop never terminates. Case 9's input is rejected before the loop ever
// starts (its width computation itself overflows); this input sails past the
// same guard legitimately (width 9 999 is well under the cap) and then hangs
// inside the loop the guard correctly let it reach.

// 14. A range of exactly maxSummaryEpochs width that ends at MaxUint64 must
// return quickly, not hang, even with nothing stored at those epochs. The
// loop legitimately does 10 000 point reads here, so the elapsed budget is
// generous -- case 6 above measured 11ms of real work for the same 10 000
// reads, so 2s catches a hang without being flaky under CI load. Same
// goroutine + buffered channel + select shape as case 9, so a regression
// fails this test instead of blocking the suite.
func TestRewardSummaryQuery_NarrowRangeEndingAtMaxUint64_ReturnsQuickly(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(90)

	const (
		to   = ^uint64(0)      // MaxUint64, the top of the type
		from = to - 10_000 + 1 // width (to-from) == 9 999, one under maxSummaryEpochs (10 000): legal
	)

	q := keeper.NewQueryServerImpl(f.keeper)

	type result struct {
		resp *types.QueryRewardSummaryResponse
		err  error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
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
		require.True(t, r.resp.TotalAmount.IsZero(), "got %s", r.resp.TotalAmount)
		require.Equal(t, "", r.resp.Denom, "no snapshot was summed, so no denom should be invented")
		require.Equal(t, uint64(10_000), r.resp.EpochsRequested)
		require.Equal(t, uint64(0), r.resp.EpochsWithRecord)
		require.Equal(t, uint64(0), r.resp.EpochsPaid)
		if elapsed >= 2*time.Second {
			t.Fatalf("RewardSummary(MaxUint64-9999, MaxUint64) took %s to return; 10 000 real point reads measure in milliseconds, not this", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RewardSummary(MaxUint64-9999, MaxUint64) did not return within 5s — the loop's own exit condition likely wrapped past MaxUint64 back to 0, turning this legal 10 000-epoch range into an unbounded scan")
	}
}

// An eligible snapshot with an empty Amount is not a denial. Before the sentinel
// checked both halves it took the silent-skip path, so the epoch counted in
// epochs_paid while contributing nothing to the total — a paid epoch whose money
// is missing, reported as if everything were fine. emission.go cannot produce
// this pair today; that is the reason to pin it rather than the reason not to.
func TestRewardSummaryQuery_EligibleWithEmptyAmount_Internal(t *testing.T) {
	f := initFixture(t)
	addr := valAddr(1)
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 5, addr, types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          5,
		Amount:           "",
		Eligible:         true,
		Reason:           "eligible",
	}))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: addr.String(),
		FromEpoch:        5,
		ToEpoch:          5,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.Internal, status.Code(err))
}
