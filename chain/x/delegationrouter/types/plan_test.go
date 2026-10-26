package types_test

import (
	"bytes"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
)

func c(op byte, score string, tokens, room int64) types.Candidate {
	return types.Candidate{
		Operator:      sdk.ValAddress(bytes.Repeat([]byte{op}, 20)).String(),
		OperatorBytes: bytes.Repeat([]byte{op}, 20),
		Score:         math.LegacyMustNewDecFromStr(score),
		Tokens:        math.NewInt(tokens),
		Room:          math.NewInt(room),
	}
}

func ops(cs []types.Candidate) []string {
	var out []string
	for _, x := range cs {
		out = append(out, x.Operator)
	}
	return out
}

func amounts(legs []types.Leg) []int64 {
	var out []int64
	for _, l := range legs {
		out = append(out, l.Amount.Int64())
	}
	return out
}

func TestWeightIsScoreTimesRoomOverTokens(t *testing.T) {
	require.Equal(t, math.LegacyMustNewDecFromStr("0.4"), types.Weight(c(1, "0.8", 1000, 500)))
	// tokens 0 → divided by 1, not by zero
	require.Equal(t, math.LegacyMustNewDecFromStr("400"), types.Weight(c(1, "0.8", 0, 500)))
}

func TestChooseTakesTheHeaviestThenAddressOnTies(t *testing.T) {
	a, b, d := c(1, "1", 1000, 100), c(2, "1", 1000, 100), c(3, "1", 1000, 300)
	got := types.Choose([]types.Candidate{a, b, d}, nil, 3, 2)
	require.Equal(t, []string{d.Operator, a.Operator}, ops(got)) // d heaviest; a beats b on bytes
}

func TestChooseRespectsFreeSlots(t *testing.T) {
	a, b, d := c(1, "1", 1000, 100), c(2, "1", 1000, 200), c(3, "1", 1000, 300)
	held := map[string]bool{a.Operator: true}
	// one free slot: d (new, heaviest) and a (held) — not b (new, would need a second free slot)
	got := types.Choose([]types.Candidate{a, b, d}, held, 1, 2)
	require.Equal(t, []string{d.Operator, a.Operator}, ops(got))
	// no free slot: only held validators
	got = types.Choose([]types.Candidate{a, b, d}, held, 0, 2)
	require.Equal(t, []string{a.Operator}, ops(got))
}

func TestSplitIsProportionalAndWaterFills(t *testing.T) {
	// equal weights, rooms 100 and 1000, remainder 600: first round 300/300,
	// the first is capped at 100, its 200 goes to the second → 100 / 500
	x, y := c(1, "1", 1000, 100), c(2, "1", 1000, 1000)
	// force equal weight by giving x the same score*room/tokens as y
	x.Tokens = math.NewInt(100)
	legs, left := types.Split(math.NewInt(600), []types.Candidate{x, y})
	require.True(t, left.IsZero())
	require.Equal(t, math.NewInt(100), legs[0].Amount)
	require.Equal(t, math.NewInt(500), legs[1].Amount)
}

func TestSplitGivesTheTruncationRemainderToTheHeaviestLegWithRoom(t *testing.T) {
	// three-way split of 100 with equal weights: 33/33/33, remainder 1 → first leg
	a, b, d := c(1, "1", 1000, 1000), c(2, "1", 1000, 1000), c(3, "1", 1000, 1000)
	legs, left := types.Split(math.NewInt(100), []types.Candidate{a, b, d})
	require.True(t, left.IsZero())
	require.Equal(t, []int64{34, 33, 33}, amounts(legs))
}

func TestSplitReportsWhatDoesNotFit(t *testing.T) {
	a := c(1, "1", 1000, 50)
	legs, left := types.Split(math.NewInt(80), []types.Candidate{a})
	require.Equal(t, math.NewInt(50), legs[0].Amount)
	require.Equal(t, math.NewInt(30), left)
}

func TestSplitOfNothingIsNoLegs(t *testing.T) {
	legs, left := types.Split(math.ZeroInt(), []types.Candidate{c(1, "1", 1, 1)})
	require.Empty(t, legs)
	require.True(t, left.IsZero())
}

