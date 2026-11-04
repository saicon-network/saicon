package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/concentration"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

const bech32Chars = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// layerMode is what the fake chain says about its entity layer.
type layerMode int

const (
	layerAbsent  layerMode = iota // module_versions: module not found
	layerPresent                  // module_versions lists it; applied plan at layerFrom
	layerError                    // module_versions fails with a 503
)

// fakeChain serves the RPC and REST answers the concentration pass asks for,
// for a set of n validators of equal power. Validator i < members is the one
// member of entity i; the remaining entities have no members, and validators
// members..n-1 are registry-ACTIVE outside any entity. Every entity is attested.
type fakeChain struct {
	t        *testing.T
	n        int
	entities int
	members  int

	mu        sync.Mutex
	layer     layerMode
	layerFrom int64
	// fail, when set, answers a request with HTTP 500 instead of the chain's answer.
	fail func(r *http.Request) bool
	// seen, when set, is told of every RPC and REST request before it is answered.
	seen func(r *http.Request)
}

func (f *fakeChain) saw(r *http.Request) {
	f.mu.Lock()
	seen := f.seen
	f.mu.Unlock()
	if seen != nil {
		seen(r)
	}
}

func (f *fakeChain) setLayer(m layerMode, from int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.layer, f.layerFrom = m, from
}

// data is a 32-character bech32 data part plus a 6-character checksum (not
// verified by chain.AddrKey), distinct per validator.
func data(i int) string {
	var b strings.Builder
	for k := 0; k < 32; k++ {
		b.WriteByte(bech32Chars[(i>>(5*(k%3)))%32])
	}
	b.WriteString(bech32Chars[i%32:i%32+1] + "qqqqq")
	return b.String()
}

func operator(i int) string { return "saiconvaloper1" + data(i) }
func account(i int) string  { return "saicon1" + data(i) }
func pubKey(i int) string {
	raw := make([]byte, 32)
	raw[0], raw[1] = byte(i), byte(i>>8)
	return base64.StdEncoding.EncodeToString(raw)
}
func entityID(i int) string { return fmt.Sprintf("ent-%03d", i) }

