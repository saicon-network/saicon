package proposers_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/proposers"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

func sp(s string) *string { return &s }
func ip(n int64) *int64   { return &n }

// row is epoch e of a chain with 4-block epochs, measured at its last block.
func row(e uint64, total int64) store.ConcentrationRow {
	return store.ConcentrationRow{Epoch: e, Height: int64(e) * 4, TotalPower: ip(total), SetSize: ip(3)}
}

func inputs(rows ...store.ConcentrationRow) *store.ProposerInputs {
	return &store.ProposerInputs{
		Rows: rows, FirstWalked: 1, Indexed: 1000,
		Powers: map[uint64][]store.PowerRow{}, Counts: map[uint64]map[string]int64{},
	}
}

func set3(in *store.ProposerInputs, e uint64) {
	in.Powers[e] = []store.PowerRow{
		{ConsAddr: "AA", Operator: sp("op1"), Power: 1, Moniker: sp("one")},
		{ConsAddr: "BB", Operator: sp("op2"), Power: 1},
		{ConsAddr: "CC", Power: 1}, // x/staking had no validator for this key
	}
}

func TestSharesAndExpectedAreTruncated(t *testing.T) {
	in := inputs(row(1, 3))
	set3(in, 1)
	in.Counts[1] = map[string]int64{"AA": 2, "BB": 1, "CC": 1}

	got := proposers.Build(in).Epochs
	require.Len(t, got, 1)
	e := got[0]
	require.Nil(t, e.Unavailable)
	require.Equal(t, int64(4), e.Blocks)
	require.Equal(t, "3", *e.TotalPower)
	require.Equal(t, int64(4), *e.EpochLength)
	require.Len(t, e.Validators, 3)

	a := e.Validators[0]
	require.Equal(t, "op1", a.Operator)
	require.Equal(t, "one", *a.Moniker)
	require.Equal(t, "0.500000", a.ProposedShare)
	require.Equal(t, "0.333333", *a.PowerShare) // 1/3 truncated, never 0.333334
	require.Equal(t, "1.33", *a.Expected)       // 4/3 truncated, never 1.34
	require.Equal(t, "cons:CC", e.Validators[2].Operator)
}

func TestUnavailableCarriesACodeAndNoFigures(t *testing.T) {
	cases := []struct {
		name  string
		code  string
		setup func(in *store.ProposerInputs, r *store.ConcentrationRow)
	}{
		{"power not measured", "pruned", func(_ *store.ProposerInputs, r *store.ConcentrationRow) { r.PowerUnavailable = sp("pruned") }},
		{"walk began later", proposers.CodeBeforeHistory, func(in *store.ProposerInputs, _ *store.ConcentrationRow) { in.FirstWalked = 2 }},
		{"walk not there yet", proposers.CodeNotIndexed, func(in *store.ProposerInputs, _ *store.ConcentrationRow) { in.Indexed = 3 }},
		{"one block short", proposers.CodeIncomplete, func(in *store.ProposerInputs, _ *store.ConcentrationRow) { in.Counts[1]["AA"] = 1 }},
		{"detail pruned", proposers.CodePruned, func(in *store.ProposerInputs, _ *store.ConcentrationRow) { delete(in.Powers, 1) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := row(1, 3)
			in := inputs(r)
			set3(in, 1)
			in.Counts[1] = map[string]int64{"AA": 2, "BB": 1, "CC": 1}
			c.setup(in, &r)
			in.Rows[0] = r
			e := proposers.Build(in).Epochs[0]
			require.NotNil(t, e.Unavailable)
			require.Equal(t, c.code, *e.Unavailable)
			require.Nil(t, e.Validators)
			require.Nil(t, e.TotalPower)
		})
	}
}

func TestNonDivisibleHeightIsUnavailable(t *testing.T) {
	r := row(3, 3)
	r.Height = 13 // not a multiple of 3: the epoch length cannot be derived
	e := proposers.Build(inputs(r)).Epochs[0]
	require.Equal(t, proposers.CodeEpochLength, *e.Unavailable)
	require.Nil(t, e.EpochLength)
}

func TestProposerOutsideTheSetKeepsItsBlocksAndHasNoPower(t *testing.T) {
	in := inputs(row(1, 2))
	in.Powers[1] = []store.PowerRow{
		{ConsAddr: "AA", Operator: sp("op1"), Power: 1},
		{ConsAddr: "BB", Operator: sp("op2"), Power: 1},
	}
	in.Counts[1] = map[string]int64{"AA": 2, "BB": 1, "ZZ": 1} // ZZ left mid-epoch
	e := proposers.Build(in).Epochs[0]
	require.Nil(t, e.Unavailable)
	require.Len(t, e.Validators, 3)
	z := e.Validators[2]
	require.Equal(t, "cons:ZZ", z.Operator)
	require.Equal(t, int64(1), z.Proposed)
	require.Equal(t, "0.250000", z.ProposedShare)
	require.Nil(t, z.Power)
	require.Nil(t, z.PowerShare)
	require.Nil(t, z.Expected)
}

