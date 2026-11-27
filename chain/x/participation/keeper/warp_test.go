package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/participation/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// The WARP engine in ComputeEpoch (ADR-0009 §2, S10 rulings R4-R7).

func requireWarp(t *testing.T, f *fixture, val sdk.ValAddress, value string, lastEpoch uint64) {
	t.Helper()
	w, found, err := f.keeper.GetWarp(f.ctx, val)
	require.NoError(t, err)
	require.True(t, found, "no WARP stored for %s", val)
	require.Equal(t, math.LegacyMustNewDecFromStr(value), w.Value, "WARP value")
	require.Equal(t, lastEpoch, w.LastEpoch, "WARP last_epoch")
	require.Equal(t, val.String(), w.ValidatorAddress)
}

func requireSnapshotWarp(t *testing.T, f *fixture, epoch uint64, val sdk.ValAddress, value string) {
	t.Helper()
	snap, found, err := f.keeper.GetSnapshot(f.ctx, epoch, val)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, snap.Warp, "the snapshot of epoch %d carries no WARP", epoch)
	require.Equal(t, math.LegacyMustNewDecFromStr(value), *snap.Warp)
}

// R6: the first scored epoch starts from warp_initial (80) and applies that
// epoch's update; later epochs move from the stored value. R4: the snapshot
// pins the post-update value.
func TestComputeEpoch_WarpStartsAtInitialThenFollowsTheScore(t *testing.T) {
	val, fv := valFor("warp-init-op-seed-aa", "warp-init-cons-seedaa")
	f := initEpochFixture(t, mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}})

	_, found, err := f.keeper.GetWarp(f.ctx, val)
	require.NoError(t, err)
	require.False(t, found, "nothing before the first scored epoch")

	seedCounters(t, f, val, 100, 0) // PS 100
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))
	requireWarp(t, f, val, "82", 1) // 80 + 0.10·(100 − 80)
	requireSnapshotWarp(t, f, 1, val, "82")

	seedCounters(t, f, val, 50, 50) // PS 50
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 2))
	requireWarp(t, f, val, "61.2", 2) // 82 − 0.65·(82 − 50)
	requireSnapshotWarp(t, f, 2, val, "61.2")
	requireSnapshotWarp(t, f, 1, val, "82") // history is not rewritten
}

// ADR-0009's newcomer exposure: one dead first epoch takes 80 to 28.
func TestComputeEpoch_DeadFirstEpochTakesANewcomerTo28(t *testing.T) {
	val, fv := valFor("warp-dead-op-seed-aa", "warp-dead-cons-seedaa")
	f := initEpochFixture(t, mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}})

	seedCounters(t, f, val, 0, 100)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))
	requireWarp(t, f, val, "28", 1)
}

// R5: no snapshot (V-7) means no update. WARP is frozen while the validator is
// not ACTIVE, and moves again from the frozen value when it returns.
func TestComputeEpoch_WarpIsFrozenWhileNotActive(t *testing.T) {
	val, fv := valFor("warp-frz-op-seed-aaa", "warp-frz-cons-seed-aa")
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{val.String(): vrtypes.Status_STATUS_ACTIVE}}
	f := initEpochFixtureWithVR(t, mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}, vr)

	seedCounters(t, f, val, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))
	requireWarp(t, f, val, "82", 1)

	vr.statuses[val.String()] = vrtypes.Status_STATUS_QUARANTINED
	for e := uint64(2); e <= 4; e++ {
		seedCounters(t, f, val, 0, 100) // a dead epoch that is not scored
		require.NoError(t, f.keeper.ComputeEpoch(f.ctx, e))
		requireWarp(t, f, val, "82", 1)
	}

	vr.statuses[val.String()] = vrtypes.Status_STATUS_ACTIVE
	seedCounters(t, f, val, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 5))
	requireWarp(t, f, val, "83.8", 5) // 82 + 0.10·18, not a restart at 80
}

// The warp_updated event carries what an indexer needs to redraw the curve.
func TestComputeEpoch_EmitsWarpUpdated(t *testing.T) {
	val, fv := valFor("warp-evt-op-seed-aaa", "warp-evt-cons-seed-aa")
	f := initEpochFixture(t, mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}})

	seedCounters(t, f, val, 0, 100)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 3))

	var got map[string]string
	for _, ev := range sdk.UnwrapSDKContext(f.ctx).EventManager().Events() {
		if ev.Type != types.EventTypeWarpUpdated {
			continue
		}
		require.Nil(t, got, "one warp_updated per validator per epoch")
		got = map[string]string{}
		for _, a := range ev.Attributes {
			got[a.Key] = a.Value
		}
	}
	require.Equal(t, map[string]string{
		types.AttributeKeyValidator:  val.String(),
		types.AttributeKeyEpochID:    "3",
		types.AttributeKeyPS:         "0.000000000000000000",
		types.AttributeKeyWarpBefore: "80.000000000000000000",
		types.AttributeKeyWarpAfter:  "28.000000000000000000",
		types.AttributeKeyEligible:   "false",
	}, got)
}

