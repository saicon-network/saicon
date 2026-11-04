package chain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
)

const heightHeader = "X-Cosmos-Block-Height"
const grpcHeightHeader = "Grpc-Metadata-X-Cosmos-Block-Height"

// fixture reads testdata/<name>.json.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	require.NoError(t, err)
	return b
}

// fixtureStatus is the HTTP status captured next to a fixture, 200 when none was.
func fixtureStatus(t *testing.T, name string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".status"))
	if os.IsNotExist(err) {
		return http.StatusOK
	}
	require.NoError(t, err)
	code, err := strconv.Atoi(strings.TrimSpace(string(b)))
	require.NoError(t, err)
	return code
}

// serveFixture answers every request with testdata/<name>.json under the status in
// testdata/<name>.status (200 when absent). A pinned request is answered the way the
// chain's gateway does: a 200 echoes the request's own height in both the plain and the
// Grpc-Metadata header, a 404 only in the Grpc-Metadata one, and every other status
// carries none. A test that needs a missing or wrong echo writes its own handler.
func serveFixture(t *testing.T, name string) http.HandlerFunc {
	body := fixture(t, name)
	status := fixtureStatus(t, name)
	return func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("x-cosmos-block-height"); h != "" {
			switch status {
			case http.StatusOK:
				w.Header().Set(heightHeader, h)
				w.Header().Set(grpcHeightHeader, h)
			case http.StatusNotFound:
				w.Header().Set(grpcHeightHeader, h)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// fixtureField digs a dotted path out of a JSON fixture and returns it as a string.
func fixtureField(t *testing.T, name, path string) string {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal(fixture(t, name), &v))
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		require.True(t, ok, "%s: %s is not an object", name, k)
		v = m[k]
	}
	return fmt.Sprint(v)
}

func TestPinnedReadSendsTheHeight(t *testing.T) {
	var sent []string
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.Header.Get("x-cosmos-block-height"))
		serveFixture(t, "rest-entities")(w, r)
	}))
	got, err := chain.NewREST(base).EntitiesAt(context.Background(), 410)
	require.NoError(t, err)
	require.Equal(t, []string{"410"}, sent)
	require.Len(t, got, 2)
	statuses := []string{got[0].Status, got[1].Status}
	require.ElementsMatch(t, []string{"ENTITY_STATUS_ATTESTED", "ENTITY_STATUS_REVOKED"}, statuses)
	for _, e := range got {
		require.NotEmpty(t, e.ID)
		require.NotEmpty(t, e.DisplayName)
		require.Positive(t, e.AttestedHeight)
	}
}

func TestPinnedReadRefusesAMissingHeightEcho(t *testing.T) {
	body := fixture(t, "rest-entities")

	t.Run("no header at all", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		}))
		got, err := chain.NewREST(base).EntitiesAt(context.Background(), 410)
		require.Error(t, err)
		require.Nil(t, got)
		require.NotErrorIs(t, err, chain.ErrHeightUnavailable)
		require.NotErrorIs(t, err, chain.ErrHeightAhead)
	})

	t.Run("only the Grpc-Metadata echo", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(grpcHeightHeader, "410")
			_, _ = w.Write(body)
		}))
		_, err := chain.NewREST(base).EntitiesAt(context.Background(), 410)
		require.Error(t, err)
	})

	t.Run("an echo of another height names both", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(heightHeader, "409")
			_, _ = w.Write(body)
		}))
		_, err := chain.NewREST(base).EntitiesAt(context.Background(), 410)
		require.Error(t, err)
		require.Contains(t, err.Error(), "409")
		require.Contains(t, err.Error(), "410")
	})

	t.Run("a Grpc-Metadata echo that disagrees", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(heightHeader, "410")
			w.Header().Set(grpcHeightHeader, "409")
			_, _ = w.Write(body)
		}))
		_, err := chain.NewREST(base).EntitiesAt(context.Background(), 410)
		require.Error(t, err)
		require.Contains(t, err.Error(), "409")
	})

	t.Run("a NotFound that echoes another height is an error, not no-record", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(grpcHeightHeader, "409")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(fixture(t, "rest-entity-stake-revoked"))
		}))
		st, err := chain.NewREST(base).EntityStakeAt(context.Background(), 410, "demo-third")
		require.Error(t, err)
		require.Nil(t, st)
	})
}

