package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"cosmossdk.io/math"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/participation/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// ADR-0010 §4 — the revised V-8. Punishment MAY zero reputation (WARP, and the
// in-flight epoch's PS snapshot); it may NEVER touch EP, and never a closed
// epoch's snapshot.
//
// ZeroReputation is the single named write path for that, so the rule can be
// checked at one place instead of being a property of every caller.

func zeroRepFixture(t *testing.T) (*fixture, sdk.ValAddress) {
	t.Helper()
	val, fv := valFor("v8-zerorep-op-seedaa", "v8-zerorep-cons-seed")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{val.String(): vrtypes.Status_STATUS_ACTIVE}}
	return initEpochFixtureWithVR(t, sk, vr), val
}

// The in-flight epoch's snapshot is forced to zero — that is what "PS → 0"
// means for a value that is otherwise an immutable per-epoch record.
func TestZeroReputation_ZeroesTheInFlightSnapshot(t *testing.T) {
	f, val := zeroRepFixture(t)

	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 7, val, types.ParticipationScoreSnapshot{
		ValidatorAddress: val.String(),
		EpochId:          7,
		BlocksSigned:     99,
		BlocksMissed:     1,
		UptimeFraction:   math.LegacyNewDecWithPrec(99, 2),
		ComputedScore:    math.LegacyNewDecWithPrec(99, 2),
	}))

	require.NoError(t, f.keeper.ZeroReputation(f.ctx, val, 7))

	snap, found, err := f.keeper.GetSnapshot(f.ctx, 7, val)
	require.NoError(t, err)
	require.True(t, found, "the record stays — zeroed, not deleted, so the audit trail survives")
	require.True(t, snap.ComputedScore.IsZero(), "score must be zero, got %s", snap.ComputedScore)
	require.True(t, snap.UptimeFraction.IsZero(), "uptime must be zero, got %s", snap.UptimeFraction)
}

// EP is the long-run accumulator and the reward gate. A double-sign already
// makes the validator permanently reward-ineligible through C=0 + JAILED +
// tombstone, so zeroing EP adds no deterrence — and it would destroy the only
// long-run record of how much this validator actually participated.
func TestZeroReputation_NeverTouchesEP(t *testing.T) {
	f, val := zeroRepFixture(t)

	require.NoError(t, f.keeper.SetEP(f.ctx, val, types.EmpiricalPointsState{
		ValidatorAddress: val.String(),
		AccumulatedEp:    math.NewInt(42),
		LastEpochUpdate:  6,
	}))
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 7, val, types.ParticipationScoreSnapshot{
		ValidatorAddress: val.String(), EpochId: 7,
		ComputedScore: math.LegacyOneDec(), UptimeFraction: math.LegacyOneDec(),
	}))

	require.NoError(t, f.keeper.ZeroReputation(f.ctx, val, 7))

	ep, found, err := f.keeper.GetEP(f.ctx, val)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, ep.AccumulatedEp.Equal(math.NewInt(42)),
		"punishment must never mutate EP (invariant V-8), got %s", ep.AccumulatedEp)
	require.Equal(t, uint64(6), ep.LastEpochUpdate, "not even the bookkeeping field")
}

// Closed epochs are history. Zeroing epoch 7 must not reach back into 6.
func TestZeroReputation_LeavesClosedEpochsAlone(t *testing.T) {
	f, val := zeroRepFixture(t)

	past := types.ParticipationScoreSnapshot{
		ValidatorAddress: val.String(), EpochId: 6,
		BlocksSigned: 100, ComputedScore: math.LegacyOneDec(), UptimeFraction: math.LegacyOneDec(),
	}
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 6, val, past))
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 7, val, types.ParticipationScoreSnapshot{
		ValidatorAddress: val.String(), EpochId: 7,
		ComputedScore: math.LegacyOneDec(), UptimeFraction: math.LegacyOneDec(),
	}))

	require.NoError(t, f.keeper.ZeroReputation(f.ctx, val, 7))

	got, found, err := f.keeper.GetSnapshot(f.ctx, 6, val)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, got.ComputedScore.Equal(math.LegacyOneDec()),
		"a closed epoch must never be rewritten, got %s", got.ComputedScore)
	require.Equal(t, uint64(100), got.BlocksSigned)
}

// A validator with no snapshot for the in-flight epoch is a no-op: punishment
// runs inside a slashing hook and must never fail or halt the chain.
func TestZeroReputation_NoSnapshotIsANoop(t *testing.T) {
	f, val := zeroRepFixture(t)
	require.NoError(t, f.keeper.ZeroReputation(f.ctx, val, 9))

	_, found, err := f.keeper.GetSnapshot(f.ctx, 9, val)
	require.NoError(t, err)
	require.False(t, found, "nothing is invented for an epoch that never scored this validator")
}

// Calling twice must be safe — a slashing hook can fire more than once.
func TestZeroReputation_IsIdempotent(t *testing.T) {
	f, val := zeroRepFixture(t)
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 3, val, types.ParticipationScoreSnapshot{
		ValidatorAddress: val.String(), EpochId: 3,
		ComputedScore: math.LegacyOneDec(), UptimeFraction: math.LegacyOneDec(),
	}))

	require.NoError(t, f.keeper.ZeroReputation(f.ctx, val, 3))
	require.NoError(t, f.keeper.ZeroReputation(f.ctx, val, 3))

	snap, _, err := f.keeper.GetSnapshot(f.ctx, 3, val)
	require.NoError(t, err)
	require.True(t, snap.ComputedScore.IsZero())
}
