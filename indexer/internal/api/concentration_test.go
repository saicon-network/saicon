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

func sp(s string) *string { return &s }
func ip(n int64) *int64   { return &n }
func bp(b bool) *bool     { return &b }

// readRow is a measured epoch of the three-holder fixture below.
func readRow(epoch uint64) store.ConcentrationRow {
	return store.ConcentrationRow{
		Epoch: epoch, Height: int64(epoch) * 100, BlockHash: sp("ABCD"),
		SetSize: ip(3), TotalPower: ip(1200), Top10Power: ip(1200), NakamotoValidators: ip(1),
		UnitCount: ip(2), NakamotoEntities: ip(1), EntityCount: ip(1),
		EntityCap: sp("2000000000000000000000"), MaxActiveSeats: ip(2),
		ReadAt: "2026-10-29T10:00:00Z",
	}
}

func fixturePowers() []store.PowerRow {
	return []store.PowerRow{
		{ConsAddr: "AA", Operator: sp("op1"), Power: 300, Unit: sp("entity:x")},
		{ConsAddr: "BB", Operator: sp("op2"), Power: 500, Unit: sp("entity:x")},
		{ConsAddr: "CC", Operator: sp("opC"), Power: 400, Unit: sp("val:C")},
	}
}

func get(t *testing.T, h http.Handler, url string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return rec.Code, body
}

func seed(t *testing.T, s *store.Store, row store.ConcentrationRow, p []store.PowerRow, e []store.EntityRow) {
	t.Helper()
	require.NoError(t, s.WriteConcentration(context.Background(), "c1", row, p, e))
}

func strs(t *testing.T, v any) []string {
	t.Helper()
	arr, ok := v.([]any)
	require.True(t, ok, "not an array: %#v", v)
	out := make([]string, len(arr))
	for i, x := range arr {
		out[i], ok = x.(string)
		require.True(t, ok, "not a string: %#v", x)
	}
	return out
}

func TestConcentrationShape(t *testing.T) {
	s, h := newServer(t)
	require.NoError(t, s.UpsertValidator(context.Background(), "c1", store.ValidatorRow{Operator: "op2", Moniker: "node2", Tokens: "1"}))
	seed(t, s, readRow(5), fixturePowers(), nil)

	code, body := get(t, h, "/v1/concentration")
	require.Equal(t, 200, code)
	for _, k := range []string{"epoch", "height", "block_hash", "set_size", "total_power", "top10_power",
		"top10_share", "nakamoto_validators", "entities_unavailable", "unit_count", "nakamoto_entities",
		"entity_count", "entity_cap", "max_active_seats", "validators", "curve"} {
		require.Contains(t, body, k)
	}
	require.Equal(t, "1200", body["total_power"])
	require.Equal(t, "1200", body["top10_power"])
	require.Equal(t, "1.000000", body["top10_share"])
	require.Equal(t, float64(3), body["set_size"])
	require.Equal(t, float64(5), body["epoch"])
	require.Nil(t, body["entities_unavailable"])
	require.Equal(t, "2000000000000000000000", body["entity_cap"])

	curve := body["curve"].(map[string]any)
	require.Equal(t, []string{"0.416666", "0.750000", "1.000000"}, strs(t, curve["validators"]))
	require.Equal(t, []string{"0.666666", "1.000000"}, strs(t, curve["entities"]))

	vals := body["validators"].([]any)
	require.Len(t, vals, 3)
	var powers []string
	for _, v := range vals {
		powers = append(powers, v.(map[string]any)["power"].(string))
	}
	require.Equal(t, []string{"500", "400", "300"}, powers)
	first := vals[0].(map[string]any)
	require.Equal(t, "node2", first["moniker"])
	require.Equal(t, "BB", first["cons_addr"])
	require.Equal(t, "op2", first["operator"])
	require.Equal(t, "entity:x", first["unit"])
	require.Nil(t, vals[1].(map[string]any)["moniker"]) // no validators row: a label is absent, not ""
}