// WARP never gets in EP's way: with WARP parameters that do not validate (a
// store that skipped the migration), the epoch still writes the snapshot and
// accrues EP, and WARP is left alone.
func TestComputeEpoch_InvalidWarpParamsLeaveEPAlone(t *testing.T) {
	val, fv := valFor("warp-bad-op-seed-aaa", "warp-bad-cons-seed-aa")
	f := initEpochFixture(t, mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}})
	p := types.DefaultParams()
	p.WarpRise = math.LegacyZeroDec()
	require.Error(t, p.Validate())
	require.NoError(t, f.keeper.Params.Set(f.ctx, p))

	for e := uint64(1); e <= 2; e++ {
		seedCounters(t, f, val, 100, 0)
		require.NoError(t, f.keeper.ComputeEpoch(f.ctx, e))
	}
	ep, found, err := f.keeper.GetEP(f.ctx, val)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(1), ep.AccumulatedEp.Int64(), "grace, then +1")
	snap, found, err := f.keeper.GetSnapshot(f.ctx, 2, val)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, snap.Warp)
	_, found, err = f.keeper.GetWarp(f.ctx, val)
	require.NoError(t, err)
	require.False(t, found)
}

// R7: ZeroReputation sets WARP to 0, keeps the epoch it was last measured in,
// and zeroes the in-flight snapshot's warp with its score.
func TestZeroReputation_ZeroesWarp(t *testing.T) {
	f, val := zeroRepFixture(t)
	seedCounters(t, f, val, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 7))
	requireWarp(t, f, val, "82", 7)

	require.NoError(t, f.keeper.ZeroReputation(f.ctx, val, 7))
	requireWarp(t, f, val, "0", 7)
	requireSnapshotWarp(t, f, 7, val, "0")

	msg, broken := f.keeper.WarpInvariant(f.ctx)
	require.False(t, broken, msg)
}

// R7 with nothing stored yet: the zero is written, so a validator punished
// before its first scored epoch does not later start at warp_initial. When it
// is ACTIVE again it climbs from 0 (R5 kept it there meanwhile).
func TestZeroReputation_ZeroesAnUnmeasuredValidator(t *testing.T) {
	f, val := zeroRepFixture(t)

	require.NoError(t, f.keeper.ZeroReputation(f.ctx, val, 4))
	requireWarp(t, f, val, "0", 0)
	_, found, err := f.keeper.GetSnapshot(f.ctx, 4, val)
	require.NoError(t, err)
	require.False(t, found, "no snapshot is invented")
	msg, broken := f.keeper.WarpInvariant(f.ctx)
	require.False(t, broken, msg)

	seedCounters(t, f, val, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 5))
	requireWarp(t, f, val, "10", 5) // 0 + 0.10·100
}

// A snapshot from before the engine has no warp; punishment does not invent one.
func TestZeroReputation_LeavesAPreWarpSnapshotWithoutWarp(t *testing.T) {
	f, val := zeroRepFixture(t)
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 7, val, types.ParticipationScoreSnapshot{
		ValidatorAddress: val.String(), EpochId: 7,
		ComputedScore: math.LegacyOneDec(), UptimeFraction: math.LegacyOneDec(),
	}))
	require.NoError(t, f.keeper.ZeroReputation(f.ctx, val, 7))
	snap, _, err := f.keeper.GetSnapshot(f.ctx, 7, val)
	require.NoError(t, err)
	require.Nil(t, snap.Warp)
	require.True(t, snap.ComputedScore.IsZero())
}

// V-16, checked and not halted.
func TestWarpInvariant(t *testing.T) {
	val, fv := valFor("warp-inv-op-seed-aaa", "warp-inv-cons-seed-aa")
	other, _ := valFor("warp-inv-op-seed-bbb", "warp-inv-cons-seed-bb")
	newF := func(t *testing.T) *fixture {
		f := initEpochFixture(t, mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}})
		seedCounters(t, f, val, 100, 0)
		require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))
		return f
	}

	t.Run("holds after an epoch", func(t *testing.T) {
		f := newF(t)
		msg, broken := f.keeper.WarpInvariant(f.ctx)
		require.False(t, broken, msg)
	})
	for _, c := range []struct {
		name string
		row  func() (sdk.ValAddress, types.WarpState)
		want string
	}{
		{"above 100", func() (sdk.ValAddress, types.WarpState) {
			return val, types.WarpState{ValidatorAddress: val.String(), Value: math.LegacyNewDec(101), LastEpoch: 1}
		}, "outside [0, 100]"},
		{"negative", func() (sdk.ValAddress, types.WarpState) {
			return val, types.WarpState{ValidatorAddress: val.String(), Value: math.LegacyNewDec(-1), LastEpoch: 1}
		}, "outside [0, 100]"},
		{"no snapshot at its last epoch", func() (sdk.ValAddress, types.WarpState) {
			return other, types.WarpState{ValidatorAddress: other.String(), Value: math.LegacyNewDec(70), LastEpoch: 1}
		}, "no snapshot at its last epoch 1"},
		{"unmeasured and not zero", func() (sdk.ValAddress, types.WarpState) {
			return other, types.WarpState{ValidatorAddress: other.String(), Value: math.LegacyNewDec(80), LastEpoch: 0}
		}, "never scored but holds 80"},
		{"keyed under another validator", func() (sdk.ValAddress, types.WarpState) {
			return val, types.WarpState{ValidatorAddress: other.String(), Value: math.LegacyNewDec(82), LastEpoch: 1}
		}, "names " + other.String()},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newF(t)
			k, row := c.row()
			require.NoError(t, f.keeper.SetWarp(f.ctx, k, row))
			msg, broken := f.keeper.WarpInvariant(f.ctx)
			require.True(t, broken)
			require.Contains(t, msg, c.want)
		})
	}
}
