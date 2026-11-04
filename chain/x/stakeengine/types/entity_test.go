package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func TestEntityStandingRoom(t *testing.T) {
	s := types.EntityStanding{EntityID: "acme", Cap: math.NewInt(100), Tokens: math.NewInt(120)}
	require.Equal(t, math.NewInt(-20), s.Room(), "room may be negative")
}

func TestEntityStandingRoomFor(t *testing.T) {
	vroom := math.NewInt(100)
	for _, tc := range []struct {
		name string
		s    types.EntityStanding
		want int64
	}{
		{"frozen", types.EntityStanding{EntityID: "acme", Frozen: true, Cap: math.NewInt(100), Tokens: math.ZeroInt()}, 0},
		{"frozen seat with no entity", types.EntityStanding{Frozen: true, Cap: math.ZeroInt(), Tokens: math.ZeroInt()}, 0},
		{"entity room below the validator's", types.EntityStanding{EntityID: "acme", Cap: math.NewInt(100), Tokens: math.NewInt(70)}, 30},
		{"entity room above the validator's", types.EntityStanding{EntityID: "acme", Cap: math.NewInt(1000), Tokens: math.NewInt(70)}, 100},
		{"entity over its cap", types.EntityStanding{EntityID: "acme", Cap: math.NewInt(100), Tokens: math.NewInt(120)}, 0},
		{"no entity", types.EntityStanding{Cap: math.ZeroInt(), Tokens: math.ZeroInt()}, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, math.NewInt(tc.want), tc.s.RoomFor(vroom))
		})
	}
}

func TestCheckEntityGrowth(t *testing.T) {
	std := types.EntityStanding{EntityID: "acme", Cap: math.NewInt(100), Tokens: math.NewInt(90)}
	require.NoError(t, types.CheckEntityGrowth(std, math.NewInt(90), math.NewInt(100)), "growth to the cap")
	require.ErrorIs(t, types.CheckEntityGrowth(std, math.NewInt(90), math.NewInt(101)), types.ErrEntityCapReached)
	require.NoError(t, types.CheckEntityGrowth(std, math.NewInt(150), math.NewInt(120)), "shrinking above the cap")

	frozen := std
	frozen.Frozen = true
	require.ErrorIs(t, types.CheckEntityGrowth(frozen, math.NewInt(10), math.NewInt(11)), types.ErrEntityFrozen)
	require.NoError(t, types.CheckEntityGrowth(frozen, math.NewInt(10), math.NewInt(10)), "no growth, no refusal")
	require.NoError(t, types.CheckEntityGrowth(frozen, math.NewInt(10), math.NewInt(5)), "a frozen entity may shrink")

	none := types.EntityStanding{Cap: math.ZeroInt(), Tokens: math.ZeroInt()}
	require.NoError(t, types.CheckEntityGrowth(none, math.NewInt(10), math.NewInt(1000)), "no entity, no entity cap")
}