func TestPrunedBodiesAreUnavailable(t *testing.T) {
	ctx := context.Background()

	t.Run("REST pruned", func(t *testing.T) {
		base := serve(t, serveFixture(t, "rest-pruned"))
		_, err := chain.NewREST(base).EntitiesAt(ctx, 20)
		require.ErrorIs(t, err, chain.ErrHeightUnavailable)
		require.NotErrorIs(t, err, chain.ErrHeightAhead)
	})

	t.Run("REST future height", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":2, "message":"codespace sdk code 26: invalid height: cannot query with height in the future; please provide a valid height", "details":[]}`))
		}))
		_, err := chain.NewREST(base).EntitiesAt(ctx, 99999999)
		require.ErrorIs(t, err, chain.ErrHeightAhead)
		require.NotErrorIs(t, err, chain.ErrHeightUnavailable)
	})

	t.Run("REST pruned answered with any status is still classified by its body", func(t *testing.T) {
		body := fixture(t, "rest-pruned")
		for _, code := range []int{http.StatusOK, http.StatusBadGateway} {
			base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write(body)
			}))
			_, err := chain.NewREST(base).EntitiesAt(ctx, 20)
			require.Error(t, err, "status %d", code)
			if code != http.StatusOK {
				require.ErrorIs(t, err, chain.ErrHeightUnavailable, "status %d", code)
			}
		}
	})

	t.Run("an unclassified 500 is a plain error", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":13,"message":"internal"}`))
		}))
		_, err := chain.NewREST(base).EntitiesAt(ctx, 20)
		require.Error(t, err)
		require.NotErrorIs(t, err, chain.ErrHeightUnavailable)
		require.NotErrorIs(t, err, chain.ErrHeightAhead)
		require.Contains(t, err.Error(), "internal")
	})

	t.Run("RPC unavailable, with the captured status and with 200", func(t *testing.T) {
		body := fixture(t, "rpc-error-unavailable")
		for _, code := range []int{fixtureStatus(t, "rpc-error-unavailable"), http.StatusOK} {
			base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write(body)
			}))
			_, err := chain.NewRPC(base).ValidatorSetAt(ctx, 5)
			require.ErrorIs(t, err, chain.ErrHeightUnavailable, "status %d", code)
			require.NotErrorIs(t, err, chain.ErrHeightAhead)
			_, err = chain.NewRPC(base).BlockHashAt(ctx, 5)
			require.ErrorIs(t, err, chain.ErrHeightUnavailable, "status %d", code)
		}
	})

	t.Run("RPC future", func(t *testing.T) {
		body := fixture(t, "rpc-error-future")
		for _, code := range []int{fixtureStatus(t, "rpc-error-future"), http.StatusOK} {
			base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write(body)
			}))
			_, err := chain.NewRPC(base).ValidatorSetAt(ctx, 99999999)
			require.ErrorIs(t, err, chain.ErrHeightAhead, "status %d", code)
			require.NotErrorIs(t, err, chain.ErrHeightUnavailable)
			_, err = chain.NewRPC(base).BlockHashAt(ctx, 99999999)
			require.ErrorIs(t, err, chain.ErrHeightAhead, "status %d", code)
		}
	})

	t.Run("an RPC error that names neither is a plain error", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":-1,"error":{"code":-32603,"message":"Internal error","data":"boom"}}`))
		}))
		_, err := chain.NewRPC(base).ValidatorSetAt(ctx, 5)
		require.Error(t, err)
		require.NotErrorIs(t, err, chain.ErrHeightUnavailable)
		require.NotErrorIs(t, err, chain.ErrHeightAhead)
		require.Contains(t, err.Error(), "boom")
	})

	t.Run("a gRPC NotFound from a pinned read stays no-record", func(t *testing.T) {
		base := serve(t, serveFixture(t, "rest-entity-stake-revoked"))
		st, err := chain.NewREST(base).EntityStakeAt(ctx, 50, "demo-third")
		require.NoError(t, err)
		require.Nil(t, st)

		// The same NotFound on a read that has no "no record" reading is an error,
		// and still not an unavailable height.
		_, err = chain.NewREST(base).EntityAt(ctx, 50, "demo-third")
		require.Error(t, err)
		require.NotErrorIs(t, err, chain.ErrHeightUnavailable)
	})
}

