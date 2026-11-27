package chain_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
)

// rest-preview.json is the preview as a local node built from feat/s10-warp
// answered it, pinned at height 80 (epoch 8 of a 10-block epoch), captured with
// its headers: one validator with full uptime and two that never signed.

func TestPreviewIsReadPinnedAndKeptAsTheChainWroteIt(t *testing.T) {
	var sent []string
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/saicon/chain/rewards/v1/voting_power_preview", r.URL.Path)
		sent = append(sent, r.Header.Get("x-cosmos-block-height"))
		serveFixture(t, "rest-preview")(w, r)
	}))
	h := int64(80)
	p, err := chain.NewREST(base).VotingPowerPreviewAt(context.Background(), h)
	require.NoError(t, err)
	require.Equal(t, []string{"80"}, sent, "the read is pinned to the epoch's last height")

	require.Equal(t, h, p.Height)
	require.Equal(t, uint64(8), p.Epoch)
	require.Equal(t, "65.000000000000000000", p.Threshold)
	require.Equal(t, "80.000000000000000000", p.Initial)
	require.Equal(t, "200000000000000", p.Cap)
	require.Equal(t, "91390655800000", p.TotalVP)
	require.Len(t, p.Validators, 3)

	in, out := p.Validators[0], p.Validators[1]
	require.True(t, in.Eligible)
	require.Empty(t, in.Reason)
	require.True(t, in.WarpMeasured)
	// "1.000000", not "1": a share is the chain's string, never re-rendered.
	require.Equal(t, "1.000000", in.VPShare)
	require.Equal(t, "0.996015", in.StockShare)
	require.Equal(t, "91.390655800000000000", in.Warp)
	require.Equal(t, "91390655800000", in.VP)

	require.False(t, out.Eligible)
	require.Equal(t, "warp below threshold", out.Reason)
	require.Equal(t, "0", out.VPShare, "an ineligible validator's share is the chain's \"0\"")
	require.Equal(t, "840350000", out.VP, "its vp is still computed")
	require.Equal(t, "0.420175000000000000", out.Warp)
}

// Every definitive answer, and the code it is stored under. The first case is
// the live DevNet's own answer, which predates the route.
func TestPreviewUnavailableCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		code   string
	}{
		{"route missing: 501 Unimplemented (live DevNet)", 501, string(fixture(t, "rest-preview-route-missing")), chain.PreviewRouteMissing},
		{"route missing: a 501 with no body", 501, ``, chain.PreviewRouteMissing},
		{"route missing: Unimplemented under another status", 500, `{"code":12,"message":"unknown method"}`, chain.PreviewRouteMissing},
		{"route missing: the gateway's own NotFound for an unknown path", 404, `{"code":5,"message":"Not Found","details":[]}`, chain.PreviewRouteMissing},
		{"params invalid: FailedPrecondition", 400, `{"code":9,"message":"x/participation WARP parameters do not validate: fall must be in (0, 1]","details":[]}`, chain.PreviewParamsInvalid},
		{"caps missing: NotFound from x/stakeengine", 404, `{"code":5,"message":"stake caps have not been computed","details":[]}`, chain.PreviewCapsMissing},
		{"pruned", 500, string(fixture(t, "rest-pruned")), chain.PreviewPruned},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			p, err := chain.NewREST(base).VotingPowerPreviewAt(context.Background(), 80)
			require.Nil(t, p)
			var un *chain.PreviewUnavailable
			require.True(t, errors.As(err, &un), "want an unavailable code, got %v", err)
			require.Equal(t, c.code, un.Code)
		})
	}
}