func TestConcentrationEntitiesUnavailableNullsOnlyTheEntityCurve(t *testing.T) {
	s, h := newServer(t)
	row := readRow(5)
	row.EntitiesUnavailable = sp("before_entity_layer")
	row.UnitCount, row.NakamotoEntities, row.EntityCount, row.EntityCap, row.MaxActiveSeats = nil, nil, nil, nil, nil
	p := fixturePowers()
	for i := range p {
		p[i].Unit = nil
	}
	seed(t, s, row, p, nil)
	code, body := get(t, h, "/v1/concentration")
	require.Equal(t, 200, code)
	require.Equal(t, "before_entity_layer", body["entities_unavailable"])
	curve := body["curve"].(map[string]any)
	require.Nil(t, curve["entities"])
	require.Len(t, curve["validators"], 3)
	require.Nil(t, body["unit_count"])
	require.Nil(t, body["entity_cap"])
}

func TestConcentrationNotFound(t *testing.T) {
	s, h := newServer(t)
	code, body := get(t, h, "/v1/concentration")
	require.Equal(t, 404, code)
	require.Equal(t, "no measured epoch", body["error"])

	row := store.ConcentrationRow{Epoch: 8, Height: 800, PowerUnavailable: sp("pruned"), ReadAt: "2026-10-29T10:00:00Z"}
	seed(t, s, row, nil, nil)
	code, body = get(t, h, "/v1/concentration?epoch=8")
	require.Equal(t, 404, code)
	require.Equal(t, "epoch 8 was not measured", body["error"])

	code, body = get(t, h, "/v1/concentration?epoch=x")
	require.Equal(t, 400, code)
	require.Equal(t, "epoch must be an unsigned integer", body["error"])

	// An epoch past the int64 range is clamped, not passed to the driver as is.
	code, _ = get(t, h, "/v1/concentration?epoch=18446744073709551615")
	require.Equal(t, 404, code)
}

func TestConcentrationPrunedDetail(t *testing.T) {
	s, h := newServer(t)
	seed(t, s, readRow(1), fixturePowers(), nil)
	seed(t, s, readRow(2001), fixturePowers(), nil)

	code, body := get(t, h, "/v1/concentration?epoch=1")
	require.Equal(t, 200, code)
	require.Contains(t, body, "validators")
	require.Nil(t, body["validators"])
	require.Contains(t, body, "curve")
	require.Nil(t, body["curve"])
	require.Equal(t, "1200", body["total_power"]) // the row itself is kept for good
}

func TestSeriesIncludesUnavailableRows(t *testing.T) {
	s, h := newServer(t)
	seed(t, s, readRow(1), fixturePowers(), nil)
	seed(t, s, store.ConcentrationRow{Epoch: 2, Height: 200, PowerUnavailable: sp("pruned"), ReadAt: "2026-10-29T10:00:00Z"}, nil, nil)

	code, body := get(t, h, "/v1/concentration/epochs")
	require.Equal(t, 200, code)
	rows := body["epochs"].([]any)
	require.Len(t, rows, 2)
	r1, r2 := rows[0].(map[string]any), rows[1].(map[string]any)
	require.Equal(t, float64(1), r1["epoch"])
	require.Equal(t, "1.000000", r1["top10_share"])
	require.Equal(t, "1200", r1["total_power"])
	require.Nil(t, r1["power_unavailable"])
	require.Equal(t, "pruned", r2["power_unavailable"])
	for _, k := range []string{"height", "set_size", "total_power", "top10_power", "top10_share", "nakamoto_validators",
		"entities_unavailable", "unit_count", "nakamoto_entities", "entity_count"} {
		require.Contains(t, r2, k)
	}
	require.Nil(t, r2["top10_share"])
	require.Nil(t, r2["total_power"])

	code, body = get(t, h, "/v1/concentration/epochs?from=2&to=2")
	require.Equal(t, 200, code)
	require.Len(t, body["epochs"], 1)

	code, body = get(t, h, "/v1/concentration/epochs?from=50")
	require.Equal(t, 200, code)
	require.Equal(t, []any{}, body["epochs"])
}

