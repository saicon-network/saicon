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

// Good state: after a normal distribution every eligible (minted) snapshot
// belongs to a validator with EP>0, so V-2 must hold.
func TestRewardGateInvariant_GoodState(t *testing.T) {
	good := valAddr(40) // eligible, EP>0
	noEP := valAddr(41) // EP=0 -> ineligible (no positive snapshot)

	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{
		fakeValidator{operator: good.String()},
		fakeValidator{operator: noEP.String()},
	}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{
		good.String(): math.NewInt(7),
		noEP.String(): math.ZeroInt(),
	}}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{
		good.String(): vrtypes.Status_STATUS_ACTIVE,
		noEP.String(): vrtypes.Status_STATUS_ACTIVE,
	}}
	cp := mockComplianceKeeper{records: map[string]bool{}}

	f := initEmissionFixture(t, sk, pk, vr, cp, types.DefaultParams())
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	msg, broken := f.keeper.RewardGateInvariant(f.ctx, testEpoch)
	require.False(t, broken, "V-2 must hold after a normal distribution, got: %s", msg)
	require.Empty(t, msg)
}

// Bad state: a RewardSnapshot with a positive amount is crafted for a validator
// whose EP is 0. V-2 must detect it and name the validator.
func TestRewardGateInvariant_BrokenState(t *testing.T) {
	bad := valAddr(42)

	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{}}
	// EP is exactly 0 for the offending validator -> a positive reward is illegal.
	pk := mockParticipationKeeper{ep: map[string]math.Int{bad.String(): math.ZeroInt()}}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{}}
	cp := mockComplianceKeeper{records: map[string]bool{}}

	f := initEmissionFixture(t, sk, pk, vr, cp, types.DefaultParams())

	// Manually write an eligible snapshot with a positive amount (simulates a
	// future emission-gate regression that minted despite EP=0).
	rogue := types.RewardSnapshot{
		ValidatorAddress: bad.String(),
		EpochId:          testEpoch,
		Amount:           sdk.NewCoins(sdk.NewCoin(types.RewardDenom, math.NewInt(1000))).String(),
		Eligible:         true,
		Reason:           "eligible",
	}
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, testEpoch, bad, rogue))

	msg, broken := f.keeper.RewardGateInvariant(f.ctx, testEpoch)
	require.True(t, broken, "V-2 must be broken when a positive reward exists with EP=0")
	require.Contains(t, msg, bad.String(), "message must name the offending validator")
}

// Bad state: positive snapshot for a validator with NO EP record at all (missing
// EP is treated as not-positive).
func TestRewardGateInvariant_BrokenMissingEP(t *testing.T) {
	bad := valAddr(43)

	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{}} // no record
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{}}
	cp := mockComplianceKeeper{records: map[string]bool{}}

	f := initEmissionFixture(t, sk, pk, vr, cp, types.DefaultParams())

	rogue := types.RewardSnapshot{
		ValidatorAddress: bad.String(),
		EpochId:          testEpoch,
		Amount:           sdk.NewCoins(sdk.NewCoin(types.RewardDenom, math.NewInt(500))).String(),
		Eligible:         true,
		Reason:           "eligible",
	}
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, testEpoch, bad, rogue))

	msg, broken := f.keeper.RewardGateInvariant(f.ctx, testEpoch)
	require.True(t, broken)
	require.Contains(t, msg, bad.String())
}

// Ineligible snapshots (zero amount) never trip V-2, even when EP=0 — that is
// exactly the expected EP=0 -> reward=0 outcome.
func TestRewardGateInvariant_IneligibleSnapshotsIgnored(t *testing.T) {
	z := valAddr(44)

	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{z.String(): math.ZeroInt()}}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{}}
	cp := mockComplianceKeeper{records: map[string]bool{}}

	f := initEmissionFixture(t, sk, pk, vr, cp, types.DefaultParams())

	ineligible := types.RewardSnapshot{
		ValidatorAddress: z.String(),
		EpochId:          testEpoch,
		Amount:           "",
		Eligible:         false,
		Reason:           "EP=0",
	}
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, testEpoch, z, ineligible))

	msg, broken := f.keeper.RewardGateInvariant(f.ctx, testEpoch)
	require.False(t, broken, "ineligible/zero snapshots must not trip V-2, got: %s", msg)
	require.Empty(t, msg)
}

// Empty epoch (no snapshots) is trivially not broken.
func TestRewardGateInvariant_EmptyEpoch(t *testing.T) {
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{}}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{}}
	cp := mockComplianceKeeper{records: map[string]bool{}}

	f := initEmissionFixture(t, sk, pk, vr, cp, types.DefaultParams())

	msg, broken := f.keeper.RewardGateInvariant(f.ctx, 999)
	require.False(t, broken)
	require.Empty(t, msg)
}
