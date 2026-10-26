package keeper_test

import (
	"context"
	"testing"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/participation/keeper"
	module "github.com/saicon-network/saicon/chain/x/participation/module"
	"github.com/saicon-network/saicon/chain/x/participation/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// --- mock validatorregistry keeper (V-7 status gate) ----------------------

// mockVRKeeper reports a per-validator SAiCoN status. A validator absent from
// the map defaults to ACTIVE, so existing epoch tests (which don't configure
// statuses) keep treating every bonded validator as active.
type mockVRKeeper struct {
	statuses map[string]vrtypes.Status
}

func (m mockVRKeeper) GetValidatorStatus(_ context.Context, addr sdk.ValAddress) (vrtypes.Status, bool, error) {
	if m.statuses != nil {
		if s, ok := m.statuses[addr.String()]; ok {
			return s, true, nil
		}
	}
	return vrtypes.Status_STATUS_ACTIVE, true, nil
}

// --- mock validator -------------------------------------------------------

// fakeValidator embeds ValidatorI (nil) and overrides only the methods
// ComputeEpoch uses: GetOperator and GetConsAddr. Any other call would panic,
// which is acceptable for these focused tests.
type fakeValidator struct {
	stakingtypes.ValidatorI
	operator string
	consAddr sdk.ConsAddress
}

func (f fakeValidator) GetOperator() string          { return f.operator }
func (f fakeValidator) GetConsAddr() ([]byte, error) { return f.consAddr.Bytes(), nil }

// --- mock staking keeper --------------------------------------------------

type mockStakingKeeper struct {
	validators []stakingtypes.ValidatorI
}

func (m mockStakingKeeper) IterateBondedValidatorsByPower(_ context.Context, fn func(index int64, validator stakingtypes.ValidatorI) (stop bool)) error {
	for i, v := range m.validators {
		if fn(int64(i), v) {
			break
		}
	}
	return nil
}

// GetValidatorByConsAddr is a stub satisfying the updated StakingKeeper interface
// (added in M1-B). Not used by current tests — returns ErrNoValidatorFound.
func (m mockStakingKeeper) GetValidatorByConsAddr(_ context.Context, _ sdk.ConsAddress) (stakingtypes.Validator, error) {
	return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
}

// --- fixture with injected mock keepers -----------------------------------

func initEpochFixture(t *testing.T, sk types.StakingKeeper) *fixture {
	return initEpochFixtureWithVR(t, sk, mockVRKeeper{})
}

func initEpochFixtureWithVR(t *testing.T, sk types.StakingKeeper, vr types.ValidatorregistryKeeper) *fixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)

	storeService := runtime.NewKVStoreService(storeKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_test")).Ctx

	authority := authtypes.NewModuleAddress(types.GovModuleName)

	k := keeper.NewKeeper(
		storeService,
		encCfg.Codec,
		addressCodec,
		authority,
		vr,
		sk,
	)
	if err := k.Params.Set(ctx, types.DefaultParams()); err != nil {
		t.Fatalf("failed to set params: %v", err)
	}

	return &fixture{ctx: ctx, keeper: k, addressCodec: addressCodec}
}

func valFor(opSeed, consSeed string) (sdk.ValAddress, fakeValidator) {
	valAddr := sdk.ValAddress([]byte(opSeed))[:20]
	consAddr := sdk.ConsAddress([]byte(consSeed))[:20]
	return valAddr, fakeValidator{
		operator: valAddr.String(),
		consAddr: consAddr,
	}
}

// seedCounters is a helper that sets epoch-window counters for the given
// validator directly into the store. Must be called before each ComputeEpoch
// call that should see non-zero block data (ComputeEpoch resets the counters
// at the end of each call, so subsequent epoch calls start from zero unless
// re-seeded).
func seedCounters(t *testing.T, f *fixture, valAddr sdk.ValAddress, signed, missed uint64) {
	t.Helper()
	if signed > 0 {
		require.NoError(t, f.keeper.EpochBlocksSigned.Set(f.ctx, valAddr.Bytes(), signed))
	}
	if missed > 0 {
		require.NoError(t, f.keeper.EpochBlocksMissed.Set(f.ctx, valAddr.Bytes(), missed))
	}
}

