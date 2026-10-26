package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
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
