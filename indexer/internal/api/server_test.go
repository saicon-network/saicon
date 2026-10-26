package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/api"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

func newServer(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoError(t, s.SetChain(context.Background(), "c1", time.Now()))
	h := api.New(s, "c1", func(context.Context) (int64, error) { return 120, nil }, &api.Stopped{})
	return s, h
}

// A stalled indexer must be visible. Every other endpoint keeps answering with
// yesterday's numbers as though they were current; this is the one that says so.
func TestStatusReportsLag(t *testing.T) {
	s, h := newServer(t)
	ctx := context.Background()
	require.NoError(t, s.RecordBlock(ctx, "c1", 100, 1, "A"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		ChainID       string `json:"chain_id"`
		IndexedHeight int64  `json:"indexed_height"`
		TipHeight     int64  `json:"tip_height"`
		LagBlocks     int64  `json:"lag_blocks"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "c1", body.ChainID)
	require.Equal(t, int64(100), body.IndexedHeight)
	require.Equal(t, int64(120), body.TipHeight)
	require.Equal(t, int64(20), body.LagBlocks)
}

func TestUnknownValidatorIs404(t *testing.T) {
	_, h := newServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/validators/saiconvaloper1nope", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// The database keeps these distinctions; the JSON has to keep them too. A COALESCE
// in the scoreboard query, or a non-pointer struct field, would put back exactly
// the confusion the columns exist to prevent — one layer up, where it is harder
// to see.
func TestAbsentFieldsAreServedAsNullNotAsZero(t *testing.T) {
	s, h := newServer(t)
	ctx := context.Background()

	// A validator in the staking set with no registry record, no compliance
	// ruling and no epoch read yet — all three absences at once.
	require.NoError(t, s.UpsertValidator(ctx, "c1", store.ValidatorRow{
		Operator: "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd",
		Moniker:  "unbonded", StakingStatus: "BOND_STATUS_UNBONDED", Tokens: "0",
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/scoreboard", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Validators []map[string]any `json:"validators"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Validators, 1)
	v := body.Validators[0]
	for _, field := range []string{"registry_status", "lockup_seconds", "compliance_flag", "accumulated_ep", "computed_score", "proposals", "latest_epoch"} {
		val, present := v[field]
		require.True(t, present, "%s must appear in the JSON", field)
		require.Nil(t, val, "%s must be null, not a zero or an empty string", field)
	}
}

func TestScoreboardIsEmptyNotNull(t *testing.T) {
	_, h := newServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/scoreboard", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"validators":[]}`, rec.Body.String(),
		"an empty index returns an empty list, never null")
}

// A service that cannot see the chain is not healthy, whatever its own state.
func TestStatusIs503WhenTheChainIsUnreachable(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoError(t, s.SetChain(context.Background(), "c1", time.Now()))

	h := api.New(s, "c1", func(context.Context) (int64, error) {
		return 0, errors.New("dial tcp: connection refused")
	}, &api.Stopped{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"a monitor reading only the status code must not see this as healthy")
	require.Contains(t, rec.Body.String(), "connection refused")
	require.Contains(t, rec.Body.String(), `"lag_blocks":null`)
}

// The zombie this closes: the loop itself decides to stop (a chain reset, a
// governance change to the epoch length) while the node stays reachable — the
// exact condition that got it there. Without Stopped, /v1/status would answer
// 200 with a lag that only grows, indistinguishable from ordinary catch-up,
// and a process that is alive and serving would never be restarted by
// `restart: unless-stopped`.
func TestStatusIs503OnceStoppedEvenWithAReachableChain(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoError(t, s.SetChain(context.Background(), "c1", time.Now()))
	require.NoError(t, s.RecordBlock(context.Background(), "c1", 100, 1, "A"))

	stopped := &api.Stopped{}
	h := api.New(s, "c1", func(context.Context) (int64, error) { return 120, nil }, stopped)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	require.Equal(t, http.StatusOK, rec.Code, "not stopped yet: the chain is reachable and this must read as healthy")

	stopped.Set("chain saicon-devnet-1 is at height 90 but the index is at 100: the chain was reset")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"a stopped loop must not read as healthy just because the node answers")
	require.Contains(t, rec.Body.String(), "was reset")
	require.Contains(t, rec.Body.String(), `"lag_blocks":20`, "the data already learned keeps being served")
}

// A caller-supplied range bound goes straight to the SQL driver as a query
// argument, and a uint64 with its high bit set was refused by
// modernc.org/sqlite even though the endpoint's own default had been clamped.
func TestEpochsClampsAnOutOfRangeBound(t *testing.T) {
	_, h := newServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/validators/saiconvaloper1nope/epochs?from=18446744073709551615", nil))
	require.Equal(t, http.StatusOK, rec.Code, "an out-of-range bound must be clamped, not sent to the driver as-is")
}

// Two stored operators differing only in case are bad data — the chain emits
// lower case — and resolving that ambiguity by taking whichever row sorted
// first served one validator's tokens under another's name.
func TestAmbiguousCaseFoldMatchIs409(t *testing.T) {
	s, h := newServer(t)
	ctx := context.Background()

	require.NoError(t, s.UpsertValidator(ctx, "c1", store.ValidatorRow{
		Operator: "saiconvaloper1abc", Moniker: "lower", StakingStatus: "BOND_STATUS_BONDED", Tokens: "1",
	}))
	require.NoError(t, s.UpsertValidator(ctx, "c1", store.ValidatorRow{
		Operator: "SAICONVALOPER1ABC", Moniker: "upper", StakingStatus: "BOND_STATUS_BONDED", Tokens: "2",
	}))

	// A third casing that exactly matches neither stored row, so the exact-match
	// fast path can't short-circuit the ambiguity.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/validators/SaiconValoper1Abc", nil))
	require.Equal(t, http.StatusConflict, rec.Code)
}