// assertCountersReset verifies that EpochBlocksSigned and EpochBlocksMissed
// have been cleared for the given validator (ErrNotFound or value == 0).
func assertCountersReset(t *testing.T, f *fixture, valAddr sdk.ValAddress) {
	t.Helper()
	uptime, s, m, err := f.keeper.EpochUptimeForTest(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, uint64(0), s, "signed counter should be 0 after reset")
	require.Equal(t, uint64(0), m, "missed counter should be 0 after reset")
	require.Equal(t, "1.000000000000000000", uptime.String(), "uptime should be 1 (no-blocks default) after reset")
}

// TestComputeEpoch_WritesSnapshotWithUptime: a validator that signed 90 of 100
// blocks (90 signed + 10 missed seeded directly) gets a snapshot with
// uptime = 90/100 = 0.9 and VotesParticipated == BlocksSigned.
func TestComputeEpoch_WritesSnapshotWithUptime(t *testing.T) {
	valAddr, fv := valFor("ps-uptime-op-seed-aa", "ps-uptime-cons-seedaa")
	const signed = uint64(90)
	const missed = uint64(10)

	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}
	f := initEpochFixture(t, sk)

	seedCounters(t, f, valAddr, signed, missed)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))

	snap, found, err := f.keeper.GetSnapshot(f.ctx, 1, valAddr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(90), snap.BlocksSigned)
	require.Equal(t, uint64(10), snap.BlocksMissed)
	require.Equal(t, "0.900000000000000000", snap.UptimeFraction.String())
	require.Equal(t, "0.900000000000000000", snap.ComputedScore.String())
	// VotesParticipated is now set to BlocksSigned (epoch-window counter source).
	require.Equal(t, uint64(90), snap.VotesParticipated)

	// Counters must be reset after ComputeEpoch.
	assertCountersReset(t, f, valAddr)
}

// TestComputeEpoch_NoBlocksMeansFullUptime: a validator with no epoch-counter
// entries is treated as total==0 → uptime=1 (benefit of the doubt).
func TestComputeEpoch_NoBlocksMeansFullUptime(t *testing.T) {
	valAddr, fv := valFor("ps-nosi-op-seed-bbbb", "ps-nosi-cons-seed-bbb")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}
	f := initEpochFixture(t, sk)

	// Do NOT seed any counters — simulate a validator with no observed blocks.
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))

	snap, found, err := f.keeper.GetSnapshot(f.ctx, 1, valAddr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(0), snap.BlocksSigned)
	require.Equal(t, uint64(0), snap.BlocksMissed)
	require.Equal(t, "1.000000000000000000", snap.UptimeFraction.String())
	require.Equal(t, "1.000000000000000000", snap.ComputedScore.String())
	// Counters must be reset (already zero, but verify no leftover state).
	assertCountersReset(t, f, valAddr)
}

// TestComputeEpoch_EPGraceThenIncrement: first observed epoch initializes EP=0
// (grace, no increment); a subsequent epoch with score>0 increments by 1.
// Counters are re-seeded before each epoch call because ComputeEpoch resets them.
func TestComputeEpoch_EPGraceThenIncrement(t *testing.T) {
	valAddr, fv := valFor("ps-grace-op-seed-cccc", "ps-grace-cons-seed-cc")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}
	f := initEpochFixture(t, sk)

	// Epoch 1: seed full uptime (100 signed / 0 missed) → score=1 → grace init, EP=0.
	seedCounters(t, f, valAddr, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))
	ep, found, err := f.keeper.GetEP(f.ctx, valAddr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(0), ep.AccumulatedEp.Int64())
	require.Equal(t, uint64(1), ep.LastEpochUpdate)
	assertCountersReset(t, f, valAddr)

	// Epoch 2: re-seed full uptime → score>0 => EP += 1.
	seedCounters(t, f, valAddr, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 2))
	ep, found, err = f.keeper.GetEP(f.ctx, valAddr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(1), ep.AccumulatedEp.Int64())
	require.Equal(t, uint64(2), ep.LastEpochUpdate)
	assertCountersReset(t, f, valAddr)

	// Epoch 3: re-seed full uptime → score>0 => EP += 1 again.
	seedCounters(t, f, valAddr, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 3))
	ep, _, err = f.keeper.GetEP(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, int64(2), ep.AccumulatedEp.Int64())
	assertCountersReset(t, f, valAddr)
}

