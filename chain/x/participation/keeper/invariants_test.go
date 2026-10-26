package keeper_test

import (
	"testing"

	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/participation/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// TestPSActiveOnlyInvariant_Clean: after a normal epoch (all active) the V-7
// invariant is not broken.
func TestPSActiveOnlyInvariant_Clean(t *testing.T) {
	valA, fvA := valFor("v7-inv-clean-op-seed", "v7-inv-clean-cons-se")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fvA}}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{valA.String(): vrtypes.Status_STATUS_ACTIVE}}
	f := initEpochFixtureWithVR(t, sk, vr)

	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))

	_, broken := f.keeper.PSActiveOnlyInvariant(f.ctx, 1)
	require.False(t, broken)
}

// TestPSActiveOnlyInvariant_Violation: a PS snapshot written directly for a
// non-active validator (bypassing the ComputeEpoch gate) breaks the invariant.
func TestPSActiveOnlyInvariant_Violation(t *testing.T) {
	valB, _ := valFor("v7-inv-viol-op-seedb", "v7-inv-viol-cons-seb")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{}}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{valB.String(): vrtypes.Status_STATUS_QUARANTINED}}
	f := initEpochFixtureWithVR(t, sk, vr)

	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 1, valB, types.ParticipationScoreSnapshot{
		ValidatorAddress: valB.String(),
		EpochId:          1,
	}))

	msg, broken := f.keeper.PSActiveOnlyInvariant(f.ctx, 1)
	require.True(t, broken)
	require.Contains(t, msg, valB.String())
}
