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

func TestCheckEntityGrowthAcrossStandings(t *testing.T) {
	limited := types.EntityStanding{EntityID: "acme", Tokens: math.NewInt(90), Cap: math.NewInt(100)}
	frozen := types.EntityStanding{Frozen: true, Reason: "r", Tokens: math.ZeroInt(), Cap: math.ZeroInt()}
	free := types.EntityStanding{Tokens: math.ZeroInt(), Cap: math.ZeroInt()}
	require.NoError(t, types.CheckEntityGrowth(limited, math.NewInt(90), math.NewInt(100)))
	require.ErrorIs(t, types.CheckEntityGrowth(limited, math.NewInt(90), math.NewInt(101)), types.ErrEntityCapReached)
	require.ErrorContains(t, types.CheckEntityGrowth(limited, math.NewInt(90), math.NewInt(101)), "entity acme's aggregate stake")
	require.NoError(t, types.CheckEntityGrowth(limited, math.NewInt(120), math.NewInt(110)), "shrinking above the cap is allowed")
	require.ErrorIs(t, types.CheckEntityGrowth(frozen, math.ZeroInt(), math.NewInt(1)), types.ErrEntityFrozen)
	require.NoError(t, types.CheckEntityGrowth(frozen, math.NewInt(5), math.NewInt(5)))
	require.NoError(t, types.CheckEntityGrowth(free, math.ZeroInt(), math.NewInt(1_000_000)))
}