// TestComputeEpoch_EPFloorOnZeroScore: when score==0 (missed all blocks), an
// already-accumulated EP decays but never below floor 1. With ep_decay=0.5,
// floor(2*0.5)=1 and floor(1*0.5) stays at 1 (floor enforcement).
func TestComputeEpoch_EPFloorOnZeroScore(t *testing.T) {
	valAddr, fv := valFor("ps-floor-op-seed-dddd", "ps-floor-cons-seed-dd")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}
	f := initEpochFixture(t, sk)
	require.NoError(t, f.keeper.Params.Set(f.ctx, types.NewParams(100, math.LegacyMustNewDecFromStr("0.5"))))

	// Epoch 1: grace, EP=0.
	seedCounters(t, f, valAddr, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))

	// Epoch 2: score>0 => EP=1.
	seedCounters(t, f, valAddr, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 2))

	// Epoch 3: score>0 => EP=2.
	seedCounters(t, f, valAddr, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 3))

	ep, _, err := f.keeper.GetEP(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, int64(2), ep.AccumulatedEp.Int64())

	// Epoch 4: miss every block (0 signed, 100 missed) → score=0, EP decays 2 → 1.
	seedCounters(t, f, valAddr, 0, 100)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 4))
	ep, _, err = f.keeper.GetEP(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, int64(1), ep.AccumulatedEp.Int64())
	require.Equal(t, "0.000000000000000000", mustScore(t, f, valAddr, 4))
	assertCountersReset(t, f, valAddr)

	// Epoch 5: miss every block again → decay hits floor, EP stays 1.
	seedCounters(t, f, valAddr, 0, 100)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 5))
	ep, _, err = f.keeper.GetEP(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, int64(1), ep.AccumulatedEp.Int64())
	assertCountersReset(t, f, valAddr)
}

// TestComputeEpoch_MultipleValidators: two bonded validators in the same epoch,
// one with full uptime and one partial. Each must get an INDEPENDENT snapshot
// (correct per-validator uptime) and an independent EP state — one validator's
// state must not bleed into the other's.
func TestComputeEpoch_MultipleValidators(t *testing.T) {
	valA, fvA := valFor("ps-multi-op-seed-aaaa", "ps-multi-cons-seedaaa")
	valB, fvB := valFor("ps-multi-op-seed-bbbb", "ps-multi-cons-seedbbb")

	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fvA, fvB}}
	f := initEpochFixture(t, sk)

	// valA: 100 signed / 0 missed => uptime 1.0
	// valB: 80 signed / 20 missed => uptime 0.8
	seedCounters(t, f, valA, 100, 0)
	seedCounters(t, f, valB, 80, 20)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))

	// Independent snapshots: A=1.0, B=0.8.
	snapA, found, err := f.keeper.GetSnapshot(f.ctx, 1, valA)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(100), snapA.BlocksSigned)
	require.Equal(t, uint64(0), snapA.BlocksMissed)
	require.Equal(t, "1.000000000000000000", snapA.UptimeFraction.String())
	require.Equal(t, "1.000000000000000000", snapA.ComputedScore.String())
	require.Equal(t, uint64(100), snapA.VotesParticipated)

	snapB, found, err := f.keeper.GetSnapshot(f.ctx, 1, valB)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(80), snapB.BlocksSigned)
	require.Equal(t, uint64(20), snapB.BlocksMissed)
	require.Equal(t, "0.800000000000000000", snapB.UptimeFraction.String())
	require.Equal(t, "0.800000000000000000", snapB.ComputedScore.String())
	require.Equal(t, uint64(80), snapB.VotesParticipated)

	// Independent EP: both score>0, both get their first-epoch grace (EP=0).
	epA, found, err := f.keeper.GetEP(f.ctx, valA)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(0), epA.AccumulatedEp.Int64())
	require.Equal(t, uint64(1), epA.LastEpochUpdate)

	epB, found, err := f.keeper.GetEP(f.ctx, valB)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(0), epB.AccumulatedEp.Int64())
	require.Equal(t, uint64(1), epB.LastEpochUpdate)

	// Counters must be reset for both validators after epoch 1.
	assertCountersReset(t, f, valA)
	assertCountersReset(t, f, valB)

	// Second epoch: re-seed both → both score>0 => each accumulates independently to EP=1.
	seedCounters(t, f, valA, 100, 0)
	seedCounters(t, f, valB, 80, 20)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 2))
	epA, _, err = f.keeper.GetEP(f.ctx, valA)
	require.NoError(t, err)
	require.Equal(t, int64(1), epA.AccumulatedEp.Int64())
	epB, _, err = f.keeper.GetEP(f.ctx, valB)
	require.NoError(t, err)
	require.Equal(t, int64(1), epB.AccumulatedEp.Int64())
}