func entityFixture() []store.EntityRow {
	return []store.EntityRow{
		{
			EntityID: "x", DisplayName: "Entity X", Status: "ENTITY_STATUS_ATTESTED",
			AttestedHeight: ip(1), Tokens: sp("123456789012345678901234"), EntityCap: sp("200"), Room: sp("77"),
			ActiveSeats: ip(2), MaxActiveSeats: ip(2), Frozen: bp(false),
			Seats: []store.SeatRow{
				{Operator: "op1", Role: "member", RegistryStatus: sp("STATUS_ACTIVE"), Tokens: sp("100"), Power: ip(300)},
				{Operator: "op2", Role: "member", RegistryStatus: sp("STATUS_ACTIVE"), Tokens: sp("200"), Power: ip(500)},
			},
		},
		{
			EntityID: "gone", DisplayName: "Revoked", Status: "ENTITY_STATUS_REVOKED",
			ActiveSeats: ip(1), MaxActiveSeats: ip(2), Frozen: bp(true), FrozenReason: sp("revoked"),
			Seats: []store.SeatRow{
				{Operator: "opF", Role: "charged_frozen", FrozenReason: sp("revoked"), FrozenHeight: ip(40)},
			},
		},
	}
}

func TestEntitiesShape(t *testing.T) {
	s, h := newServer(t)
	require.NoError(t, s.UpsertValidator(context.Background(), "c1", store.ValidatorRow{Operator: "op1", Moniker: "node1", Tokens: "1"}))
	row := readRow(5)
	row.EntityCount = ip(2)
	seed(t, s, row, fixturePowers(), entityFixture())

	code, body := get(t, h, "/v1/entities")
	require.Equal(t, 200, code)
	require.Equal(t, float64(5), body["epoch"])
	require.Equal(t, float64(500), body["height"])
	require.Equal(t, "2000000000000000000000", body["entity_cap"])
	require.Equal(t, float64(2), body["max_active_seats"])

	ents := body["entities"].([]any)
	require.Len(t, ents, 2)
	x, gone := ents[0].(map[string]any), ents[1].(map[string]any)

	require.Equal(t, "x", x["entity_id"])
	require.Equal(t, "123456789012345678901234", x["tokens"]) // exact, a string
	require.Equal(t, "77", x["room"])
	require.Equal(t, false, x["frozen"])
	require.Nil(t, x["frozen_reason"])
	require.Equal(t, []any{}, x["charged_frozen"])
	members := x["members"].([]any)
	require.Len(t, members, 2)
	m0 := members[0].(map[string]any)
	require.Equal(t, "op1", m0["operator"])
	require.Equal(t, "node1", m0["moniker"])
	require.Equal(t, "300", m0["power"])
	require.Equal(t, "100", m0["tokens"])
	require.Equal(t, "STATUS_ACTIVE", m0["registry_status"])

	// The revoked entity: chain figures are null, never 0, and its frozen seat is listed.
	require.Equal(t, "gone", gone["entity_id"])
	require.Contains(t, gone, "tokens")
	require.Nil(t, gone["tokens"])
	require.Nil(t, gone["room"])
	require.Equal(t, []any{}, gone["members"])
	cf := gone["charged_frozen"].([]any)
	require.Len(t, cf, 1)
	c0 := cf[0].(map[string]any)
	require.Equal(t, "revoked", c0["frozen_reason"])
	require.Equal(t, float64(40), c0["frozen_height"])
	require.Equal(t, "opF", c0["operator"])
	require.Nil(t, c0["power"])
}