func (f *fakeChain) rpc(w http.ResponseWriter, r *http.Request) {
	f.saw(r)
	h := r.URL.Query().Get("height")
	var result any
	switch r.URL.Path {
	case "/validators":
		var vs []map[string]any
		for i := 0; i < f.n; i++ {
			addr, err := chain.ConsAddrFromPubKey(pubKey(i))
			require.NoError(f.t, err)
			vs = append(vs, map[string]any{"address": addr, "pub_key": map[string]any{"value": pubKey(i)},
				"voting_power": "1000"})
		}
		result = map[string]any{"block_height": h, "validators": vs, "total": strconv.Itoa(f.n)}
	case "/commit":
		result = map[string]any{"signed_header": map[string]any{
			"header": map[string]any{"height": h},
			"commit": map[string]any{"block_id": map[string]any{"hash": strings.Repeat("AB", 32)}},
		}}
	default:
		f.t.Errorf("rpc: unexpected path %s", r.URL.Path)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": -1, "result": result})
}

func (f *fakeChain) rest(w http.ResponseWriter, r *http.Request) {
	f.saw(r)
	f.mu.Lock()
	mode, from, fail := f.layer, f.layerFrom, f.fail
	f.mu.Unlock()
	if fail != nil && fail(r) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":13,"message":"injected fault"}`))
		return
	}

	p := r.URL.Path
	switch p {
	case "/cosmos/upgrade/v1beta1/module_versions":
		switch mode {
		case layerAbsent:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":2,"message":"x/upgrade: QueryModuleVersions module entityregistry not found"}`))
		case layerPresent:
			_, _ = w.Write([]byte(`{"module_versions":[{"name":"entityregistry","version":"1"}]}`))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`upstream unavailable`))
		}
		return
	case "/cosmos/upgrade/v1beta1/applied_plan/" + chain.EntityLayerUpgrade:
		_, _ = fmt.Fprintf(w, `{"height":"%d"}`, from)
		return
	}

	// Everything else is a pinned read: echo the height the way the gateway does.
	w.Header().Set("X-Cosmos-Block-Height", r.Header.Get("x-cosmos-block-height"))
	var body any
	switch {
	case p == "/cosmos/staking/v1beta1/validators":
		var vs []map[string]any
		for i := 0; i < f.n; i++ {
			vs = append(vs, map[string]any{"operator_address": operator(i), "tokens": "1000000000",
				"consensus_pubkey": map[string]any{"key": pubKey(i)}})
		}
		body = map[string]any{"validators": vs, "pagination": map[string]any{"next_key": nil}}
	case p == "/saicon/chain/validatorregistry/v1/validators":
		var vs []map[string]any
		for i := 0; i < f.n; i++ {
			vs = append(vs, map[string]any{"validator_address": account(i), "status": "STATUS_ACTIVE",
				"bonding_start_height": "1", "lockup_duration_seconds": "0"})
		}
		body = map[string]any{"validators": vs, "pagination": nil}
	case p == "/saicon/chain/stakeengine/v1/caps":
		body = map[string]any{"cap_state": map[string]any{"entity_cap": "5000000000"}}
	case p == "/saicon/chain/entityregistry/v1/params":
		body = map[string]any{"params": map[string]any{"max_active_seats": 2}}
	case p == "/saicon/chain/entityregistry/v1/entities":
		var es []map[string]any
		for i := 0; i < f.entities; i++ {
			es = append(es, map[string]any{"id": entityID(i), "display_name": "Entity " + strconv.Itoa(i),
				"status": "ENTITY_STATUS_ATTESTED", "attested_height": "1"})
		}
		body = map[string]any{"entities": es, "pagination": map[string]any{"next_key": nil}}
	case strings.HasPrefix(p, "/saicon/chain/entityregistry/v1/entities/"):
		id := strings.TrimPrefix(p, "/saicon/chain/entityregistry/v1/entities/")
		var i int
		_, err := fmt.Sscanf(id, "ent-%03d", &i)
		require.NoError(f.t, err)
		members, seats := []map[string]any{}, 0
		if i < f.members {
			members, seats = append(members, map[string]any{"validator": operator(i)}), 1
		}
		body = map[string]any{"members": members, "active_seats": seats}
	case strings.HasPrefix(p, "/saicon/chain/stakeengine/v1/entities/"):
		body = map[string]any{"tokens": "1000000000", "entity_cap": "5000000000", "room": "4000000000",
			"active_seats": 1, "max_active_seats": 2, "frozen": false}
	case strings.HasPrefix(p, "/saicon/chain/entityregistry/v1/validators/"):
		body = map[string]any{"attested": false, "entity_id": "", "pending": false}
	default:
		f.t.Errorf("rest: unexpected path %s", p)
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// harness is a real store and a concRunner on real clients against a fake chain.
type harness struct {
	f    *fakeChain
	s    *store.Store
	conc *concRunner
	logs []string
}

const testChain = "fake-1"

func newHarness(t *testing.T, f *fakeChain, limit int) *harness {
	t.Helper()
	f.t = t
	rpcSrv := httptest.NewServer(http.HandlerFunc(f.rpc))
	t.Cleanup(rpcSrv.Close)
	restSrv := httptest.NewServer(http.HandlerFunc(f.rest))
	t.Cleanup(restSrv.Close)

	s, err := store.Open(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoError(t, s.SetChain(context.Background(), testChain, time.Now()))

	h := &harness{f: f, s: s}
	logf := func(format string, args ...any) { h.logs = append(h.logs, fmt.Sprintf(format, args...)) }
	h.conc = newConcRunner(s, chain.NewRPC(rpcSrv.URL), chain.NewREST(restSrv.URL), testChain, 10, limit, logf)
	return h
}

// walkedTo sets the follower's cursor as if it had walked every block up to height.
func (h *harness) walkedTo(t *testing.T, height int64) {
	t.Helper()
	_, err := h.s.DB().ExecContext(context.Background(),
		`UPDATE chains SET indexed_height = ? WHERE chain_id = ?`, height, testChain)
	require.NoError(t, err)
}

// tick runs one real tick with the concentration pass wired to the harness and
// the S6/S7 steps recorded.
func (h *harness) tick(t *testing.T) []string {
	t.Helper()
	var calls []string
	rec := func(name string) { calls = append(calls, name) }
	d := tickDeps{
		epochLen:     10,
		epochLength:  func(context.Context) (uint64, error) { return 10, nil },
		newestHashed: func(context.Context) (int64, string, bool, error) { return 0, "", false, nil },
		hashAt:       func(context.Context, int64) (string, error) { return "", nil },
		followOnce: func(context.Context) (int64, error) {
			rec("followOnce")
			return 0, nil
		},
		epochPass: func(context.Context) { rec("epochPass") },
		concentrationPass: func(ctx context.Context) {
			rec("concentrationPass")
			h.conc.pass(ctx)
		},
		logf: func(string, ...any) {},
	}
	require.Empty(t, tick(context.Background(), d))
	return calls
}

func (h *harness) rows(t *testing.T) []store.ConcentrationRow {
	t.Helper()
	rows, err := h.s.ConcentrationSeries(context.Background(), testChain, 0, 1<<62)
	require.NoError(t, err)
	return rows
}

func code(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// A failing EntityLayerFrom, at startup or in a tick, stops nothing: the
// follower and the epoch pass run, and only the concentration pass waits.
func TestAnUnknownEntityLayerHoldsBackOnlyTheConcentrationPass(t *testing.T) {
	f := &fakeChain{n: 3, entities: 1, members: 1, layer: layerError}
	h := newHarness(t, f, ConcentrationCallBudget)
	h.walkedTo(t, 50)

	require.False(t, h.conc.resolveLayer(context.Background()), "the startup read fails and returns")
	require.Contains(t, strings.Join(h.logs, "\n"), "entity layer: ")

	require.Equal(t, []string{"followOnce", "epochPass", "concentrationPass"}, h.tick(t))
	require.Empty(t, h.rows(t), "no epoch is labelled while the layer is unknown")

	// An absent layer is an answer: the pass proceeds and says so per epoch.
	f.setLayer(layerAbsent, 0)
	h.tick(t)
	rows := h.rows(t)
	require.Len(t, rows, 5)
	for _, r := range rows {
		require.Equal(t, concentration.NoEntityLayer, code(r.EntitiesUnavailable), "epoch %d", r.Epoch)
	}
}

// The layer appears while the indexer runs: the epochs it wrote no_entity_layer
// at or after the layer's first height are read again with entity figures, the
// ones before it keep their rows, and new epochs are measured with the layer.
func TestTheEntityLayerAppearingMidRunIsPickedUp(t *testing.T) {
	ctx := context.Background()
	f := &fakeChain{n: 3, entities: 1, members: 1, layer: layerAbsent}
	h := newHarness(t, f, ConcentrationCallBudget)
	require.True(t, h.conc.resolveLayer(ctx))
	h.walkedTo(t, 100)
	h.tick(t)
	before := h.rows(t)
	require.Len(t, before, 10)
	for _, r := range before {
		require.Equal(t, concentration.NoEntityLayer, code(r.EntitiesUnavailable))
	}

	f.setLayer(layerPresent, 55) // epochs 6-10 end at 60-100, at or after 55
	h.walkedTo(t, 120)
	for i := 0; i < 3; i++ {
		h.tick(t)
	}
	after := h.rows(t)
	require.Len(t, after, 12)
	for _, r := range after {
		if r.Height < 55 {
			require.Equal(t, concentration.NoEntityLayer, code(r.EntitiesUnavailable), "epoch %d keeps its row", r.Epoch)
			require.Equal(t, before[r.Epoch-1].ReadAt, r.ReadAt, "epoch %d was not read again", r.Epoch)
			continue
		}
		require.Nil(t, r.EntitiesUnavailable, "epoch %d", r.Epoch)
		require.NotNil(t, r.UnitCount, "epoch %d", r.Epoch)
		require.Equal(t, int64(3), *r.UnitCount, "epoch %d", r.Epoch)
		require.Equal(t, int64(1), *r.EntityCount, "epoch %d", r.Epoch)
	}
	require.True(t, h.conc.rereadDone)
	_, ents, err := h.s.Entities(ctx, testChain, ptrU(8))
	require.NoError(t, err)
	require.Len(t, ents, 1)
	require.Equal(t, entityID(0), ents[0].EntityID)

	// Once present the layer is cached: no more module_versions reads.
	calls := h.conc.calls()
	h.tick(t)
	require.Equal(t, calls, h.conc.calls(), "nothing left to read, nothing asked")
}

func ptrU(v uint64) *uint64 { return &v }

// At 75 validators and 75 entities an epoch costs about 170 requests. One pass
// stays within the budget however many epochs are waiting, and the follower
// runs in the same tick.
func TestAPassOnALargeSetStaysWithinTheBudget(t *testing.T) {
	f := &fakeChain{n: 75, entities: 75, members: 60, layer: layerPresent, layerFrom: 1}
	const budget = 400
	h := newHarness(t, f, budget)
	h.walkedTo(t, 10_000) // a thousand epochs to backfill

	start := h.conc.calls()
	require.Equal(t, []string{"followOnce", "epochPass", "concentrationPass"}, h.tick(t))
	spent := h.conc.calls() - start
	require.LessOrEqual(t, spent, budget)
	rows := h.rows(t)
	require.Len(t, rows, 2, "7 + 75 entity reads + 75 stake reads + 15 frozen-seat reads = 172 an epoch")
	require.Equal(t, 172, h.conc.est)
	for _, r := range rows {
		require.Nil(t, r.EntitiesUnavailable)
		require.Equal(t, int64(75), *r.UnitCount)
	}

	start = h.conc.calls()
	h.tick(t)
	require.LessOrEqual(t, h.conc.calls()-start, budget)
	require.Len(t, h.rows(t), 4)
}

// The reviewer's scenario: at 75 validators and 75 entities under the default
// budget, the re-read of one epoch fails late (the last entity's stake read)
// on every attempt. The other re-read epochs must still be re-read, and the
// epochs waiting to be measured must still be measured.
func TestAFailingRereadStallsNeitherTheOthersNorNewEpochs(t *testing.T) {
	f := &fakeChain{n: 75, entities: 75, members: 60, layer: layerAbsent}
	h := newHarness(t, f, ConcentrationCallBudget)
	require.True(t, h.conc.resolveLayer(context.Background()))
	h.walkedTo(t, 30)
	h.tick(t)
	require.Len(t, h.rows(t), 3, "epochs 1-3 written no_entity_layer")

	f.setLayer(layerPresent, 1)
	attempts := 0
	f.mu.Lock()
	f.fail = func(r *http.Request) bool {
		hit := r.URL.Path == "/saicon/chain/stakeengine/v1/entities/"+entityID(74)+"/stake" &&
			r.Header.Get("x-cosmos-block-height") == "10"
		if hit {
			attempts++
		}
		return hit
	}
	f.mu.Unlock()
	h.walkedTo(t, 200) // epochs 4-20 wait to be measured

	ctx := context.Background()
	for i := 0; i < 25; i++ {
		before, _, err := h.s.HighestConcentrationEpoch(ctx, testChain)
		require.NoError(t, err)
		start := h.conc.calls()
		h.tick(t)
		require.LessOrEqual(t, h.conc.calls()-start, 2*ConcentrationCallBudget,
			"tick %d: at most the forward epoch and one re-read past the budget", i)
		after, _, err := h.s.HighestConcentrationEpoch(ctx, testChain)
		require.NoError(t, err)
		if before < 20 {
			require.Greater(t, after, before, "tick %d: the forward pass measures a new epoch every tick", i)
		}
	}
	rows := h.rows(t)
	require.Len(t, rows, 20, "every waiting epoch was measured")
	for _, r := range rows {
		if r.Epoch == 1 {
			require.Equal(t, concentration.NoEntityLayer, code(r.EntitiesUnavailable), "epoch 1 keeps failing")
			continue
		}
		require.Nil(t, r.EntitiesUnavailable, "epoch %d", r.Epoch)
	}
	require.False(t, h.conc.rereadDone, "epoch 1 is still waiting")
	require.LessOrEqual(t, attempts, 25/rereadBackoffPasses+1,
		"the failing epoch is retried once per %d passes, not every pass", rereadBackoffPasses)
	require.GreaterOrEqual(t, attempts, 2, "and it is retried")
}

// Within one pass a failed re-read moves on to the next epoch: on a small set
// with budget to spare, one failing epoch does not hold the others to the next
// pass.
func TestAFailedRereadMovesOnWithinThePass(t *testing.T) {
	f := &fakeChain{n: 3, entities: 1, members: 1, layer: layerAbsent}
	h := newHarness(t, f, ConcentrationCallBudget)
	require.True(t, h.conc.resolveLayer(context.Background()))
	h.walkedTo(t, 30)
	h.tick(t)
	require.Len(t, h.rows(t), 3)

	f.setLayer(layerPresent, 1)
	f.mu.Lock()
	f.fail = func(r *http.Request) bool {
		return strings.HasPrefix(r.URL.Path, "/saicon/chain/stakeengine/v1/entities/") &&
			r.Header.Get("x-cosmos-block-height") == "10"
	}
	f.mu.Unlock()
	h.tick(t)
	for _, r := range h.rows(t) {
		if r.Epoch == 1 {
			require.Equal(t, concentration.NoEntityLayer, code(r.EntitiesUnavailable))
			continue
		}
		require.Nil(t, r.EntitiesUnavailable, "epoch %d is re-read in the same pass", r.Epoch)
	}
	require.Equal(t, h.conc.passes+rereadBackoffPasses, h.conc.retryAt[1], "epoch 1 waits before it is tried again")
}

// rereadSetup writes epochs 1-10 no_entity_layer on a three-validator chain,
// then makes the layer present and moves the follower to height 150, so epochs
// 11-15 wait to be measured and 1-10 wait to be re-read.
func rereadSetup(t *testing.T) (*fakeChain, *harness) {
	t.Helper()
	f := &fakeChain{n: 3, entities: 1, members: 1, layer: layerAbsent}
	h := newHarness(t, f, ConcentrationCallBudget)
	require.True(t, h.conc.resolveLayer(context.Background()))
	h.walkedTo(t, 100)
	h.tick(t)
	require.Len(t, h.rows(t), 10)
	f.setLayer(layerPresent, 1)
	require.True(t, h.conc.resolveLayer(context.Background()))
	h.walkedTo(t, 150)
	return f, h
}

// pinnedHeight is the height a request asks about: the pinned header on REST,
// the height parameter on RPC.
func pinnedHeight(r *http.Request) int {
	v := r.Header.Get("x-cosmos-block-height")
	if v == "" {
		v = r.URL.Query().Get("height")
	}
	n, _ := strconv.Atoi(v)
	return n
}

// A gateway that fails every historical read must not hold a pass for one
// attempt per waiting epoch: three failures in a row end the re-read for the
// pass, and the new epochs were measured before it.
func TestTheRereadStopsAfterThreeFailuresInARow(t *testing.T) {
	f, h := rereadSetup(t)
	attempts := map[int]bool{}
	f.mu.Lock()
	f.fail = func(r *http.Request) bool {
		hgt := pinnedHeight(r)
		if hgt > 0 && hgt <= 100 && strings.HasPrefix(r.URL.Path, "/saicon/chain/stakeengine/v1/entities/") {
			attempts[hgt] = true
			return true
		}
		return false
	}
	f.mu.Unlock()

	h.logs = nil
	h.tick(t)
	require.Len(t, attempts, 3, "three epochs tried, then the re-read stops for this pass")
	require.Len(t, h.rows(t), 15, "the new epochs 11-15 were measured in the same tick")
	failed := 0
	for _, l := range h.logs {
		if strings.HasPrefix(l, "concentration re-read epoch") {
			failed++
		}
	}
	require.Equal(t, 3, failed)
}

// A context cancelled during the re-read (the service shutting down) ends it
// at once: no further epoch is attempted and nothing is logged per epoch.
func TestACancelledContextEndsTheRereadAtOnce(t *testing.T) {
	f, h := rereadSetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reread []int
	f.mu.Lock()
	f.seen = func(r *http.Request) {
		if hgt := pinnedHeight(r); hgt > 0 && hgt <= 100 && r.URL.Path == "/validators" {
			reread = append(reread, hgt)
			cancel() // shutdown arrives while the first re-read is in flight
		}
	}
	f.mu.Unlock()

	h.logs = nil
	h.conc.pass(ctx)
	require.Equal(t, []int{10}, reread, "only the epoch in flight was asked for")
	for _, l := range h.logs {
		require.NotContains(t, l, "concentration re-read epoch", "no line per epoch on shutdown")
	}
	require.Empty(t, h.conc.retryAt, "a shutdown is not a failure of the epoch")
}

// New epochs are read before any re-read in a pass.
func TestNewEpochsAreReadBeforeTheReread(t *testing.T) {
	f, h := rereadSetup(t)
	var order []int
	f.mu.Lock()
	f.seen = func(r *http.Request) {
		if r.URL.Path == "/validators" {
			order = append(order, pinnedHeight(r))
		}
	}
	f.mu.Unlock()
	h.tick(t)
	require.NotEmpty(t, order)
	seenReread := false
	for _, hgt := range order {
		if hgt <= 100 {
			seenReread = true
			continue
		}
		require.False(t, seenReread, "height %d (a new epoch) was read after a re-read: %v", hgt, order)
	}
	require.True(t, seenReread, "the pass also re-read: %v", order)
}
