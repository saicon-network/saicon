package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

func TestRoundOneIsCreatedUnscheduledAndScheduledAtTheFirstEndBlock(t *testing.T) {
	f := newFixture(t)
	r := f.round()
	require.EqualValues(t, 1, r.Number)
	require.Zero(t, r.RotationHeight)

	f.run(1, 1)

	r = f.round()
	require.EqualValues(t, 20, r.RotationHeight)
	require.EqualValues(t, 10, r.WindowOpenHeight)
	require.EqualValues(t, 0, r.KeyCutoffHeight)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_SCHEDULED, r.Status)
	requireEvent(t, f.ctx, types.EventTypeRoundScheduled, types.AttributeKeyRotationHeight, "20")
	requireEvent(t, f.ctx, types.EventTypeRoundScheduled, types.AttributeKeyWindowOpenHeight, "10")
	requireEvent(t, f.ctx, types.EventTypeRoundScheduled, types.AttributeKeyRound, "1")
}

func TestAParameterChangeDoesNotMoveTheScheduledRound(t *testing.T) {
	f := newFixture(t)
	f.run(1, 1)

	require.NoError(t, f.k.Params.Set(f.ctx, types.Params{RotationEpochs: 2, WindowBlocks: 50}))
	f.part.epochLen = 7
	f.run(2, 8)

	r := f.round()
	require.EqualValues(t, 20, r.RotationHeight)
	require.EqualValues(t, 10, r.WindowOpenHeight)
}

func TestEpochLengthZeroLeavesTheRoundUnscheduled(t *testing.T) {
	f := newFixture(t)
	f.part.epochLen = 0

	f.run(1, 30)

	r := f.round()
	require.Zero(t, r.RotationHeight)
	require.Zero(t, r.WindowOpenHeight)
	require.EqualValues(t, 1, r.Number)

	f.part.epochLen = 5
	f.run(31, 31)

	r = f.round()
	require.EqualValues(t, 50, r.RotationHeight, "31+11=42, next multiple of 10")
	require.EqualValues(t, 40, r.WindowOpenHeight)
}

func TestEndBlockErrorsNeverEscape(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.CurrentRound.Remove(f.ctx))
	require.NoError(t, f.k.EndBlock(f.at(5)))
}
