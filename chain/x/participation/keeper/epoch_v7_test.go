package keeper_test

import (
	"testing"

	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// TestComputeEpoch_SkipsNonActiveValidator implements invariant V-7: PS/EP are
// computed only for SAiCoN-active validators. A bonded-but-quarantined validator
// must NOT receive a PS snapshot or accrue EP, even though it is in the staking
// bonded set.
func TestComputeEpoch_SkipsNonActiveValidator(t *testing.T) {
	valA, fvA := valFor("v7-active-op-seedaaa", "v7-active-cons-seedaa")
	valB, fvB := valFor("v7-quarant-op-seedbb", "v7-quarant-cons-seedb")

	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fvA, fvB}}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{
		valA.String(): vrtypes.Status_STATUS_ACTIVE,
		valB.String(): vrtypes.Status_STATUS_QUARANTINED,
	}}
	f := initEpochFixtureWithVR(t, sk, vr)

	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))

	// A (active) — snapshot + EP present.
	_, foundSnapA, err := f.keeper.GetSnapshot(f.ctx, 1, valA)
	require.NoError(t, err)
	require.True(t, foundSnapA, "active validator must get a PS snapshot")
	_, foundEpA, err := f.keeper.GetEP(f.ctx, valA)
	require.NoError(t, err)
	require.True(t, foundEpA, "active validator must accrue EP")

	// B (quarantined) — NO snapshot, NO EP.
	_, foundSnapB, err := f.keeper.GetSnapshot(f.ctx, 1, valB)
	require.NoError(t, err)
	require.False(t, foundSnapB, "quarantined validator must not get a PS snapshot (V-7)")
	_, foundEpB, err := f.keeper.GetEP(f.ctx, valB)
	require.NoError(t, err)
	require.False(t, foundEpB, "quarantined validator must not accrue EP (V-7)")
}
