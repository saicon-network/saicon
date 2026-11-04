package keeper_test

import (
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/entityregistry/keeper"
	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestQueryNilRequests(t *testing.T) {
	f := newFixture(t)
	q := keeper.NewQueryServerImpl(f.k)
	for name, call := range map[string]func() error{
		"params":   func() error { _, err := q.Params(f.ctx, nil); return err },
		"entities": func() error { _, err := q.Entities(f.ctx, nil); return err },
		"entity":   func() error { _, err := q.Entity(f.ctx, nil); return err },
		"valent":   func() error { _, err := q.ValidatorEntity(f.ctx, nil); return err },
		"tickets":  func() error { _, err := q.Tickets(f.ctx, nil); return err },
		"vrfkey":   func() error { _, err := q.VrfKey(f.ctx, nil); return err },
	} {
		require.Equal(t, codes.InvalidArgument, status.Code(call()), name)
	}
}

func TestQueryParams(t *testing.T) {
	f := newFixture(t)
	res, err := keeper.NewQueryServerImpl(f.k).Params(f.ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, uint32(2), res.Params.MaxActiveSeats)
}

func TestQueryEntity(t *testing.T) {
	f := newFixture(t)
	q := keeper.NewQueryServerImpl(f.k)

	_, err := q.Entity(f.ctx, &types.QueryEntityRequest{EntityId: "ghost"})
	require.Equal(t, codes.NotFound, status.Code(err))

	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2)))
	require.NoError(t, err)

	res, err := q.Entity(f.ctx, &types.QueryEntityRequest{EntityId: "acme"})
	require.NoError(t, err)
	require.Equal(t, "acme", res.Entity.Id)
	require.Len(t, res.Members, 2)
	require.Equal(t, uint32(1), res.ActiveSeats)
}

func TestQueryValidatorEntity(t *testing.T) {
	f := newFixture(t)
	q := keeper.NewQueryServerImpl(f.k)

	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_BONDING
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("beta", val(2)))
	require.NoError(t, err)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "gone")
	require.NoError(t, err) // val(1) is ACTIVE: its seat is frozen
	require.Error(t, f.k.MayActivate(f.ctx, val(3)))

	res, err := q.ValidatorEntity(f.ctx, &types.QueryValidatorEntityRequest{Validator: val(2).String()})
	require.NoError(t, err)
	require.True(t, res.Attested)
	require.Equal(t, "beta", res.EntityId)
	require.Nil(t, res.Frozen)
	require.False(t, res.Pending)

	res, err = q.ValidatorEntity(f.ctx, &types.QueryValidatorEntityRequest{Validator: val(1).String()})
	require.NoError(t, err)
	require.False(t, res.Attested)
	require.NotNil(t, res.Frozen)
	require.Equal(t, "acme", res.Frozen.FormerEntityId)

	res, err = q.ValidatorEntity(f.ctx, &types.QueryValidatorEntityRequest{Validator: val(3).String()})
	require.NoError(t, err)
	require.True(t, res.Pending)
	require.False(t, res.Attested)

	res, err = q.ValidatorEntity(f.ctx, &types.QueryValidatorEntityRequest{Validator: val(9).String()})
	require.NoError(t, err)
	require.Equal(t, &types.QueryValidatorEntityResponse{}, res)

	_, err = q.ValidatorEntity(f.ctx, &types.QueryValidatorEntityRequest{Validator: "nope"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestQueryTickets(t *testing.T) {
	f := newFixture(t)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	for _, e := range []struct {
		id string
		v  byte
	}{{"beta", 3}, {"acme", 1}} {
		_, err := f.k.Attest(f.ctx, f.attestor, att(e.id, val(e.v)))
		require.NoError(t, err)
	}
	res, err := keeper.NewQueryServerImpl(f.k).Tickets(f.ctx, &types.QueryTicketsRequest{})
	require.NoError(t, err)
	require.Equal(t, []types.Ticket{
		{EntityId: "acme", Members: []string{val(1).String()}, ActiveSeats: 1, Tickets: 1},
		{EntityId: "beta", Members: []string{val(3).String()}, ActiveSeats: 0, Tickets: 1},
	}, res.Tickets)
}

func TestQueryEntitiesPaginates(t *testing.T) {
	f := newFixture(t)
	q := keeper.NewQueryServerImpl(f.k)
	for i, id := range []string{"acme", "beta", "gamma"} {
		_, err := f.k.Attest(f.ctx, f.attestor, att(id, val(byte(i+1))))
		require.NoError(t, err)
	}
	res, err := q.Entities(f.ctx, &types.QueryEntitiesRequest{Pagination: &query.PageRequest{Limit: 1}})
	require.NoError(t, err)
	require.Len(t, res.Entities, 1)
	require.Equal(t, "acme", res.Entities[0].Id)
	require.NotEmpty(t, res.Pagination.NextKey)

	res, err = q.Entities(f.ctx, &types.QueryEntitiesRequest{
		Pagination: &query.PageRequest{Key: res.Pagination.NextKey, Limit: 5},
	})
	require.NoError(t, err)
	require.Len(t, res.Entities, 2)
	require.Empty(t, res.Pagination.NextKey)
}

func TestQueryVrfKey(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	q := keeper.NewQueryServerImpl(f.k)
	k1, k2 := vrfKey(t, 1), vrfKey(t, 2)
	f.offer(t, k1, val(1))
	_, err := f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1)}, bind(k1, val(1))))
	require.NoError(t, err)
	f.ctx = f.ctx.WithBlockHeight(20)
	f.offer(t, k2, val(1))

	res, err := q.VrfKey(f.ctx, &types.QueryVrfKeyRequest{Validator: val(1).String()})
	require.NoError(t, err)
	require.NotNil(t, res.Offer)
	require.Equal(t, hex.EncodeToString(k2.PublicKey()), res.Offer.VrfPublicKey)
	require.Equal(t, int64(20), res.Offer.Height)
	require.Equal(t, "acme", res.EntityId)
	require.Equal(t, hex.EncodeToString(k1.PublicKey()), res.VrfPublicKey)
	require.Equal(t, int64(10), res.VrfKeyHeight)

	res, err = q.VrfKey(f.ctx, &types.QueryVrfKeyRequest{Validator: val(9).String()})
	require.NoError(t, err)
	require.Nil(t, res.Offer)
	require.Equal(t, &types.QueryVrfKeyResponse{}, res)

	_, err = q.VrfKey(f.ctx, &types.QueryVrfKeyRequest{Validator: "nope"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
