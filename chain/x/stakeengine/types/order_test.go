package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func ids(es []types.QueueEntry) []uint64 {
	out := make([]uint64, len(es))
	for i, e := range es {
		out[i] = e.Id
	}
	return out
}

func q(score string, ep int64) types.Quality {
	return types.Quality{Score: math.LegacyMustNewDecFromStr(score), EP: math.NewInt(ep)}
}

func TestSortQueue_ByScoreThenEPThenHeightThenID(t *testing.T) {
	entries := []types.QueueEntry{
		{Id: 1, Validator: "low", EnqueuedHeight: 5},
		{Id: 2, Validator: "high", EnqueuedHeight: 9},
		{Id: 3, Validator: "mid-ep-high", EnqueuedHeight: 7},
		{Id: 4, Validator: "mid-ep-low", EnqueuedHeight: 3},
		{Id: 5, Validator: "high", EnqueuedHeight: 9},
		{Id: 6, Validator: "high", EnqueuedHeight: 2},
	}
	quality := map[string]types.Quality{
		"high":        q("0.95", 1),
		"mid-ep-high": q("0.50", 40),
		"mid-ep-low":  q("0.50", 10),
		"low":         q("0.10", 99),
	}
	got := types.SortQueue(entries, func(v string) types.Quality { return quality[v] })
	// score desc; within "high" height asc (6 before 2 and 5), then id asc (2 before 5);
	// within score 0.50, EP desc (3 before 4) regardless of height.
	require.Equal(t, []uint64{6, 2, 5, 3, 4, 1}, ids(got))
}

func TestSortQueue_AValidatorWithNoScoreRanksLast(t *testing.T) {
	entries := []types.QueueEntry{
		{Id: 1, Validator: "new", EnqueuedHeight: 1},
		{Id: 2, Validator: "scored", EnqueuedHeight: 50},
	}
	got := types.SortQueue(entries, func(v string) types.Quality {
		if v == "scored" {
			return q("0.01", 0)
		}
		return types.Quality{} // no snapshot, no EP: nil fields
	})
	require.Equal(t, []uint64{2, 1}, ids(got))
}

func TestSortQueue_DoesNotMutateItsInput(t *testing.T) {
	entries := []types.QueueEntry{{Id: 2, Validator: "a"}, {Id: 1, Validator: "a"}}
	_ = types.SortQueue(entries, func(string) types.Quality { return types.Quality{} })
	require.Equal(t, []uint64{2, 1}, ids(entries))
}

func TestSortQueue_AsksForEachValidatorOnce(t *testing.T) {
	calls := map[string]int{}
	entries := []types.QueueEntry{{Id: 1, Validator: "a"}, {Id: 2, Validator: "a"}, {Id: 3, Validator: "b"}}
	_ = types.SortQueue(entries, func(v string) types.Quality { calls[v]++; return types.Quality{} })
	require.Equal(t, map[string]int{"a": 1, "b": 1}, calls)
}
