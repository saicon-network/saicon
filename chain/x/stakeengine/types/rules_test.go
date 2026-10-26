package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func TestCheckGrowthOnlyFailsOnGrowthPastTheLimit(t *testing.T) {
	l := math.NewInt(100)
	require.NoError(t, types.CheckGrowth(math.NewInt(90), math.NewInt(100), l, types.ErrGlobalCapReached, "x")) // at the limit
	require.ErrorIs(t, types.CheckGrowth(math.NewInt(90), math.NewInt(101), l, types.ErrGlobalCapReached, "x"), types.ErrGlobalCapReached)
	require.NoError(t, types.CheckGrowth(math.NewInt(150), math.NewInt(120), l, types.ErrGlobalCapReached, "x")) // above, but shrinking
	require.NoError(t, types.CheckGrowth(math.NewInt(150), math.NewInt(150), l, types.ErrGlobalCapReached, "x")) // above, unchanged
}

func TestCheckSelfBondFloor(t *testing.T) {
	f := types.EntryFloor()
	require.NoError(t, types.CheckSelfBondFloor(math.ZeroInt()))
	require.NoError(t, types.CheckSelfBondFloor(f))
	require.ErrorIs(t, types.CheckSelfBondFloor(f.SubRaw(1)), types.ErrSelfBondBelowFloor)
	require.ErrorIs(t, types.CheckSelfBondFloor(math.OneInt()), types.ErrSelfBondBelowFloor)
}

func TestCheckNewSlot(t *testing.T) {
	slots := []string{"a", "b", "c"}
	require.NoError(t, types.CheckNewSlot(slots, "b", 3))
	require.ErrorIs(t, types.CheckNewSlot(slots, "d", 3), types.ErrTooManyValidators)
	require.NoError(t, types.CheckNewSlot(slots[:2], "d", 3))
}

func TestCheckMinDelegation(t *testing.T) {
	m := math.NewInt(100)
	require.NoError(t, types.CheckMinDelegation(m, m))
	require.ErrorIs(t, types.CheckMinDelegation(m.SubRaw(1), m), types.ErrBelowMinDelegation)
}
