package chain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
)

func TestStakingAndRegistryAt(t *testing.T) {
	ctx := context.Background()
	var pinned, unpinned int
	var paths []string
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if r.Header.Get("x-cosmos-block-height") == "" {
			unpinned++
		} else {
			require.Equal(t, "123", r.Header.Get("x-cosmos-block-height"))
			pinned++
		}
		switch r.URL.Path {
		case "/cosmos/staking/v1beta1/validators":
			serveFixture(t, "rest-staking-validators")(w, r)
		case "/saicon/chain/validatorregistry/v1/validators":
			serveFixture(t, "rest-registry-validators")(w, r)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	r := chain.NewREST(base)

	vals, err := r.ValidatorsAt(ctx, 123)
	require.NoError(t, err)
	require.Len(t, vals, 3)
	set := map[string]bool{}
	for _, m := range chainSet(t) {
		set[m] = true
	}
	for _, v := range vals {
		require.NotEmpty(t, v.Operator)
		require.NotEmpty(t, v.Tokens)
		require.NotEmpty(t, v.ConsensusPubKey)
		addr, err := chain.ConsAddrFromPubKey(v.ConsensusPubKey)
		require.NoError(t, err)
		require.True(t, set[addr], "%s is not in the captured set", addr)
	}

	reg, err := r.RegistryValidatorsAt(ctx, 123)
	require.NoError(t, err)
	require.Len(t, reg, 3)
	for _, e := range reg {
		require.NotEmpty(t, e.Operator)
		require.NotEmpty(t, e.Status)
	}
	require.Equal(t, 2, pinned)
	require.Zero(t, unpinned)
	require.Equal(t, []string{
		"/cosmos/staking/v1beta1/validators?pagination.limit=500",
		"/saicon/chain/validatorregistry/v1/validators?pagination.limit=500",
	}, paths)

	// The latest-state readers send no height header and need no echo.
	plain := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("x-cosmos-block-height"))
		serveFixture(t, "rest-staking-validators")(w, r)
	}))
	latest, err := chain.NewREST(plain).Validators(ctx)
	require.NoError(t, err)
	require.Len(t, latest, 3)
	require.Equal(t, vals, latest, "the pinned and the latest reader parse one body alike")
}