// Whatever the inputs, Split places sum(legs)+left == r, never over a room,
// never a zero leg, in the order of its input.
func TestSplitInvariants(t *testing.T) {
	cases := []struct {
		name string
		r    int64
		cs   []types.Candidate // already in weight order, as Choose returns them
	}{
		{"unequal weights", 1_000_003, []types.Candidate{c(1, "0.9", 100, 5_000_000), c(2, "0.8", 400, 5_000_000), c(3, "0.76", 900, 5_000_000)}},
		{"capped heaviest spills dust down", 1_000_001, []types.Candidate{c(1, "0.99", 10, 300_000), c(2, "0.8", 500, 4_000_000), c(3, "0.75", 700, 4_000_000)}},
		{"zero remainder", 900, []types.Candidate{c(1, "0.5", 1, 1_000), c(2, "0.5", 1, 1_000), c(3, "0.5", 1, 1_000)}},
		{"total room below r", 1_000_000, []types.Candidate{c(1, "0.9", 10, 200_000), c(2, "0.9", 20, 300_000)}},
		{"single leg", 777, []types.Candidate{c(4, "0.8", 3, 10_000)}},
		{"tiny weights, prime amount", 7919, []types.Candidate{c(1, "0.75", 999_999, 1), c(2, "0.75", 999_999, 5_000), c(3, "0.75", 999_999, 5_000)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := math.NewInt(tc.r)
			legs, left := types.Split(r, tc.cs)

			total := math.ZeroInt()
			pos := -1
			for _, l := range legs {
				require.True(t, l.Amount.IsPositive(), "zero or negative leg for %s", l.Validator)
				total = total.Add(l.Amount)
				at := -1
				for i, cand := range tc.cs {
					if cand.Operator == l.Validator {
						at = i
						require.True(t, l.Amount.LTE(cand.Room), "%s over its room", l.Validator)
					}
				}
				require.Greater(t, at, pos, "legs are not in input order")
				pos = at
			}
			require.False(t, left.IsNegative())
			require.True(t, total.Add(left).Equal(r), "sum %s + left %s != %s", total, left, r)
		})
	}
}

func TestSplitAtLeastFoldsATinyLeg(t *testing.T) {
	// a is heavy, b nearly weightless: the plain split gives b a dust leg.
	a, b := c(1, "1", 3, 3_000_000), c(2, "1", 1000, 1000) // weights 1e6 and 1
	cs := []types.Candidate{a, b}
	plain, _ := types.Split(math.NewInt(2_000_000), cs)
	require.Len(t, plain, 2)
	require.Equal(t, int64(1), plain[1].Amount.Int64())

	legs, left := types.SplitAtLeast(math.NewInt(2_000_000), cs, math.NewInt(100))
	require.True(t, left.IsZero())
	require.Equal(t, []int64{2_000_000}, amounts(legs))
	require.Equal(t, a.Operator, legs[0].Validator)
}

func TestSplitAtLeastLeavesAGoodSplitAlone(t *testing.T) {
	a, b := c(1, "1", 1000, 100_000), c(2, "1", 1000, 100_000)
	cs := []types.Candidate{a, b}
	want, wl := types.Split(math.NewInt(2000), cs)
	got, gl := types.SplitAtLeast(math.NewInt(2000), cs, math.NewInt(100))
	require.Equal(t, want, got)
	require.Equal(t, wl, gl)
}

func TestSplitAtLeastKeepsASingleSmallLeg(t *testing.T) {
	a := c(1, "1", 1000, 100_000)
	legs, left := types.SplitAtLeast(math.NewInt(50), []types.Candidate{a}, math.NewInt(100))
	require.True(t, left.IsZero())
	require.Equal(t, []int64{50}, amounts(legs))
}

func TestSplitAtLeastFallsBackWhenFoldingWouldLoseRoom(t *testing.T) {
	// room 1000 + 10, amount 1005: the plain split places everything, with a
	// 5-nscn dust leg. Folding it would strand 5, so the plain legs are kept.
	a, b := c(1, "1", 0, 1000), c(2, "1", 0, 10)
	cs := []types.Candidate{a, b}
	want, wl := types.Split(math.NewInt(1005), cs)
	require.True(t, wl.IsZero())
	legs, left := types.SplitAtLeast(math.NewInt(1005), cs, math.NewInt(100))
	require.True(t, left.IsZero())
	require.Equal(t, want, legs)
	require.Len(t, legs, 2)
}

func TestSplitAtLeastReportsTheBestLeftover(t *testing.T) {
	// total room 1010 < 1020: nothing can place it all; report the plain split's leftover.
	a, b := c(1, "1", 0, 1000), c(2, "1", 0, 10)
	legs, left := types.SplitAtLeast(math.NewInt(1020), []types.Candidate{a, b}, math.NewInt(100))
	require.Equal(t, int64(10), left.Int64())
	require.Equal(t, []int64{1000, 10}, amounts(legs))
}
