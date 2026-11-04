package concentration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/concentration"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

// liveNode is the chain as the concentration reader sees it: block data from RPC,
// module state from REST.
type liveNode struct {
	*chain.RPC
	*chain.REST
}

// liveEnv returns the two endpoints of a running chain, or skips the test.
func liveEnv(t *testing.T) (rpcURL, restURL string) {
	t.Helper()
	rpcURL, restURL = os.Getenv("INDEXER_TEST_RPC"), os.Getenv("INDEXER_TEST_REST")
	if rpcURL == "" || restURL == "" {
		t.Skip("set INDEXER_TEST_RPC and INDEXER_TEST_REST to a running chain (bash indexer/scripts/localnet.sh start)")
	}
	return rpcURL, restURL
}

// rawGet asks the node directly, without going through the indexer's own readers, so that
// the figures it stored can be compared with what the node says. height > 0 pins the read
// and requires the node to echo it. A gRPC NotFound answers (nil, 404, nil).
func rawGet(t *testing.T, base, path string, height int64) (map[string]any, int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	require.NoError(t, err)
	if height > 0 {
		req.Header.Set("x-cosmos-block-height", strconv.FormatInt(height, 10))
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if resp.StatusCode == http.StatusNotFound {
		return nil, resp.StatusCode
	}
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %s", path, b)
	if height > 0 {
		require.Equal(t, strconv.FormatInt(height, 10), resp.Header.Get("X-Cosmos-Block-Height"), "%s echoes the pinned height", path)
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out), "%s: %s", path, b)
	return out, resp.StatusCode
}

// jstr reads a string member of a decoded JSON object; absent and null read as "".
func jstr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// jnum reads a JSON number member as an int64, whether the gateway wrote it as a number
// or as a string; absent reads as 0.
func jnum(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	switch v := m[key].(type) {
	case nil:
		return 0
	case float64:
		return int64(v)
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		require.NoError(t, err, "%s=%q", key, v)
		return n
	}
	require.Failf(t, "unexpected JSON type", "%s=%v", key, m[key])
	return 0
}

// deref returns what p points to, or the zero value of T for nil.
func deref[T any](p *T) T {
	var z T
	if p == nil {
		return z
	}
	return *p
}

// nodeSetAt reads the consensus set at height straight from CometBFT's /validators.
func nodeSetAt(t *testing.T, rpcURL string, height int64) (power map[string]int64, count int, sum int64) {
	t.Helper()
	power = map[string]int64{}
	for page := 1; ; page++ {
		body, _ := rawGet(t, rpcURL, fmt.Sprintf("/validators?height=%d&page=%d&per_page=100", height, page), 0)
		res, ok := body["result"].(map[string]any)
		require.True(t, ok, "validators answer has a result")
		require.Equal(t, strconv.FormatInt(height, 10), jstr(res, "block_height"))
		vs, _ := res["validators"].([]any)
		for _, v := range vs {
			vm := v.(map[string]any)
			p := jnum(t, vm, "voting_power")
			power[jstr(vm, "address")] = p
			sum += p
			count++
		}
		if count >= int(jnum(t, res, "total")) || len(vs) == 0 {
			return power, count, sum
		}
	}
}