// chainSet is the consensus addresses of the captured CometBFT validator set.
func chainSet(t *testing.T) []string {
	t.Helper()
	var body struct {
		Result struct {
			Validators []struct {
				Address string `json:"address"`
			} `json:"validators"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(fixture(t, "rpc-validators"), &body))
	var out []string
	for _, v := range body.Result.Validators {
		out = append(out, v.Address)
	}
	require.Len(t, out, 3)
	return out
}

func TestEntityAtMembers(t *testing.T) {
	var path string
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		serveFixture(t, "rest-entity")(w, r)
	}))
	got, err := chain.NewREST(base).EntityAt(context.Background(), 50, "saicon-devnet-operator")
	require.NoError(t, err)
	require.Equal(t, "/saicon/chain/entityregistry/v1/entities/saicon-devnet-operator", path)
	require.Equal(t, []string{
		"saiconvaloper128d6d2qq4g5ej57vhuzseudffp4pxy4ln7red8",
		"saiconvaloper106p86dmlwqgg0u90lnkc8fvg06a2x2gk29wvyf",
	}, got.Members)
	require.Equal(t, uint32(2), got.ActiveSeats)
}

func TestEntityStakeNotFoundIsNil(t *testing.T) {
	ctx := context.Background()

	var path string
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		serveFixture(t, "rest-entity-stake-revoked")(w, r)
	}))
	st, err := chain.NewREST(base).EntityStakeAt(ctx, 50, "demo-third")
	require.NoError(t, err)
	require.Nil(t, st)
	require.Equal(t, "/saicon/chain/stakeengine/v1/entities/demo-third/stake", path)

	base = serve(t, serveFixture(t, "rest-entity-stake"))
	st, err = chain.NewREST(base).EntityStakeAt(ctx, 50, "saicon-devnet-operator")
	require.NoError(t, err)
	require.NotNil(t, st)
	require.Equal(t, chain.EntityStake{
		Tokens:         fixtureField(t, "rest-entity-stake", "tokens"),
		EntityCap:      fixtureField(t, "rest-entity-stake", "entity_cap"),
		Room:           fixtureField(t, "rest-entity-stake", "room"),
		ActiveSeats:    2,
		MaxActiveSeats: 2,
		Frozen:         false,
		Reason:         "",
	}, *st)
	require.NotEmpty(t, st.Tokens)
	require.NotEmpty(t, st.EntityCap)
	require.NotEmpty(t, st.Room)
}

func TestValidatorEntityFrozen(t *testing.T) {
	ctx := context.Background()
	const op = "saiconvaloper17jn6zrjaxkp6kg4mnsuw30n2g5ex2fyujwma9n"

	var path string
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		serveFixture(t, "rest-validator-entity-frozen")(w, r)
	}))
	got, err := chain.NewREST(base).ValidatorEntityAt(ctx, 50, op)
	require.NoError(t, err)
	require.Equal(t, "/saicon/chain/entityregistry/v1/validators/"+op+"/entity", path)
	require.False(t, got.Attested)
	require.False(t, got.Pending)
	require.Empty(t, got.EntityID)
	require.NotNil(t, got.Frozen)
	require.Equal(t, "demo-third", got.Frozen.FormerEntityID)
	require.NotEmpty(t, got.Frozen.Reason)
	require.Positive(t, got.Frozen.Height)

	for name, body := range map[string]string{
		"absent": `{"attested":true,"entity_id":"saicon-devnet-operator","pending":false}`,
		"null":   `{"attested":true,"entity_id":"saicon-devnet-operator","frozen":null,"pending":false}`,
	} {
		base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(heightHeader, r.Header.Get("x-cosmos-block-height"))
			_, _ = w.Write([]byte(body))
		}))
		got, err := chain.NewREST(base).ValidatorEntityAt(ctx, 50, op)
		require.NoError(t, err, name)
		require.Nil(t, got.Frozen, name)
		require.True(t, got.Attested, name)
		require.Equal(t, "saicon-devnet-operator", got.EntityID, name)
	}
}

func TestEntitiesFollowNextKey(t *testing.T) {
	var keys []string
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(heightHeader, r.Header.Get("x-cosmos-block-height"))
		require.Equal(t, "500", r.URL.Query().Get("pagination.limit"))
		key := r.URL.Query().Get("pagination.key")
		keys = append(keys, r.URL.RawQuery)
		switch key {
		case "":
			_, _ = w.Write([]byte(`{"entities":[{"id":"one","display_name":"One","status":"ENTITY_STATUS_ATTESTED","attested_height":"5"}],"pagination":{"next_key":"a+b/c=","total":"0"}}`))
		case "a+b/c=":
			_, _ = w.Write([]byte(`{"entities":[{"id":"two","display_name":"Two","status":"ENTITY_STATUS_REVOKED","attested_height":"6"}],"pagination":{"next_key":null,"total":"0"}}`))
		default:
			t.Errorf("unexpected key %q", key)
		}
	}))
	got, err := chain.NewREST(base).EntitiesAt(context.Background(), 50)
	require.NoError(t, err)
	require.Equal(t, []chain.EntityRecord{
		{ID: "one", DisplayName: "One", Status: "ENTITY_STATUS_ATTESTED", AttestedHeight: 5},
		{ID: "two", DisplayName: "Two", Status: "ENTITY_STATUS_REVOKED", AttestedHeight: 6},
	}, got)
	require.Len(t, keys, 2)
	require.Equal(t, "pagination.limit=500&pagination.key="+url.QueryEscape("a+b/c="), keys[1])
}

func TestEntitiesRefuseARepeatedNextKey(t *testing.T) {
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(heightHeader, r.Header.Get("x-cosmos-block-height"))
		_, _ = w.Write([]byte(`{"entities":[],"pagination":{"next_key":"same"}}`))
	}))
	got, err := chain.NewREST(base).EntitiesAt(context.Background(), 50)
	require.Error(t, err)
	require.Nil(t, got)
}

func TestEntityCapAndSeatLimit(t *testing.T) {
	ctx := context.Background()
	var paths []string
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/saicon/chain/stakeengine/v1/caps":
			serveFixture(t, "rest-caps")(w, r)
		case "/saicon/chain/entityregistry/v1/params":
			serveFixture(t, "rest-entity-params")(w, r)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	r := chain.NewREST(base)

	cap, err := r.EntityCapAt(ctx, 50)
	require.NoError(t, err)
	require.Equal(t, fixtureField(t, "rest-caps", "cap_state.entity_cap"), cap)
	require.NotEmpty(t, cap)

	seats, err := r.MaxActiveSeatsAt(ctx, 50)
	require.NoError(t, err)
	require.Equal(t, uint32(2), seats)
	require.Len(t, paths, 2)

	t.Run("a body without the figure is an error, never zero", func(t *testing.T) {
		empty := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(heightHeader, r.Header.Get("x-cosmos-block-height"))
			_, _ = w.Write([]byte(`{}`))
		}))
		_, err := chain.NewREST(empty).EntityCapAt(ctx, 50)
		require.Error(t, err)
		_, err = chain.NewREST(empty).MaxActiveSeatsAt(ctx, 50)
		require.Error(t, err)
	})
}

// A pinned list read that the chain answers in pages must read every page: a
// silent stop at the first one would drop validators from the join and from the
// frozen-seat scan.
func TestPinnedValidatorListsFollowNextKey(t *testing.T) {
	ctx := context.Background()
	stakingPage := func(op, next string) string {
		n := "null"
		if next != "" {
			n = `"` + next + `"`
		}
		return `{"validators":[{"operator_address":"` + op + `","tokens":"1","consensus_pubkey":{"key":"AA=="}}],"pagination":{"next_key":` + n + `}}`
	}
	registryPage := func(op, next string) string {
		n := "null"
		if next != "" {
			n = `"` + next + `"`
		}
		return `{"validators":[{"validator_address":"` + op + `","status":"STATUS_ACTIVE","bonding_start_height":"1","lockup_duration_seconds":"0"}],"pagination":{"next_key":` + n + `}}`
	}
	var queries []string
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "88", r.Header.Get("x-cosmos-block-height"))
		w.Header().Set(heightHeader, "88")
		require.Equal(t, "500", r.URL.Query().Get("pagination.limit"))
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		key := r.URL.Query().Get("pagination.key")
		switch r.URL.Path {
		case "/cosmos/staking/v1beta1/validators":
			switch key {
			case "":
				_, _ = w.Write([]byte(stakingPage("opA", "k+1/=")))
			case "k+1/=":
				_, _ = w.Write([]byte(stakingPage("opB", "")))
			default:
				t.Errorf("unexpected staking key %q", key)
			}
		case "/saicon/chain/validatorregistry/v1/validators":
			switch key {
			case "":
				_, _ = w.Write([]byte(registryPage("regA", "r2")))
			case "r2":
				_, _ = w.Write([]byte(registryPage("regB", "")))
			default:
				t.Errorf("unexpected registry key %q", key)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	r := chain.NewREST(base)

	vals, err := r.ValidatorsAt(ctx, 88)
	require.NoError(t, err)
	require.Len(t, vals, 2)
	require.Equal(t, "opA", vals[0].Operator)
	require.Equal(t, "opB", vals[1].Operator)

	reg, err := r.RegistryValidatorsAt(ctx, 88)
	require.NoError(t, err)
	require.Len(t, reg, 2)
	require.Equal(t, "regA", reg[0].Operator)
	require.Equal(t, "regB", reg[1].Operator)

	require.Equal(t, []string{
		"/cosmos/staking/v1beta1/validators?pagination.limit=500",
		"/cosmos/staking/v1beta1/validators?pagination.limit=500&pagination.key=" + url.QueryEscape("k+1/="),
		"/saicon/chain/validatorregistry/v1/validators?pagination.limit=500",
		"/saicon/chain/validatorregistry/v1/validators?pagination.limit=500&pagination.key=r2",
	}, queries)
}

func TestPinnedValidatorListsRefuseARepeatedNextKey(t *testing.T) {
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(heightHeader, r.Header.Get("x-cosmos-block-height"))
		_, _ = w.Write([]byte(`{"validators":[],"pagination":{"next_key":"same"}}`))
	}))
	r := chain.NewREST(base)
	vals, err := r.ValidatorsAt(context.Background(), 5)
	require.Error(t, err)
	require.Nil(t, vals)
	reg, err := r.RegistryValidatorsAt(context.Background(), 5)
	require.Error(t, err)
	require.Nil(t, reg)
}

// Every HTTP request a client makes is counted, pages included: the
// concentration pass budgets its work by this number.
func TestClientsCountTheirRequests(t *testing.T) {
	ctx := context.Background()
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(heightHeader, r.Header.Get("x-cosmos-block-height"))
		switch r.URL.Query().Get("pagination.key") {
		case "":
			_, _ = w.Write([]byte(`{"entities":[],"pagination":{"next_key":"p2"}}`))
		default:
			_, _ = w.Write([]byte(`{"entities":[],"pagination":{"next_key":null}}`))
		}
	}))
	r := chain.NewREST(base)
	require.Zero(t, r.Calls())
	_, err := r.EntitiesAt(ctx, 9)
	require.NoError(t, err)
	require.Equal(t, uint64(2), r.Calls(), "two pages are two requests")
	_, _ = r.EntityCapAt(ctx, 9) // a failed answer is still a request
	require.Equal(t, uint64(3), r.Calls())

	rpcBase := serve(t, serveFixture(t, "rpc-commit"))
	rpc := chain.NewRPC(rpcBase)
	_, _ = rpc.BlockHashAt(ctx, 1)
	require.Equal(t, uint64(1), rpc.Calls())
}