// headerFile is a captured response head: status and headers, as written by curl -D.
type headerFile struct {
	status int
	header http.Header
}

// parseHeaderFile reads testdata/<name>.headers. The files may be CRLF, carry a trailing
// blank line, and (for a chunked reply) trailer lines after the first blank line, which
// are not part of the head.
func parseHeaderFile(t *testing.T, name string) headerFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".headers"))
	require.NoError(t, err)
	hf := headerFile{header: http.Header{}}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break
		}
		if i == 0 {
			fields := strings.Fields(line)
			require.GreaterOrEqual(t, len(fields), 2, "status line %q", line)
			hf.status, err = strconv.Atoi(fields[1])
			require.NoError(t, err)
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		require.True(t, ok, "header line %q", line)
		hf.header.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	return hf
}

// serveCaptured replays a captured response head and the fixture body next to it. The
// framing headers are left to net/http, which recomputes them.
func serveCaptured(t *testing.T, name string) http.HandlerFunc {
	hf := parseHeaderFile(t, name)
	body := fixture(t, name)
	return func(w http.ResponseWriter, r *http.Request) {
		for k, vs := range hf.header {
			switch http.CanonicalHeaderKey(k) {
			case "Content-Length", "Transfer-Encoding", "Trailer", "Date":
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(hf.status)
		_, _ = w.Write(body)
	}
}

// The three captured heads are what the live node sent: a 200 with both height headers,
// a 404 with only the Grpc-Metadata one, and a pruned 500 with none.
func TestCapturedHeadsAreHonoured(t *testing.T) {
	ctx := context.Background()

	entities := parseHeaderFile(t, "rest-entities")
	h, err := strconv.ParseInt(entities.header.Get(heightHeader), 10, 64)
	require.NoError(t, err)
	base := serve(t, serveCaptured(t, "rest-entities"))
	got, err := chain.NewREST(base).EntitiesAt(ctx, h)
	require.NoError(t, err)
	require.Len(t, got, 2)
	_, err = chain.NewREST(base).EntitiesAt(ctx, h+1)
	require.Error(t, err, "the captured echo is for another height")

	notFound := parseHeaderFile(t, "rest-entity-stake-revoked")
	require.Empty(t, notFound.header.Get(heightHeader), "a captured 404 carries no plain height header")
	h, err = strconv.ParseInt(notFound.header.Get(grpcHeightHeader), 10, 64)
	require.NoError(t, err)
	base = serve(t, serveCaptured(t, "rest-entity-stake-revoked"))
	st, err := chain.NewREST(base).EntityStakeAt(ctx, h, "demo-third")
	require.NoError(t, err)
	require.Nil(t, st)
	_, err = chain.NewREST(base).EntityStakeAt(ctx, h+1, "demo-third")
	require.Error(t, err, "a NotFound echoing another height is not trusted")

	base = serve(t, serveCaptured(t, "rest-pruned"))
	_, err = chain.NewREST(base).EntitiesAt(ctx, 20)
	require.ErrorIs(t, err, chain.ErrHeightUnavailable)
}

func TestValidatorSetPages(t *testing.T) {
	ctx := context.Background()
	const height = 77

	member := func(i int) map[string]any {
		return map[string]any{
			"address":           fmt.Sprintf("%040X", i),
			"pub_key":           map[string]any{"type": "tendermint/PubKeyEd25519", "value": fmt.Sprintf("key-%d", i)},
			"voting_power":      strconv.Itoa(1000 + i),
			"proposer_priority": "0",
		}
	}
	page := func(from, to, count int, total string) []byte {
		var vs []map[string]any
		for i := from; i < to; i++ {
			vs = append(vs, member(i))
		}
		b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": -1, "result": map[string]any{
			"block_height": strconv.Itoa(height), "validators": vs, "count": strconv.Itoa(count), "total": total,
		}})
		require.NoError(t, err)
		return b
	}

	t.Run("150 members over two pages", func(t *testing.T) {
		var queries []string
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			queries = append(queries, r.URL.RawQuery)
			require.Equal(t, "/validators", r.URL.Path)
			require.Equal(t, "77", r.URL.Query().Get("height"))
			require.Equal(t, "100", r.URL.Query().Get("per_page"))
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = w.Write(page(0, 100, 100, "150"))
			case "2":
				_, _ = w.Write(page(100, 150, 50, "150"))
			default:
				t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
			}
		}))
		got, err := chain.NewRPC(base).ValidatorSetAt(ctx, height)
		require.NoError(t, err)
		require.Len(t, got, 150)
		require.Len(t, queries, 2)
		for i, m := range got {
			require.Equal(t, fmt.Sprintf("%040X", i), m.Address)
			require.Equal(t, fmt.Sprintf("key-%d", i), m.PubKey)
			require.Equal(t, int64(1000+i), m.VotingPower)
		}
	})

	t.Run("fewer than total with no more pages", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write(page(0, 100, 100, "150"))
				return
			}
			_, _ = w.Write(page(0, 0, 0, "150"))
		}))
		got, err := chain.NewRPC(base).ValidatorSetAt(ctx, height)
		require.Error(t, err)
		require.Nil(t, got)
	})

	t.Run("a different height in the answer", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(page(0, 3, 3, "3"))
		}))
		_, err := chain.NewRPC(base).ValidatorSetAt(ctx, height+1)
		require.Error(t, err)
		require.Contains(t, err.Error(), "77")
		require.Contains(t, err.Error(), "78")
	})

	t.Run("a duplicate address", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = w.Write(page(0, 100, 100, "101"))
			default:
				_, _ = w.Write(page(99, 100, 1, "101")) // member 99 again
			}
		}))
		got, err := chain.NewRPC(base).ValidatorSetAt(ctx, height)
		require.Error(t, err)
		require.Nil(t, got)
		require.Contains(t, err.Error(), fmt.Sprintf("%040X", 99))
	})

	t.Run("the captured set", func(t *testing.T) {
		h, err := strconv.ParseInt(fixtureField(t, "rpc-validators", "result.block_height"), 10, 64)
		require.NoError(t, err)
		base := serve(t, serveFixture(t, "rpc-validators"))
		got, err := chain.NewRPC(base).ValidatorSetAt(ctx, h)
		require.NoError(t, err)
		require.Len(t, got, 3)
		for _, m := range got {
			require.Regexp(t, `^[0-9A-F]{40}$`, m.Address)
			require.NotEmpty(t, m.PubKey)
			require.Positive(t, m.VotingPower)
		}
	})
}

