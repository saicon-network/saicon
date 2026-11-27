package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/store"
)

func getOK(t *testing.T, h http.Handler, path string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

// R3: the WARP and preview fields are null when there is nothing to say and the
// chain's own strings when there is, never a zero standing in for an absence.
func TestScoreboardWarpAndPreviewFields(t *testing.T) {
	s, h := newServer(t)
	ctx := context.Background()
	const (
		a = "saiconvaloper1a"
		b = "saiconvaloper1b"
		c = "saiconvaloper1c"
	)
	for _, op := range []string{a, b, c} {
		require.NoError(t, s.UpsertValidator(ctx, "c1", store.ValidatorRow{Operator: op, StakingStatus: "BOND_STATUS_BONDED", Tokens: "1"}))
	}
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{Epoch: 6, Operator: a,
		ComputedScore: sp("1.000000000000000000"), Warp: sp("86.878000000000000000")}))
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{Epoch: 6, Operator: b,
		ComputedScore: sp("0.000000000000000000")})) // a snapshot without WARP

	// No preview read yet: every preview field null.
	body := getOK(t, h, "/v1/scoreboard")
	for _, k := range []string{"preview_epoch", "preview_unavailable", "preview_threshold"} {
		v, ok := body[k]
		require.True(t, ok, "%s must appear", k)
		require.Nil(t, v, "%s must be null", k)
	}
	rows := map[string]map[string]any{}
	for _, v := range body["validators"].([]any) {
		m := v.(map[string]any)
		rows[m["operator"].(string)] = m
	}
	require.Equal(t, "86.878000000000000000", rows[a]["warp"], "the chain's string, unrounded")
	for _, op := range []string{b, c} {
		v, ok := rows[op]["warp"]
		require.True(t, ok)
		require.Nil(t, v, "%s: no WARP is null, not 0", op)
	}
	for _, op := range []string{a, b, c} {
		v, ok := rows[op]["preview"]
		require.True(t, ok)
		require.Nil(t, v)
	}

	// Epoch 5 available, epoch 6 not.
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 5, Height: 50,
		TotalVP: sp("85420000000000"), Threshold: sp("65.000000000000000000"), Cap: sp("200000000000000")},
		[]store.PreviewRow{
			{Operator: a, VP: "85420000000000", VPShare: "0.333333", StockShare: "0.980000",
				Eligible: true, Warp: "85.420000000000000000", WarpMeasured: true},
			{Operator: b, VP: "560000000000", VPShare: "0", StockShare: "0.020000",
				Eligible: false, Reason: sp("warp below threshold"), Warp: "28.000000000000000000", WarpMeasured: true},
		}))
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 6, Height: 60, Unavailable: sp("route_missing")}, nil))

	body = getOK(t, h, "/v1/scoreboard")
	require.Equal(t, float64(5), body["preview_epoch"])
	require.Equal(t, "route_missing", body["preview_unavailable"])
	require.Equal(t, "65.000000000000000000", body["preview_threshold"])
	rows = map[string]map[string]any{}
	for _, v := range body["validators"].([]any) {
		m := v.(map[string]any)
		rows[m["operator"].(string)] = m
	}
	require.Equal(t, map[string]any{"vp": "85420000000000", "vp_share": "0.333333", "eligible": true,
		"reason": nil, "warp_measured": true}, rows[a]["preview"], "shares pass through as the chain's strings")
	require.Equal(t, map[string]any{"vp": "560000000000", "vp_share": "0", "eligible": false,
		"reason": "warp below threshold", "warp_measured": true}, rows[b]["preview"])
	require.Nil(t, rows[c]["preview"], "a validator outside the preview has none, not a zero one")

	// The single-validator endpoint serves the same row.
	one := getOK(t, h, "/v1/validators/"+b)
	require.Nil(t, one["warp"])
	require.Equal(t, "0", one["preview"].(map[string]any)["vp_share"])
}

// R3: the history the scoreboard's drawer reads gains warp per epoch.
func TestEpochsServeWarpPerEpoch(t *testing.T) {
	s, h := newServer(t)
	ctx := context.Background()
	const op = "saiconvaloper1a"
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{Epoch: 1, Operator: op, ComputedScore: sp("1.000000000000000000")}))
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{Epoch: 2, Operator: op, ComputedScore: sp("1.000000000000000000"),
		Warp: sp("82.000000000000000000")}))

	body := getOK(t, h, "/v1/validators/"+op+"/epochs")
	eps := body["epochs"].([]any)
	require.Len(t, eps, 2)
	w1, ok := eps[0].(map[string]any)["warp"]
	require.True(t, ok, "warp must appear on every epoch")
	require.Nil(t, w1, "an epoch without WARP is null")
	require.Equal(t, "82.000000000000000000", eps[1].(map[string]any)["warp"])

	// Each epoch names its own preview's threshold, so the drawer marks it
	// against the threshold that applied then; null when it has none.
	t1, ok := eps[0].(map[string]any)["preview_threshold"]
	require.True(t, ok, "preview_threshold must appear on every epoch")
	require.Nil(t, t1)
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 2, Height: 20,
		TotalVP: sp("1"), Threshold: sp("70.000000000000000000"), Cap: sp("1")}, nil))
	eps = getOK(t, h, "/v1/validators/"+op+"/epochs")["epochs"].([]any)
	require.Equal(t, "70.000000000000000000", eps[1].(map[string]any)["preview_threshold"])
	require.Nil(t, eps[0].(map[string]any)["preview_threshold"])
}