// TestComputeEpoch_CountersResetAfterEpoch: after ComputeEpoch completes the
// per-epoch counters are cleared; a second call without re-seeding sees
// no-blocks (uptime=1) and produces a new snapshot reflecting that.
func TestComputeEpoch_CountersResetAfterEpoch(t *testing.T) {
	valAddr, fv := valFor("ps-reset-op-seed-eeee", "ps-reset-cons-seed-ee")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}
	f := initEpochFixture(t, sk)

	// Epoch 1: seed partial uptime.
	seedCounters(t, f, valAddr, 70, 30)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))

	snap1, found, err := f.keeper.GetSnapshot(f.ctx, 1, valAddr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(70), snap1.BlocksSigned)
	require.Equal(t, uint64(30), snap1.BlocksMissed)

	// Verify counters are cleared.
	assertCountersReset(t, f, valAddr)

	// Epoch 2: do NOT re-seed → uptime=1 (no-blocks default).
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 2))
	snap2, found, err := f.keeper.GetSnapshot(f.ctx, 2, valAddr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(0), snap2.BlocksSigned)
	require.Equal(t, uint64(0), snap2.BlocksMissed)
	require.Equal(t, "1.000000000000000000", snap2.UptimeFraction.String())
}

func mustScore(t *testing.T, f *fixture, valAddr sdk.ValAddress, epoch uint64) string {
	t.Helper()
	snap, found, err := f.keeper.GetSnapshot(f.ctx, epoch, valAddr)
	require.NoError(t, err)
	require.True(t, found)
	return snap.ComputedScore.String()
}

// TestComputeEpoch_NoDecayWhenParamZero: a validator builds EP over 3 signing
// epochs, then misses epoch 4; with default ep_decay=0 there is no decay, so EP
// is unchanged (decay is fully parameter-driven).
func TestComputeEpoch_NoDecayWhenParamZero(t *testing.T) {
	valAddr, fv := valFor("ps-nodecay-op-seedaa", "ps-nodecay-cons-seeda")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}
	f := initEpochFixture(t, sk) // default params: ep_decay = 0

	for e := uint64(1); e <= 3; e++ {
		seedCounters(t, f, valAddr, 100, 0)
		require.NoError(t, f.keeper.ComputeEpoch(f.ctx, e))
	}
	seedCounters(t, f, valAddr, 0, 100)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 4))
	ep, _, err := f.keeper.GetEP(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, int64(2), ep.AccumulatedEp.Int64())
}