func TestAMemberWithNoProposalsIsAZeroNotAbsent(t *testing.T) {
	in := inputs(row(1, 2))
	in.Powers[1] = []store.PowerRow{
		{ConsAddr: "AA", Operator: sp("op1"), Power: 1},
		{ConsAddr: "BB", Operator: sp("op2"), Power: 1},
	}
	in.Counts[1] = map[string]int64{"AA": 4}
	e := proposers.Build(in).Epochs[0]
	b := e.Validators[1]
	require.Equal(t, int64(0), b.Proposed)
	require.Equal(t, "0.000000", b.ProposedShare)
}

func TestWindowAggregatesExactlyOverComparableEpochs(t *testing.T) {
	// Epoch 1: AA 2 of 4 blocks, equal power. Epoch 2 cannot be compared and is
	// left out. Epoch 3: AA's power halves to 1 of 4 total, it proposes 0.
	in := inputs(row(1, 3), row(2, 3), row(3, 4))
	set3(in, 1)
	in.Counts[1] = map[string]int64{"AA": 2, "BB": 1, "CC": 1}
	in.Counts[2] = map[string]int64{"AA": 1} // one block short of 4: unavailable
	in.Powers[3] = []store.PowerRow{
		{ConsAddr: "AA", Operator: sp("op1"), Power: 1},
		{ConsAddr: "BB", Operator: sp("op2"), Power: 3},
	}
	in.Counts[3] = map[string]int64{"BB": 4}
	res := proposers.Build(in)
	w := res.Window
	require.Equal(t, 2, w.Epochs)
	require.Equal(t, uint64(1), *w.From)
	require.Equal(t, uint64(3), *w.To)
	require.Equal(t, int64(8), w.Blocks)
	require.Len(t, w.Validators, 3)

	// BB: power 1/3 then 3/4, mean 13/24 = 0.541666; proposed 5 of 8; expected 4/3+3 = 13/3.
	b := w.Validators[0]
	require.Equal(t, "op2", b.Operator)
	require.Equal(t, "0.541666", b.PowerShare)
	require.Equal(t, "0.625000", b.ProposedShare)
	require.Equal(t, "1.153", *b.Ratio) // 5 / (13/3) = 1.1538...
	// AA: power 1/3 then 1/4, mean 7/24 = 0.291666; proposed 2 of 8.
	a := w.Validators[1]
	require.Equal(t, "0.291666", a.PowerShare)
	require.Equal(t, "0.250000", a.ProposedShare)
	// CC left the set after epoch 1: its power in epoch 3 counts as 0.
	c := w.Validators[2]
	require.Equal(t, "cons:CC", c.Operator)
	require.Equal(t, "0.166666", c.PowerShare)
	require.Equal(t, 1, c.InSetEpochs)
	require.Equal(t, "0.750", *c.Ratio) // 1 / (4/3)
}

func TestWindowWithNoComparableEpochIsEmptyNotNull(t *testing.T) {
	r := row(1, 3)
	r.PowerUnavailable = sp("pruned")
	w := proposers.Build(inputs(r)).Window
	require.Equal(t, 0, w.Epochs)
	require.Nil(t, w.From)
	require.NotNil(t, w.Validators)
	require.Empty(t, w.Validators)
}

func TestOperatorIsResolvedFromAnyEpochOfTheSeries(t *testing.T) {
	in := inputs(row(1, 2), row(2, 2))
	in.Powers[1] = []store.PowerRow{{ConsAddr: "AA", Operator: sp("op1"), Power: 1, Moniker: sp("one")}, {ConsAddr: "BB", Operator: sp("op2"), Power: 1}}
	in.Powers[2] = []store.PowerRow{{ConsAddr: "AA", Power: 1}, {ConsAddr: "BB", Operator: sp("op2"), Power: 1}} // staking state pruned for AA
	in.Counts[1] = map[string]int64{"AA": 2, "BB": 2}
	in.Counts[2] = map[string]int64{"AA": 2, "BB": 2}
	res := proposers.Build(in)
	for _, e := range res.Epochs {
		require.Equal(t, "op1", e.Validators[0].Operator)
		require.Equal(t, "one", *e.Validators[0].Moniker)
	}
	require.Equal(t, "op1", res.Window.Validators[0].Operator)
}