// Everything else is transient: an ordinary error, so the caller asks again
// rather than store a code that would never be revisited.
func TestPreviewTransientFailuresAreNotCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"a future height", 500, `{"code":2, "message":"codespace sdk code 26: invalid height: cannot query with height in the future; please provide a valid height", "details":[]}`},
		{"Internal", 500, `{"code":13,"message":"stored WARP of x is not set","details":[]}`},
		{"a proxy's 404 with no gRPC code", 404, `<html><body>404 Not Found</body></html>`},
		{"a 404 with a JSON body but no code", 404, `{"error":"not found"}`},
		{"a proxy's 502", 502, `bad gateway`},
		{"a 503", 503, `{"code":14,"message":"unavailable"}`},
		{"a 200 that is not JSON", 200, `<html>`},
		{"a 200 with a non-integer vp", 200, `{"height":"80","epoch":"8","params":{"warp_threshold":"65","warp_initial":"80","validator_cap":"1"},"validators":[{"operator_address":"x","vp":"1.5","vp_share":"1.000000","stock_share":"1.000000","warp":"80"}],"total_vp":"1"}`},
		{"a 200 with a float share", 200, `{"height":"80","epoch":"8","params":{"warp_threshold":"65","warp_initial":"80","validator_cap":"1"},"validators":[{"operator_address":"x","vp":"1","vp_share":1,"stock_share":"1.000000","warp":"80"}],"total_vp":"1"}`},
		{"a 200 whose body names another height", 200, `{"height":"79","epoch":"7","params":{"warp_threshold":"65","warp_initial":"80","validator_cap":"1"},"validators":[],"total_vp":"0"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.status == http.StatusOK {
					w.Header().Set(heightHeader, r.Header.Get("x-cosmos-block-height"))
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			p, err := chain.NewREST(base).VotingPowerPreviewAt(context.Background(), 80)
			require.Nil(t, p)
			require.Error(t, err)
			var un *chain.PreviewUnavailable
			require.False(t, errors.As(err, &un), "transient, not a code: %v", err)
		})
	}

	t.Run("a 200 without the height echo", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(fixture(t, "rest-preview"))
		}))
		p, err := chain.NewREST(base).VotingPowerPreviewAt(context.Background(), 80)
		require.Nil(t, p, "current state labelled as history is not a preview")
		var un *chain.PreviewUnavailable
		require.False(t, errors.As(err, &un))
	})

	t.Run("no answer at all", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		base := srv.URL
		srv.Close()
		_, err := chain.NewREST(base).VotingPowerPreviewAt(context.Background(), 80)
		require.Error(t, err)
		var un *chain.PreviewUnavailable
		require.False(t, errors.As(err, &un))
	})

	t.Run("a cancelled context", func(t *testing.T) {
		base := serve(t, serveFixture(t, "rest-preview"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := chain.NewREST(base).VotingPowerPreviewAt(ctx, 80)
		require.Error(t, err)
		var un *chain.PreviewUnavailable
		require.False(t, errors.As(err, &un))
	})

	t.Run("a future height keeps its sentinel", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":2, "message":"codespace sdk code 26: invalid height: cannot query with height in the future; please provide a valid height", "details":[]}`))
		}))
		_, err := chain.NewREST(base).VotingPowerPreviewAt(context.Background(), 80)
		require.ErrorIs(t, err, chain.ErrHeightAhead)
	})
}

// The snapshot's warp: a decimal from 0 to 100 is kept verbatim; null and
// absent are nil; anything else is nil too, and reported as invalid.
func TestSnapshotWarp(t *testing.T) {
	const op = "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd"
	snap := func(warp string) *chain.Snapshot {
		t.Helper()
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"snapshot":{"computed_score":"1.000000000000000000","computed_at":"2026-11-24T00:00:00Z",` +
				`"blocks_signed":"10","blocks_missed":"0"` + warp + `}}`))
		}))
		s, err := chain.NewREST(base).Snapshot(context.Background(), op, 3)
		require.NoError(t, err, "a bad warp never fails the snapshot")
		require.NotNil(t, s)
		return s
	}

	for _, v := range []string{"85.420000000000000000", "0.000000000000000000", "0", "100", "100.000000000000000000"} {
		got := snap(`,"warp":"` + v + `"`)
		require.NotNil(t, got.Warp, v)
		require.Equal(t, v, *got.Warp, "stored as the chain wrote it, not rounded")
		require.Empty(t, got.InvalidWarp)
	}

	none := snap(`,"warp":null`)
	require.Nil(t, none.Warp, "a pre-WARP snapshot answers null: unknown, not 0")
	require.Empty(t, none.InvalidWarp)
	absent := snap(``)
	require.Nil(t, absent.Warp, "a node that predates the field omits it: unknown, not 0")
	require.Empty(t, absent.InvalidWarp)

	for _, raw := range []string{`""`, `"-1"`, `"1e2"`, `"NaN"`, `"101"`, `"100.000000000000000001"`, `" 50"`, `85.4`, `true`, `{}`} {
		got := snap(`,"warp":` + raw)
		require.Nil(t, got.Warp, raw)
		require.Equal(t, raw, got.InvalidWarp, "what the node sent, for the log")
	}
}
