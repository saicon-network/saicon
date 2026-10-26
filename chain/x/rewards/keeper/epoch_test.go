package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// epochLen is the epoch length the mock participation keeper reports for these
// MaybeRunEpoch tests; height N is an epoch boundary when N%epochLen==0 && N>1.
const epochTestLen uint64 = 10

// Non-boundary height: nothing happens — no distribution, no panic, no mints,
// no snapshots written.
func TestMaybeRunEpoch_NonBoundaryNoop(t *testing.T) {
	va := valAddr(50) // would be fully eligible if a boundary were hit
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{
		ep:       map[string]math.Int{va.String(): math.NewInt(5)},
		epochLen: epochTestLen,
	}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())

	// height 15 is not divisible by epochLen=10 -> no-op.
	require.NotPanics(t, func() {
		require.NoError(t, f.keeper.MaybeRunEpoch(f.ctx, 15))
	})

	require.Empty(t, f.bank.minted)
	require.Empty(t, f.bank.sends)

	// No snapshot written for any epoch the height could map to.
	_, found, err := f.keeper.GetRewardSnapshot(f.ctx, 1, va)
	require.NoError(t, err)
	require.False(t, found)
}

// Boundary height with an eligible validator: reward minted + snapshot written,
// no panic. epochID = height/epochLen.
func TestMaybeRunEpoch_BoundaryDistributes(t *testing.T) {
	va := valAddr(51)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{
		ep:       map[string]math.Int{va.String(): math.NewInt(8)},
		epochLen: epochTestLen,
	}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())

	const height int64 = 30                  // boundary: 30%10==0, 30>1
	epochID := uint64(height) / epochTestLen // = 3

	require.NotPanics(t, func() {
		require.NoError(t, f.keeper.MaybeRunEpoch(f.ctx, height))
	})

	// The pool is the epoch's share of the annual security budget, so it scales
	// with the epoch length: this fixture's epochs are 10 blocks, ten times
	// shorter than the 100-block fixtures, so each carries a tenth of testPool.
	expected := sdk.NewCoins(sdk.NewCoin(types.RewardDenom, testPool.QuoRaw(10)))
	require.Len(t, f.bank.minted, 1)
	require.True(t, f.bank.minted[0].Equal(expected),
		"expected %s, got %s", expected, f.bank.minted[0])
	require.Len(t, f.bank.sends, 1)
	require.Equal(t, sdk.AccAddress(va.Bytes()), f.bank.sends[0].recipient)

	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, epochID, va)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, snap.Eligible)
	require.Equal(t, expected.String(), snap.Amount)
}

// Broken V-2 at a boundary panics. We pre-seed a rogue eligible snapshot (positive
// amount) for a validator whose EP is 0, then call MaybeRunEpoch at the boundary
// for that snapshot's epoch. DistributeEpochRewards is a no-op for this epoch (no
// bonded validators), so the rogue snapshot survives to V-2, which must panic.
func TestMaybeRunEpoch_PanicsOnBrokenInvariant(t *testing.T) {
	bad := valAddr(52)

	// No bonded validators -> DistributeEpochRewards writes nothing and cannot
	// overwrite the rogue snapshot before V-2 runs.
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{}}
	pk := mockParticipationKeeper{
		ep:       map[string]math.Int{bad.String(): math.ZeroInt()}, // EP=0 -> positive reward is illegal
		epochLen: epochTestLen,
	}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{}}
	cp := mockComplianceKeeper{records: map[string]bool{}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())

	const height int64 = 40                  // boundary
	epochID := uint64(height) / epochTestLen // = 4

	// Rogue snapshot for the epoch MaybeRunEpoch will audit (simulates a future
	// emission-gate regression that minted despite EP=0).
	rogue := types.RewardSnapshot{
		ValidatorAddress: bad.String(),
		EpochId:          epochID,
		Amount:           sdk.NewCoins(sdk.NewCoin(types.RewardDenom, math.NewInt(1000))).String(),
		Eligible:         true,
		Reason:           "eligible",
	}
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, epochID, bad, rogue))

	require.Panics(t, func() {
		_ = f.keeper.MaybeRunEpoch(f.ctx, height)
	}, "MaybeRunEpoch must panic when V-2 is broken at an epoch boundary")
}

// epochLen=0 -> no-op (epochs disabled / participation not configured).
func TestMaybeRunEpoch_EpochLenZeroNoop(t *testing.T) {
	va := valAddr(53)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{
		ep:       map[string]math.Int{va.String(): math.NewInt(5)},
		epochLen: 0, // disabled
	}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())

	require.NotPanics(t, func() {
		require.NoError(t, f.keeper.MaybeRunEpoch(f.ctx, 100))
	})
	require.Empty(t, f.bank.minted)
	require.Empty(t, f.bank.sends)
}