// TestConcentrationAgreesWithChain: every figure the indexer stores for a closed epoch
// equals what the node says at that epoch's last height — the consensus set, the
// entity aggregates, and the unit each validator is counted under. INDEXER_TEST_EPOCH
// names another closed epoch to check instead of the newest. INDEXER_TEST_EXPECT_CHARGED=1
// additionally requires that the epoch holds a revoked entity with a charged frozen seat
// (the localnet demo's demo-third), which without it is checked only when present.
//
//	INDEXER_TEST_RPC=http://127.0.0.1:26657 INDEXER_TEST_REST=http://127.0.0.1:1317 \
//	  go test ./internal/concentration/ -run TestConcentrationAgreesWithChain -v
func TestConcentrationAgreesWithChain(t *testing.T) {
	rpcURL, restURL := liveEnv(t)
	ctx := context.Background()
	rpc, rest := chain.NewRPC(rpcURL), chain.NewREST(restURL)

	epochLen, err := rest.EpochLength(ctx)
	require.NoError(t, err)
	status, err := rpc.Status(ctx)
	require.NoError(t, err)
	// The newest closed epoch at least 2 blocks behind the tip.
	ep := uint64(status.LatestHeight-2) / epochLen
	if v := os.Getenv("INDEXER_TEST_EPOCH"); v != "" { // an older epoch the node still holds
		ep, err = strconv.ParseUint(v, 10, 64)
		require.NoError(t, err, "INDEXER_TEST_EPOCH")
	}
	require.Greater(t, ep, uint64(0), "the chain needs a closed epoch before it can be read")
	require.LessOrEqual(t, int64(ep*epochLen), status.LatestHeight-2, "epoch %d is not closed 2 blocks ago", ep)
	h := int64(ep * epochLen)

	from, present, err := rest.EntityLayerFrom(ctx)
	require.NoError(t, err)
	require.True(t, present, "the localnet runs the entity layer")

	s, err := store.Open(filepath.Join(t.TempDir(), "agree.db"))
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	require.NoError(t, s.SetChain(ctx, "live", time.Now()))
	require.NoError(t, concentration.NewReader(s, liveNode{rpc, rest}, epochLen, from, present).ReadEpoch(ctx, "live", ep))

	row, powers, err := s.Concentration(ctx, "live", &ep)
	require.NoError(t, err)
	require.NotNil(t, row, "epoch %d (height %d) has a power row", ep, h)
	require.Nil(t, row.PowerUnavailable)
	require.Nil(t, row.EntitiesUnavailable, "the entity part of epoch %d was read", ep)
	require.Equal(t, h, row.Height)

	// 1. The consensus set: count, sum, and each member's power.
	nodePower, count, sum := nodeSetAt(t, rpcURL, h)
	require.NotZero(t, count, "the node's consensus set at %d is not empty", h)
	require.Equal(t, int64(count), deref(row.SetSize), "set_size")
	require.Equal(t, sum, deref(row.TotalPower), "total_power")
	require.Len(t, powers, count)
	for _, p := range powers {
		want, ok := nodePower[p.ConsAddr]
		require.True(t, ok, "stored member %s is in the node's set", p.ConsAddr)
		require.Equal(t, want, p.Power, "power of %s", p.ConsAddr)
		require.NotNil(t, p.Operator, "member %s resolves to an operator", p.ConsAddr)
		require.NotNil(t, p.Unit, "member %s has a unit", p.ConsAddr)
	}

	// The commit hash of that height, from the node.
	commit, _ := rawGet(t, rpcURL, fmt.Sprintf("/commit?height=%d", h), 0)
	res, _ := commit["result"].(map[string]any)
	sh, _ := res["signed_header"].(map[string]any)
	cm, _ := sh["commit"].(map[string]any)
	bid, _ := cm["block_id"].(map[string]any)
	require.NotNil(t, row.BlockHash)
	require.Equal(t, jstr(bid, "hash"), *row.BlockHash, "block_hash")

	// 2. Top-10 and Nakamoto by validator, recomputed here from the node's numbers.
	ps := make([]int64, 0, len(nodePower))
	for _, p := range nodePower {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i] > ps[j] })
	var top10 int64
	for i := 0; i < len(ps) && i < 10; i++ {
		top10 += ps[i]
	}
	require.Equal(t, top10, deref(row.Top10Power), "top10_power")
	require.Equal(t, int64(nakamotoOf(ps, sum)), deref(row.NakamotoValidators), "nakamoto_validators")

	// 3. The entity registry and the stake engine at H, entity by entity.
	list, _ := rawGet(t, restURL, "/saicon/chain/entityregistry/v1/entities?pagination.limit=500", h)
	nodeEntities, _ := list["entities"].([]any)
	require.NotEmpty(t, nodeEntities)
	_, rows, err := s.Entities(ctx, "live", &ep)
	require.NoError(t, err)
	stored := map[string]store.EntityRow{}
	for _, e := range rows {
		stored[e.EntityID] = e
	}
	memberOf := map[string]string{}    // operator -> attested entity id, from the node
	revokedSeats := map[string]int64{} // revoked entity id -> the chain's own seat count
	attested := 0
	for _, raw := range nodeEntities {
		e := raw.(map[string]any)
		id := jstr(e, "id")
		detail, _ := rawGet(t, restURL, "/saicon/chain/entityregistry/v1/entities/"+url.PathEscape(id), h)
		if jstr(e, "status") != "ENTITY_STATUS_ATTESTED" {
			// A revoked entity has no members: every seat it still counts is a charged frozen seat.
			require.Empty(t, detail["members"], "revoked entity %s has no members", id)
			revokedSeats[id] = jnum(t, detail, "active_seats")
			continue
		}
		attested++
		got, ok := stored[id]
		require.True(t, ok, "attested entity %s is stored", id)
		require.Equal(t, jstr(e, "status"), got.Status)
		require.Equal(t, jstr(e, "display_name"), got.DisplayName)
		require.Equal(t, jnum(t, e, "attested_height"), deref(got.AttestedHeight), "%s attested_height", id)
		require.Equal(t, jnum(t, detail, "active_seats"), deref(got.ActiveSeats), "%s active_seats (entity query)", id)
		members, _ := detail["members"].([]any)
		for _, m := range members {
			memberOf[jstr(m.(map[string]any), "validator")] = id
		}

		st, code := rawGet(t, restURL, "/saicon/chain/stakeengine/v1/entities/"+url.PathEscape(id)+"/stake", h)
		require.Equal(t, http.StatusOK, code, "an attested entity has a stake record")
		require.Equal(t, jstr(st, "tokens"), deref(got.Tokens), "%s tokens", id)
		require.Equal(t, jstr(st, "entity_cap"), deref(got.EntityCap), "%s entity_cap", id)
		require.Equal(t, jstr(st, "room"), deref(got.Room), "%s room", id)
		require.Equal(t, jnum(t, st, "active_seats"), deref(got.ActiveSeats), "%s active_seats (stake query)", id)
		require.Equal(t, jnum(t, st, "max_active_seats"), deref(got.MaxActiveSeats), "%s max_active_seats", id)
		require.Equal(t, st["frozen"] == true, deref(got.Frozen), "%s frozen", id)
		require.Equal(t, jstr(st, "reason"), deref(got.FrozenReason), "%s frozen reason", id)
		require.Len(t, got.Seats, len(members), "%s: one stored seat per member", id)
	}
	require.NotZero(t, attested)

	caps, _ := rawGet(t, restURL, "/saicon/chain/stakeengine/v1/caps", h)
	capState, _ := caps["cap_state"].(map[string]any)
	require.Equal(t, jstr(capState, "entity_cap"), deref(row.EntityCap), "entity_cap")
	params, _ := rawGet(t, restURL, "/saicon/chain/entityregistry/v1/params", h)
	pm, _ := params["params"].(map[string]any)
	require.Equal(t, jnum(t, pm, "max_active_seats"), deref(row.MaxActiveSeats), "max_active_seats")

	// 4. The unit of every validator: its entity when a member, else the entity its frozen
	// seat is charged to, else itself.
	unitOfOp := map[string]string{}
	for _, p := range powers {
		unitOfOp[*p.Operator] = *p.Unit
	}
	vals, _ := rawGet(t, restURL, "/cosmos/staking/v1beta1/validators?pagination.limit=500", h)
	nodeVals, _ := vals["validators"].([]any)
	require.NotEmpty(t, nodeVals)
	regList, _ := rawGet(t, restURL, "/saicon/chain/validatorregistry/v1/validators?pagination.limit=500", h)
	regStatus := map[string]string{} // address key -> registry status, from the node
	nodeReg, _ := regList["validators"].([]any)
	require.NotEmpty(t, nodeReg)
	for _, raw := range nodeReg {
		rm := raw.(map[string]any)
		k, err := chain.AddrKey(jstr(rm, "validator_address"))
		require.NoError(t, err)
		regStatus[k] = jstr(rm, "status")
	}
	unitPower := map[string]int64{}
	for _, raw := range nodeVals {
		op := jstr(raw.(map[string]any), "operator_address")
		ve, _ := rawGet(t, restURL, "/saicon/chain/entityregistry/v1/validators/"+url.PathEscape(op)+"/entity", h)
		want := "val:" + op
		switch frozen, _ := ve["frozen"].(map[string]any); {
		case ve["attested"] == true:
			want = "entity:" + jstr(ve, "entity_id")
			require.Equal(t, memberOf[op], jstr(ve, "entity_id"), "%s: entity query and entity members agree", op)
		case frozen != nil:
			// A frozen seat is charged to its former entity only while the validator is
			// registry-ACTIVE (the chain's charging rule); otherwise it counts as itself.
			k, err := chain.AddrKey(op)
			require.NoError(t, err)
			if regStatus[k] == "STATUS_ACTIVE" {
				want = "entity:" + jstr(frozen, "former_entity_id")
			}
		}
		got, inSet := unitOfOp[op]
		if !inSet {
			continue // bonded in x/staking but not in the consensus set at H
		}
		require.Equal(t, want, got, "unit of %s", op)
	}
	for _, p := range powers {
		unitPower[*p.Unit] += p.Power
	}
	units := make([]int64, 0, len(unitPower))
	for _, p := range unitPower {
		units = append(units, p)
	}
	sort.Slice(units, func(i, j int) bool { return units[i] > units[j] })
	require.Equal(t, int64(len(units)), deref(row.UnitCount), "unit_count")
	require.Equal(t, int64(nakamotoOf(units, sum)), deref(row.NakamotoEntities), "nakamoto_entities")
	require.LessOrEqual(t, deref(row.NakamotoEntities), deref(row.NakamotoValidators))

	// 5. A revoked entity is in the picture exactly while the chain still counts a seat against
	// it, and then with its charged frozen seats and no chain stake figure (the demo's
	// demo-third until its validator leaves the active set).
	sawCharged := false
	for id, seats := range revokedSeats {
		third, ok := stored[id]
		if seats == 0 {
			require.False(t, ok, "revoked entity %s has no seat left and must not be listed", id)
			continue
		}
		require.True(t, ok, "revoked entity %s still counts %d seat(s) and must be listed", id, seats)
		sawCharged = true
		require.Equal(t, "ENTITY_STATUS_REVOKED", third.Status)
		require.Nil(t, third.Tokens, "a revoked entity has no chain stake figure")
		require.Nil(t, third.EntityCap)
		charged := int64(0)
		for _, seat := range third.Seats {
			require.Equal(t, "charged_frozen", seat.Role, "a revoked entity lists no members")
			charged++
			require.NotNil(t, seat.FrozenHeight)
			require.Equal(t, "entity:"+id, unitOfOp[seat.Operator], "the charged seat's validator is counted under the revoked entity")
		}
		require.Equal(t, seats, charged, "%s: stored charged seats equal the chain's seat count", id)
	}
	t.Logf("epoch %d, height %d: set %d, %d entities stored, charged frozen seat of a revoked entity present: %v",
		ep, h, count, len(rows), sawCharged)
	if os.Getenv("INDEXER_TEST_EXPECT_CHARGED") == "1" {
		require.True(t, sawCharged, "INDEXER_TEST_EXPECT_CHARGED=1: epoch %d has no revoked entity with a charged frozen seat "+
			"(on the localnet only epochs closed before its second and third validators are jailed, below about height 110)", ep)
	}
}

