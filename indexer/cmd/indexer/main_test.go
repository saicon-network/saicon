package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/follow"
)

// This composition — LastClosedEpoch, EpochOf(firstWalked), "whichever of the
// floor or HighestEpochRead is higher", walk one at a time, stop on error
// rather than skip — produced both of the review's Criticals and Task 5's
// permanent epoch-skipping bug. It lived only in main() with nothing testing
// it until now: every prior catch was a person reading the code or a live
// run.

// cursor 400, tip 1200, epochLen 100: epochs 1-4 already read (highest=4),
// heights up to 1200 walked (indexedHeight=1200). LastClosedEpoch(1200,100)=12.
// A single "read the latest closed epoch" would jump straight to 12 and skip
// 5 through 11 for ever — that is the bug epochPass exists to not have.
func TestEpochPassWalksForwardOneAtATimeWithoutSkipping(t *testing.T) {
	var read []uint64
	readEpoch := func(_ context.Context, e uint64) error {
		read = append(read, e)
		return nil
	}
	epochPass(context.Background(), 1200, 1, 100, 4, true, readEpoch, t.Logf)

	require.Equal(t, []uint64{5, 6, 7, 8, 9, 10, 11, 12}, read,
		"every closed epoch not yet read, in order, none skipped")
}

// A REST failure on epoch 7 must stop the pass there: 8-12 must not be
// attempted (reading ahead of a gap the epoch reader itself has to retry),
// and 7 has to be the one retried on the next tick — not skipped past because
// the pass "moved on".
func TestEpochPassStopsAtTheFirstFailureAndDoesNotSkipPastIt(t *testing.T) {
	var read []uint64
	readEpoch := func(_ context.Context, e uint64) error {
		if e == 7 {
			return errAtSeven
		}
		read = append(read, e)
		return nil
	}
	epochPass(context.Background(), 1200, 1, 100, 4, true, readEpoch, t.Logf)
	require.Equal(t, []uint64{5, 6}, read, "5 and 6 succeeded before the failure; 8-12 were never attempted")

	// Retry, exactly as the real next tick would drive it: ReadEpoch marks an
	// epoch read only after it finishes, so 5 and 6 are in epochs_read and 7 is
	// not — HighestEpochRead now reports 6, and the caller passes that in as
	// `highest`. The pass must resume AT 7, neither re-reading 5 and 6 nor
	// skipping past 7.
	read = nil
	readEpoch = func(_ context.Context, e uint64) error {
		read = append(read, e)
		return nil
	}
	epochPass(context.Background(), 1200, 1, 100, 6, true, readEpoch, t.Logf)
	require.Equal(t, []uint64{7, 8, 9, 10, 11, 12}, read,
		"epoch 7 is retried and the pass continues to completion, without re-reading 5 or 6")
}

