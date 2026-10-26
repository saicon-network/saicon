package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/participation/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// TestV8_PunishmentDoesNotModifyEP implements invariant V-8: EP changes ONLY
// through participation (ComputeEpoch accrual/decay), never through a punishment
// event. Quarantining a validator (a punishment applied via the compliance flag +
// status) must NOT alter its accumulated EP — x/participation has no code path
// that lets compliance/slashing write EP (the only SetEP caller is ComputeEpoch).
// The V-7 gate simply freezes a non-active validator's EP (lazy decay applies only
// on return to ACTIVE), so several epochs of quarantine leave EP untouched.
func TestV8_PunishmentDoesNotModifyEP(t *testing.T) {
	valB, fvB := valFor("v8-punish-op-seed-bb", "v8-punish-cons-seedb")
	sk := mockStakingKeeper{validators: []stakingtypes.ValidatorI{fvB}}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{valB.String(): vrtypes.Status_STATUS_QUARANTINED}}
	f := initEpochFixtureWithVR(t, sk, vr)

	// Seed EP = 5 (as if earned while previously active).
	require.NoError(t, f.keeper.SetEP(f.ctx, valB, types.EmpiricalPointsState{
		ValidatorAddress: valB.String(),
		AccumulatedEp:    math.NewInt(5),
		LastEpochUpdate:  0,
	}))

	// Quarantined (punished): run several epochs — none may touch its EP.
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 1))
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 2))
	require.NoError(t, f.keeper.ComputeEpoch(f.ctx, 3))

	ep, found, err := f.keeper.GetEP(f.ctx, valB)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, math.NewInt(5), ep.AccumulatedEp, "punishment (quarantine) must not modify EP (V-8)")
	require.Equal(t, uint64(0), ep.LastEpochUpdate, "a skipped validator's EP state is left untouched")
}