func TestBlockHashAt(t *testing.T) {
	h, err := strconv.ParseInt(fixtureField(t, "rpc-commit", "result.signed_header.header.height"), 10, 64)
	require.NoError(t, err)
	want := fixtureField(t, "rpc-commit", "result.signed_header.commit.block_id.hash")
	require.Regexp(t, regexp.MustCompile(`^[0-9A-F]{64}$`), want)

	var query string
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Path + "?" + r.URL.RawQuery
		serveFixture(t, "rpc-commit")(w, r)
	}))
	got, err := chain.NewRPC(base).BlockHashAt(context.Background(), h)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, fmt.Sprintf("/commit?height=%d", h), query)

	_, err = chain.NewRPC(base).BlockHashAt(context.Background(), h+1)
	require.Error(t, err, "the commit answered is for another height")
}

func TestEntityLayerFrom(t *testing.T) {
	ctx := context.Background()
	versions := fixture(t, "rest-module-versions")
	plan := func(height string) []byte { return []byte(`{"height":"` + height + `"}`) }

	handler := func(versionsStatus int, versionsBody, planBody []byte) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasPrefix(r.URL.Path, "/cosmos/upgrade/v1beta1/module_versions"):
				require.Equal(t, "entityregistry", r.URL.Query().Get("module_name"))
				require.Empty(t, r.Header.Get("x-cosmos-block-height"), "the layer start is read at the tip")
				w.WriteHeader(versionsStatus)
				_, _ = w.Write(versionsBody)
			case r.URL.Path == "/cosmos/upgrade/v1beta1/applied_plan/"+chain.EntityLayerUpgrade:
				require.Empty(t, r.Header.Get("x-cosmos-block-height"))
				_, _ = w.Write(planBody)
			default:
				t.Errorf("unexpected path %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		})
	}

	t.Run("from genesis", func(t *testing.T) {
		base := serve(t, handler(200, versions, fixture(t, "rest-applied-plan")))
		from, present, err := chain.NewREST(base).EntityLayerFrom(ctx)
		require.NoError(t, err)
		require.True(t, present)
		require.Equal(t, int64(1), from)
	})

	t.Run("upgraded in place", func(t *testing.T) {
		base := serve(t, handler(200, versions, plan("812")))
		from, present, err := chain.NewREST(base).EntityLayerFrom(ctx)
		require.NoError(t, err)
		require.True(t, present)
		require.Equal(t, int64(812), from)
	})

	t.Run("the module is absent", func(t *testing.T) {
		body := strings.ReplaceAll(string(fixture(t, "rest-module-versions-absent")), "nosuchmodule", "entityregistry")
		require.Contains(t, body, "x/upgrade: QueryModuleVersions module entityregistry not found")
		base := serve(t, handler(fixtureStatus(t, "rest-module-versions-absent"), []byte(body), nil))
		from, present, err := chain.NewREST(base).EntityLayerFrom(ctx)
		require.NoError(t, err)
		require.False(t, present)
		require.Zero(t, from)
	})

	t.Run("an empty list is absent", func(t *testing.T) {
		base := serve(t, handler(200, []byte(`{"module_versions":[]}`), nil))
		from, present, err := chain.NewREST(base).EntityLayerFrom(ctx)
		require.NoError(t, err)
		require.False(t, present)
		require.Zero(t, from)
	})

	t.Run("any other 500 is an error", func(t *testing.T) {
		for _, body := range []string{
			`{"code":13,"message":"internal"}`,
			// another module, and the bare phrase without its prefix, are not "absent" either
			`{"code":2,"message":"x/upgrade: QueryModuleVersions module bank not found"}`,
			`{"code":2,"message":"module entityregistry not found"}`,
		} {
			base := serve(t, handler(500, []byte(body), nil))
			from, present, err := chain.NewREST(base).EntityLayerFrom(ctx)
			require.Error(t, err, body)
			require.False(t, present)
			require.Zero(t, from)
		}
	})

	t.Run("the absent text under another status is an error", func(t *testing.T) {
		body := `{"code":2,"message":"x/upgrade: QueryModuleVersions module entityregistry not found"}`
		base := serve(t, handler(200, []byte(body), nil))
		_, present, err := chain.NewREST(base).EntityLayerFrom(ctx)
		require.Error(t, err)
		require.False(t, present)
	})

	t.Run("a failing applied plan is an error", func(t *testing.T) {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "module_versions") {
				_, _ = w.Write(versions)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":13,"message":"internal"}`))
		}))
		_, present, err := chain.NewREST(base).EntityLayerFrom(ctx)
		require.Error(t, err)
		require.False(t, present)
	})
}