var errAtSeven = &fakeErr{"epoch 7: node unreachable"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

// A restart does not carry an in-memory "last epoch I read" — that is
// precisely Task 5's bug. epochPass takes `highest` as an explicit parameter
// rather than closing over state, so calling it fresh (as main() does on
// every tick, re-reading HighestEpochRead from the database each time) with
// the persisted progress resumes correctly instead of re-walking from the
// floor or, worse, from zero.
func TestEpochPassResumesFromPersistedProgressNotFromZero(t *testing.T) {
	var read []uint64
	readEpoch := func(_ context.Context, e uint64) error {
		read = append(read, e)
		return nil
	}

	// First tick: chain has only reached epoch 7's close (indexedHeight=700).
	// Nothing read yet (highestKnown=false), floor is genesis.
	epochPass(context.Background(), 700, 1, 100, 0, false, readEpoch, t.Logf)
	require.Equal(t, []uint64{1, 2, 3, 4, 5, 6, 7}, read)

	// "Restart": a brand new call, as if the process exited and started again.
	// The only thing carried across is what a real HighestEpochRead would
	// report after epochs_read was written for 1-7: highest=7, known=true.
	// The chain has since progressed to tip 1200 (indexedHeight=1200).
	read = nil
	epochPass(context.Background(), 1200, 1, 100, 7, true, readEpoch, t.Logf)
	require.Equal(t, []uint64{8, 9, 10, 11, 12}, read,
		"resumes at 8, from the persisted highest — not back at 1, and not jumped to 12")
}

func TestEpochLengthMismatchDetection(t *testing.T) {
	t.Run("no stored length: nothing to compare against, not a mismatch", func(t *testing.T) {
		_, mismatch := epochLengthMismatch(0, false, 100, "http://node")
		require.False(t, mismatch)
	})
	t.Run("stored length matches current: not a mismatch", func(t *testing.T) {
		_, mismatch := epochLengthMismatch(100, true, 100, "http://node")
		require.False(t, mismatch)
	})
	t.Run("stored length differs from current: a mismatch naming both values", func(t *testing.T) {
		msg, mismatch := epochLengthMismatch(100, true, 200, "http://node:26657")
		require.True(t, mismatch)
		require.Contains(t, msg, "100")
		require.Contains(t, msg, "200")
		require.Contains(t, msg, "http://node:26657")
		require.Contains(t, msg, "start a new database")
	})
}

// Against a pruned node the cursor is seeded forward without a block being read,
// and every epoch below that floor was never walked. The other three cases here
// all pass firstWalked=1, so the floor is never the binding constraint in any of
// them — replacing it with a constant 1 leaves all three green. This is the case
// that holds it: nothing below the floor may be read, because a proposal count
// for an epoch nobody walked is a measured zero that is not a measurement.
func TestEpochPassNeverReadsBelowTheWalkFloor(t *testing.T) {
	var read []uint64
	readEpoch := func(_ context.Context, e uint64) error {
		read = append(read, e)
		return nil
	}

	// The node retains from height 5001, so the walk began there: epochs 1-50
	// belong to blocks this service has never seen and never will.
	const firstWalked = 5001
	epochPass(context.Background(), 5400, firstWalked, 100, 0, false, readEpoch, t.Logf)

	require.Equal(t, []uint64{51, 52, 53, 54}, read,
		"epoch 51 is the first whose blocks begin at or above the walk floor")
	for _, e := range read {
		require.Greater(t, e, uint64(50), "epoch %d lies entirely below the walk floor", e)
	}
}

// recordReads returns a read function that appends each epoch it is asked for,
// failing on failAt (0 means never).
func recordReads(into *[]uint64, failAt uint64) func(context.Context, uint64) error {
	return func(_ context.Context, e uint64) error {
		*into = append(*into, e)
		if e == failAt {
			return errors.New("node unreachable")
		}
		return nil
	}
}

// free is a budget that the reads under test never spend: only the epoch
// cursors decide what is read.
func free() *callBudget {
	return &callBudget{limit: ConcentrationCallBudget, spent: func() int { return 0 }}
}

// The measuring pass trails the follower: it never asks for an epoch whose last
// block the follower has not walked, because the node may not have it either.
func TestConcentrationPassTrailsTheFollower(t *testing.T) {
	var read []uint64
	concentrationPass(context.Background(), 1205, 1, 100, 0, false, free(), recordReads(&read, 0), t.Logf)
	require.Equal(t, []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, read, "epoch 13 is still open")
}

// The pass is bounded by requests, not by epochs: at 9 requests an epoch a
// budget of 180 is twenty epochs, and the twenty-first would not fit.
func TestConcentrationPassHonoursTheCallBudget(t *testing.T) {
	var read []uint64
	spent := 0
	b := &callBudget{limit: 180, spent: func() int { return spent }}
	concentrationPass(context.Background(), 10000, 1, 100, 4, true, b, func(_ context.Context, e uint64) error {
		read = append(read, e)
		spent += 9
		return nil
	}, t.Logf)
	require.Len(t, read, 20)
	require.Equal(t, uint64(5), read[0])
	require.Equal(t, uint64(24), read[len(read)-1])
	require.Equal(t, 180, spent)
	require.Equal(t, 9, b.est, "the last epoch's cost is the estimate for the next pass")
}

// An epoch that alone costs more than the budget is still measured, one per pass.
func TestConcentrationPassAlwaysMakesProgress(t *testing.T) {
	var read []uint64
	spent := 0
	b := &callBudget{limit: 100, spent: func() int { return spent }, est: 500}
	concentrationPass(context.Background(), 10000, 1, 100, 0, false, b, func(_ context.Context, e uint64) error {
		read = append(read, e)
		spent += 500
		return nil
	}, t.Logf)
	require.Equal(t, []uint64{1}, read)
}

func TestConcentrationPassStopsAtTheFirstFailure(t *testing.T) {
	var read []uint64
	concentrationPass(context.Background(), 1200, 1, 100, 4, true, free(), recordReads(&read, 7), t.Logf)
	require.Equal(t, []uint64{5, 6, 7}, read, "7 failed: 8 is never attempted, and 7 is retried next tick")
}

func TestConcentrationPassStartsAtTheWalkFloor(t *testing.T) {
	var read []uint64
	concentrationPass(context.Background(), 1300, 1001, 100, 0, false, free(), recordReads(&read, 0), t.Logf)
	require.Equal(t, []uint64{11, 12, 13}, read, "epochs 1-10 end below the first walked block")
}

func TestContinuityStopsOnADifferentHash(t *testing.T) {
	hashAt := func(context.Context, int64) (string, error) { return "BB", nil }
	v, msg := continuityVerdict(context.Background(), 300, "AA", hashAt)
	require.Equal(t, continuityStop, v)
	require.Contains(t, msg, "AA")
	require.Contains(t, msg, "BB")
	require.Contains(t, msg, "300")
	require.Contains(t, msg, "reset under the same chain id")
}

func TestContinuityIsOKOnTheSameHash(t *testing.T) {
	hashAt := func(context.Context, int64) (string, error) { return "AA", nil }
	v, msg := continuityVerdict(context.Background(), 300, "AA", hashAt)
	require.Equal(t, continuityOK, v)
	require.Empty(t, msg)
}

func TestContinuitySkipsAnUnavailableHeight(t *testing.T) {
	for name, err := range map[string]error{
		"pruned":        fmt.Errorf("block 300: %w", chain.ErrHeightUnavailable),
		"not yet there": fmt.Errorf("block 300: %w", chain.ErrHeightAhead),
	} {
		t.Run(name, func(t *testing.T) {
			v, msg := continuityVerdict(context.Background(), 300, "AA",
				func(context.Context, int64) (string, error) { return "", err })
			require.Equal(t, continuitySkip, v)
			require.Empty(t, msg, "only the check is skipped; the passes run")
		})
	}
	t.Run("any other error also skips the concentration pass", func(t *testing.T) {
		v, msg := continuityVerdict(context.Background(), 300, "AA",
			func(context.Context, int64) (string, error) { return "", errors.New("connection refused") })
		require.Equal(t, continuitySkip, v)
		require.Contains(t, msg, "continuity check:")
		require.Contains(t, msg, "connection refused")
	})
}

// fakeTick builds tick dependencies that record the order they are called in.
func fakeTick(calls *[]string, hashAt func(context.Context, int64) (string, error)) tickDeps {
	rec := func(name string) { *calls = append(*calls, name) }
	return tickDeps{
		epochLen: 100,
		epochLength: func(context.Context) (uint64, error) {
			rec("epochLength")
			return 100, nil
		},
		newestHashed: func(context.Context) (int64, string, bool, error) {
			rec("newestHashed")
			return 300, "AA", true, nil
		},
		hashAt: func(ctx context.Context, h int64) (string, error) {
			rec("hashAt")
			return hashAt(ctx, h)
		},
		followOnce: func(context.Context) (int64, error) {
			rec("followOnce")
			return 0, nil
		},
		epochPass:         func(context.Context) { rec("epochPass") },
		concentrationPass: func(context.Context) { rec("concentrationPass") },
		logf:              func(string, ...any) {},
	}
}

// A node that has not reached the stored height yet cannot answer the continuity
// question, which is not a reason to stop counting: a chain that is merely
// shorter is the follower's to stop.
func TestOnlyTheCheckIsSkippedForAHeightAhead(t *testing.T) {
	var calls []string
	d := fakeTick(&calls, func(context.Context, int64) (string, error) {
		return "", fmt.Errorf("block 300: %w", chain.ErrHeightAhead)
	})
	stop := tick(context.Background(), d)
	require.Empty(t, stop)
	require.Equal(t, []string{"epochLength", "newestHashed", "hashAt",
		"followOnce", "epochPass", "concentrationPass"}, calls)
}

// A reset that keeps the chain id must be caught before the new chain's blocks
// are counted into the proposal history, so the check precedes the follower.
func TestContinuityRunsBeforeTheFollower(t *testing.T) {
	var calls []string
	d := fakeTick(&calls, func(context.Context, int64) (string, error) { return "BB", nil })
	stop := tick(context.Background(), d)
	require.NotEmpty(t, stop)
	require.Contains(t, stop, "reset under the same chain id")
	require.Equal(t, []string{"epochLength", "newestHashed", "hashAt"}, calls)
}

// Any other continuity failure leaves the follower and the epoch pass alone and
// holds back only the new pass.
func TestAFailedContinuityCheckSkipsOnlyTheConcentrationPass(t *testing.T) {
	var calls []string
	d := fakeTick(&calls, func(context.Context, int64) (string, error) { return "", errors.New("connection refused") })
	require.Empty(t, tick(context.Background(), d))
	require.Equal(t, []string{"epochLength", "newestHashed", "hashAt", "followOnce", "epochPass"}, calls)
}

func TestNoMeasuredEpochMeansNoContinuityCheck(t *testing.T) {
	var calls []string
	d := fakeTick(&calls, nil)
	d.newestHashed = func(context.Context) (int64, string, bool, error) {
		calls = append(calls, "newestHashed")
		return 0, "", false, nil
	}
	require.Empty(t, tick(context.Background(), d))
	require.Equal(t, []string{"epochLength", "newestHashed", "followOnce", "epochPass", "concentrationPass"}, calls)
}

func TestTickKeepsTheExistingStopReasons(t *testing.T) {
	var calls []string
	d := fakeTick(&calls, nil)
	d.epochLength = func(context.Context) (uint64, error) { return 200, nil }
	stop := tick(context.Background(), d)
	require.Contains(t, stop, "epoch_length_blocks changed 100 -> 200")
	require.Contains(t, stop, "start a new database")

	calls = nil
	d = fakeTick(&calls, func(context.Context, int64) (string, error) { return "AA", nil })
	d.followOnce = func(context.Context) (int64, error) {
		return 0, &follow.ResetError{ChainID: "c1", Cursor: 500, Tip: 10}
	}
	stop = tick(context.Background(), d)
	require.Contains(t, stop, "chain c1 is at height 10")
	require.NotContains(t, calls, "epochPass")
	require.NotContains(t, calls, "concentrationPass")
}