// TestComputeEpoch_GapDecayOnReturn: a validator accrues EP, leaves the bonded
// set for several epochs, and is charged multiplicative decay for the gap when
// it returns and earns again.
func TestComputeEpoch_GapDecayOnReturn(t *testing.T) {
	valAddr, fv := valFor("ps-gap-op-seed-bbbb", "ps-gap-cons-seed-bbb")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}
	f := initEpochFixture(t, sk)
	require.NoError(t, f.keeper.Params.Set(f.ctx, types.NewParams(100, math.LegacyMustNewDecFromStr("0.5"))))

	require.NoError(t, f.keeper.SetEP(f.ctx, valAddr, types.EmpiricalPointsState{
		ValidatorAddress: valAddr.String(),
		AccumulatedEp:    math.NewInt(8),
		LastEpochUpdate:  2,
	}))

	// returns at epoch 5 and signs: idle = 5-2 = 3, gap charged = idle-1 = 2.
	// 8 * 0.5^2 = 2, then +1 = 3.
	seedCounters(t, f, valAddr, 100, 0)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 5))
	ep, _, err := f.keeper.GetEP(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, int64(3), ep.AccumulatedEp.Int64())
	require.Equal(t, uint64(5), ep.LastEpochUpdate)
}

// TestComputeEpoch_GapDecayOnZeroScoreReturn: a validator with prior EP returns
// after a multi-epoch gap but scores 0 on the return epoch — the full gap incl.
// this epoch is decayed (score==0 branch, idle>1).
func TestComputeEpoch_GapDecayOnZeroScoreReturn(t *testing.T) {
	valAddr, fv := valFor("ps-gapzero-op-seedcc", "ps-gapzero-cons-seec")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}
	f := initEpochFixture(t, sk)
	require.NoError(t, f.keeper.Params.Set(f.ctx, types.NewParams(100, math.LegacyMustNewDecFromStr("0.5"))))

	require.NoError(t, f.keeper.SetEP(f.ctx, valAddr, types.EmpiricalPointsState{
		ValidatorAddress: valAddr.String(),
		AccumulatedEp:    math.NewInt(8),
		LastEpochUpdate:  2,
	}))

	// returns at epoch 5 but misses all blocks: idle = 5-2 = 3, decay over full gap.
	// 8 * 0.5^3 = 1.0 -> floor 1.
	seedCounters(t, f, valAddr, 0, 100)
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 5))
	ep, _, err := f.keeper.GetEP(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, int64(1), ep.AccumulatedEp.Int64())
	require.Equal(t, uint64(5), ep.LastEpochUpdate)
}

// TestComputeEpoch_EmitsScoreComputedEvent: ComputeEpoch must emit exactly one
// types.EventTypeScoreComputed event per bonded validator, with attributes
// identifying the validator, epoch, and computed uptime/score/EP.
func TestComputeEpoch_EmitsScoreComputedEvent(t *testing.T) {
	valAddr, fv := valFor("ps-event-op-seed-aaaa", "ps-event-cons-seedaa")
	const signed = uint64(90)
	const missed = uint64(10)

	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fv}}
	f := initEpochFixture(t, sk)

	seedCounters(t, f, valAddr, signed, missed)

	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	require.NoError(t, f.keeper.ComputeEpoch(sdkCtx, 7))

	events := sdkCtx.EventManager().Events()
	var matches []sdk.Event
	for _, e := range events {
		if e.Type == types.EventTypeScoreComputed {
			matches = append(matches, e)
		}
	}
	require.Len(t, matches, 1, "expected exactly one %s event", types.EventTypeScoreComputed)

	attrs := map[string]string{}
	for _, a := range matches[0].Attributes {
		attrs[a.Key] = a.Value
	}
	require.Equal(t, valAddr.String(), attrs[types.AttributeKeyValidator])
	require.Equal(t, "7", attrs[types.AttributeKeyEpochID])
	require.Equal(t, "0.900000000000000000", attrs[types.AttributeKeyUptime])
	require.Equal(t, "0.900000000000000000", attrs[types.AttributeKeyScore])
	require.Contains(t, attrs, types.AttributeKeyEPAfter)
}
