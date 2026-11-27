package api_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/store"
)

// walk records heights 1..n of a chain with 4-block epochs, proposer chosen by who(height).
func walk(t *testing.T, s *store.Store, n int, who func(h int) string) {
	t.Helper()
	for h := 1; h <= n; h++ {
		require.NoError(t, s.RecordBlock(context.Background(), "c1", int64(h), uint64((h-1)/4+1), who(h)))
	}
}

func equalSet() []store.PowerRow {
	return []store.PowerRow{
		{ConsAddr: "AA", Operator: sp("op1"), Power: 1, Unit: sp("val:op1")},
		{ConsAddr: "BB", Operator: sp("op2"), Power: 1, Unit: sp("val:op2")},
		{ConsAddr: "CC", Power: 1},
	}
}

func epochRow(e uint64) store.ConcentrationRow {
	r := readRow(e)
	r.Height = int64(e) * 4
	r.TotalPower = ip(3)
	r.SetSize = ip(3)
	return r
}

func epochsOf(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	arr, ok := body["epochs"].([]any)
	require.True(t, ok, "epochs is not an array: %#v", body["epochs"])
	out := make([]map[string]any, len(arr))
	for i, x := range arr {
		out[i] = x.(map[string]any)
	}
	return out
}

func TestProposersShape(t *testing.T) {
	s, h := newServer(t)
	require.NoError(t, s.UpsertValidator(context.Background(), "c1", store.ValidatorRow{Operator: "op1", Moniker: "node1", Tokens: "1"}))
	walk(t, s, 4, func(h int) string { return []string{"AA", "AA", "BB", "CC"}[h-1] })
	seed(t, s, epochRow(1), equalSet(), nil)

	code, body := get(t, h, "/v1/proposers")
	require.Equal(t, 200, code)
	eps := epochsOf(t, body)
	require.Len(t, eps, 1)
	e := eps[0]
	for _, k := range []string{"epoch", "height", "epoch_length", "blocks", "total_power", "unavailable", "validators"} {
		require.Contains(t, e, k)
	}
	require.Equal(t, float64(1), e["epoch"])
	require.Equal(t, float64(4), e["blocks"])
	require.Equal(t, "3", e["total_power"])
	require.Nil(t, e["unavailable"])
	vs := e["validators"].([]any)
	require.Len(t, vs, 3)
	v := vs[0].(map[string]any)
	require.Equal(t, "op1", v["operator"])
	require.Equal(t, "node1", v["moniker"])
	require.Equal(t, float64(2), v["proposed"])
	require.Equal(t, "0.500000", v["proposed_share"])
	require.Equal(t, "0.333333", v["power_share"]) // truncated, not rounded to ...334
	require.Equal(t, "1.33", v["expected"])
	require.Equal(t, "cons:CC", vs[2].(map[string]any)["operator"])
	require.NotContains(t, v, "ratio", "the ratio is a window figure, never per epoch")

	w := body["window"].(map[string]any)
	require.Equal(t, float64(1), w["epochs"])
	require.Equal(t, float64(4), w["blocks"])
	wv := w["validators"].([]any)[0].(map[string]any)
	require.Equal(t, "op1", wv["operator"])
	require.Equal(t, "0.500000", wv["proposed_share"])
	require.Equal(t, "0.333333", wv["power_share"])
	require.Equal(t, "1.500", wv["ratio"])
}

func TestProposersUnavailableEpochsAreNotZeros(t *testing.T) {
	s, h := newServer(t)
	walk(t, s, 11, func(h int) string { return "AA" })
	seed(t, s, epochRow(1), equalSet(), nil)
	pruned := epochRow(2)
	pruned.PowerUnavailable = sp("pruned")
	pruned.SetSize, pruned.TotalPower, pruned.Top10Power, pruned.NakamotoValidators = nil, nil, nil, nil
	seed(t, s, pruned, nil, nil)
	seed(t, s, epochRow(3), equalSet(), nil) // last block 12 is not indexed yet (cursor 11)

	_, body := get(t, h, "/v1/proposers")
	eps := epochsOf(t, body)
	require.Len(t, eps, 3)
	require.Nil(t, eps[0]["unavailable"])
	require.Equal(t, "pruned", eps[1]["unavailable"])
	require.Nil(t, eps[1]["validators"])
	require.Nil(t, eps[1]["total_power"])
	require.Equal(t, float64(4), eps[1]["blocks"]) // counted, still a fact
	require.Equal(t, "blocks_not_indexed", eps[2]["unavailable"])
	require.Nil(t, eps[2]["validators"])
}

func TestProposersBounds(t *testing.T) {
	s, h := newServer(t)
	walk(t, s, 40, func(h int) string { return "AA" })
	for e := uint64(1); e <= 10; e++ {
		seed(t, s, epochRow(e), equalSet(), nil)
	}
	nums := func(url string) []float64 {
		t.Helper()
		code, body := get(t, h, url)
		require.Equal(t, 200, code, url)
		var out []float64
		for _, e := range epochsOf(t, body) {
			out = append(out, e["epoch"].(float64))
		}
		return out
	}
	require.Equal(t, []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, nums("/v1/proposers"))              // default: newest 20
	require.Equal(t, []float64{9, 10}, nums("/v1/proposers?epochs=2"))                             // newest N, oldest first
	require.Equal(t, []float64{3, 4, 5}, nums("/v1/proposers?from=3&to=5"))                        // a range
	require.Equal(t, []float64{4, 5}, nums("/v1/proposers?from=3&to=5&epochs=2"))                  // newest N inside it
	require.Equal(t, []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, nums("/v1/proposers?epochs=99999")) // clamped to the cap (200)
	require.Equal(t, []float64{9, 10}, nums("/v1/proposers?from=9"))
	require.Empty(t, nums("/v1/proposers?from=18446744073709551615")) // clamped, not a 500
	code, body := get(t, h, "/v1/proposers?from=500")
	require.Equal(t, 200, code)
	require.NotNil(t, body["epochs"], "an empty range serves [], not null")
}

func TestProposersMalformedParametersAre400(t *testing.T) {
	_, h := newServer(t)
	for _, u := range []string{"?from=abc", "?to=abc", "?to=-1", "?epochs=abc", "?epochs=-1", "?epochs=0", "?epochs=1e3", "?from=1.5"} {
		code, body := get(t, h, "/v1/proposers"+u)
		require.Equal(t, 400, code, u)
		require.Contains(t, body, "error", u)
	}
}

func TestProposersCapIs200(t *testing.T) {
	s, h := newServer(t)
	for e := uint64(1); e <= 230; e++ {
		seed(t, s, epochRow(e), nil, nil) // no detail: the epochs read as unavailable, which is enough to count rows
	}
	count := func(u string) (int, float64) {
		code, body := get(t, h, u)
		require.Equal(t, 200, code, u)
		eps := epochsOf(t, body)
		return len(eps), eps[0]["epoch"].(float64)
	}
	n, first := count("/v1/proposers?epochs=1000")
	require.Equal(t, 200, n)
	require.Equal(t, float64(31), first)
	n, first = count("/v1/proposers?from=1")
	require.Equal(t, 200, n)
	require.Equal(t, float64(1), first)
}