// nakamotoOf is the smallest count of the largest holders whose power reaches a third of
// total (3·cum ≥ total), by the spec's rule, on powers sorted descending.
func nakamotoOf(sorted []int64, total int64) int {
	var cum int64
	for i, p := range sorted {
		cum += p
		if 3*cum >= total {
			return i + 1
		}
	}
	return len(sorted)
}

// TestPrunedHeightIsUnavailable: an epoch the node has pruned the app state of is stored
// as unavailable with its reason, never as numbers.
func TestPrunedHeightIsUnavailable(t *testing.T) {
	rpcURL, restURL := liveEnv(t)
	ctx := context.Background()
	rpc, rest := chain.NewRPC(rpcURL), chain.NewREST(restURL)

	epochLen, err := rest.EpochLength(ctx)
	require.NoError(t, err)
	status, err := rpc.Status(ctx)
	require.NoError(t, err)
	h := int64(2 * epochLen)
	if status.LatestHeight <= h+100 {
		t.Skipf("tip %d is not far enough past height %d for the node (which keeps 50 heights) to have pruned it", status.LatestHeight, h)
	}

	// A node that still holds the app state at H (an archive node, or default pruning) has
	// nothing to be unavailable about.
	req, err := http.NewRequest(http.MethodGet, restURL+"/cosmos/staking/v1beta1/validators", nil)
	require.NoError(t, err)
	req.Header.Set("x-cosmos-block-height", strconv.FormatInt(h, 10))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	if resp.StatusCode == http.StatusOK {
		t.Skipf("the node still holds the app state at height %d: it does not prune that far back", h)
	}
	require.Contains(t, string(body), "failed to load state at height",
		"the node refused height %d for another reason than having pruned it (HTTP %d)", h, resp.StatusCode)

	from, present, err := rest.EntityLayerFrom(ctx)
	require.NoError(t, err)

	s, err := store.Open(filepath.Join(t.TempDir(), "pruned.db"))
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	require.NoError(t, s.SetChain(ctx, "live", time.Now()))
	require.NoError(t, concentration.NewReader(s, liveNode{rpc, rest}, epochLen, from, present).ReadEpoch(ctx, "live", 2))

	rows, err := s.ConcentrationSeries(ctx, "live", 2, 2)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, h, rows[0].Height)
	require.NotNil(t, rows[0].EntitiesUnavailable)
	require.Equal(t, concentration.Pruned, *rows[0].EntitiesUnavailable)
	require.Nil(t, rows[0].UnitCount, "no entity figure for a pruned height")
	require.Nil(t, rows[0].NakamotoEntities, "no entity figure for a pruned height")
	_, ents, err := s.Entities(ctx, "live", nil)
	require.NoError(t, err)
	require.Empty(t, ents, "nothing is served as the entity view of a pruned epoch")
}