func TestEntitiesPrunedDetail(t *testing.T) {
	s, h := newServer(t)
	zero := readRow(1)
	zero.EntityCount = ip(0) // counted none: only the horizon says its detail is gone
	seed(t, s, zero, fixturePowers(), nil)
	seed(t, s, readRow(2), fixturePowers(), nil) // counted 1 entity, none stored: detail missing
	seed(t, s, readRow(2001), fixturePowers(), entityFixture()[:1])

	// Past the detail horizon the list is gone even for an epoch that had no entities.
	code, body := get(t, h, "/v1/entities?epoch=1")
	require.Equal(t, 200, code)
	require.Equal(t, float64(1), body["epoch"])
	require.Contains(t, body, "entities")
	require.Nil(t, body["entities"])

	// A row that counted entities but holds none is never served as zero entities.
	code, body = get(t, h, "/v1/entities?epoch=2")
	require.Equal(t, 200, code)
	require.Nil(t, body["entities"])

	code, body = get(t, h, "/v1/entities?epoch=2001")
	require.Equal(t, 200, code)
	require.Len(t, body["entities"], 1)

	code, body = get(t, h, "/v1/entities?epoch=9")
	require.Equal(t, 404, code)
	require.Equal(t, "epoch 9 was not measured", body["error"])

	code, _ = get(t, h, "/v1/entities?epoch=-1")
	require.Equal(t, 400, code)
}

func TestEntitiesWithNoEntitiesIsAnEmptyListNotNull(t *testing.T) {
	s, h := newServer(t)
	row := readRow(3)
	row.EntityCount = ip(0)
	seed(t, s, row, fixturePowers(), nil)
	code, body := get(t, h, "/v1/entities")
	require.Equal(t, 200, code)
	require.Equal(t, []any{}, body["entities"])
}

func TestEntitiesNoneMeasured(t *testing.T) {
	s, h := newServer(t)
	row := readRow(3)
	row.EntitiesUnavailable = sp("no_entity_layer")
	seed(t, s, row, fixturePowers(), nil)
	code, body := get(t, h, "/v1/entities")
	require.Equal(t, 404, code)
	require.Equal(t, "no measured epoch", body["error"])
}

// The SQL driver refuses a uint64 with the high bit set, so every caller clamps
// an epoch bound to MaxInt64 before it reaches the store: the largest unsigned
// value is answered like any other epoch that was never measured, never with 500.
func TestEpochBoundsPastInt64AreClamped(t *testing.T) {
	s, h := newServer(t)
	seed(t, s, readRow(3), fixturePowers(), nil)
	const max = "18446744073709551615"

	code, body := get(t, h, "/v1/entities?epoch="+max)
	require.Equal(t, 404, code)
	require.Equal(t, "epoch 9223372036854775807 was not measured", body["error"])

	code, body = get(t, h, "/v1/concentration/epochs?from="+max+"&to="+max)
	require.Equal(t, 200, code)
	require.Empty(t, body["epochs"])

	code, body = get(t, h, "/v1/concentration/epochs?from=0&to="+max)
	require.Equal(t, 200, code)
	require.Len(t, body["epochs"], 1)
}

// What the service stores, /v1/entities can serve: an epoch with non-integer
// tokens is refused at write time, so the entity view stays on the last good one.
func TestEntitiesNeverFailOnWhatWasStored(t *testing.T) {
	s, h := newServer(t)
	good := []store.EntityRow{{EntityID: "x", DisplayName: "X", Status: "ENTITY_STATUS_ATTESTED", Tokens: sp("800")}}
	seed(t, s, readRow(3), fixturePowers(), good)
	bad := []store.EntityRow{{EntityID: "x", DisplayName: "X", Status: "ENTITY_STATUS_ATTESTED", Tokens: sp("")}}
	require.Error(t, s.WriteConcentration(context.Background(), "c1", readRow(4), fixturePowers(), bad))

	code, body := get(t, h, "/v1/entities")
	require.Equal(t, 200, code)
	require.Equal(t, float64(3), body["epoch"])
}
