package keeper_test

import (
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// No Validate() here: the unit fixture's global bech32 prefix is cosmos, which
// GenesisState.Validate (explicit saiconvaloper) rightly rejects. Validate is
// covered in types/genesis_test.go.
func TestGenesisRoundTrips(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	// val(2) holds a bound key, val(7) only an offer; offered in reverse key order.
	k2, k7 := vrfKey(t, 2), vrfKey(t, 7)
	f.offer(t, k7, val(7))
	f.offer(t, k2, val(2))
	// Attest out of id order, so the export order is not the insertion order.
	for _, a := range []types.Attestation{
		att("gamma", val(3)),
		attWithKeys("acme", []sdk.ValAddress{val(2), val(1)}, bind(k2, val(2))),
		att("beta", val(4)),
	} {
		_, err := f.k.Attest(f.ctx, f.attestor, a)
		require.NoError(t, err)
	}
	f.reg.status[val(4).String()] = vrtypes.Status_STATUS_ACTIVE
	_, err := f.k.Revoke(f.ctx, f.attestor, "beta", "lapsed")
	require.NoError(t, err)
	require.NoError(t, f.k.Pending.Set(f.ctx, val(6)))
	require.NoError(t, f.k.Pending.Set(f.ctx, val(5)))

	first, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, first.Entities, 3)
	require.Equal(t, []string{"acme", "beta", "gamma"},
		[]string{first.Entities[0].Id, first.Entities[1].Id, first.Entities[2].Id})
	require.Equal(t, types.EntityStatus_ENTITY_STATUS_REVOKED, first.Entities[1].Status)
	require.Len(t, first.Members, 3)
	require.Equal(t, val(1).String(), first.Members[0].Validator, "members in key order")
	require.Len(t, first.Frozen, 1)
	require.Equal(t, val(4).String(), first.Frozen[0].Validator)
	require.Equal(t, []string{val(5).String(), val(6).String()}, first.Pending)
	require.Equal(t, hex.EncodeToString(k2.PublicKey()), first.Members[1].VrfPublicKey)
	require.Equal(t, int64(10), first.Members[1].VrfKeyHeight)
	require.Len(t, first.VrfKeyOffers, 2)
	require.Equal(t, val(2).String(), first.VrfKeyOffers[0].Validator, "offers in key order")
	require.Equal(t, val(7).String(), first.VrfKeyOffers[1].Validator)
	require.Len(t, first.VrfKeyOwners, 2)
	wantOwners := []types.VrfKeyOwner{
		{VrfPublicKey: hex.EncodeToString(k2.PublicKey()), Validator: val(2).String()},
		{VrfPublicKey: hex.EncodeToString(k7.PublicKey()), Validator: val(7).String()},
	}
	if string(k2.PublicKey()) > string(k7.PublicKey()) {
		wantOwners[0], wantOwners[1] = wantOwners[1], wantOwners[0]
	}
	require.Equal(t, wantOwners, first.VrfKeyOwners, "owners in key-byte order")

	g := newFixture(t)
	require.NoError(t, g.k.InitGenesis(g.ctx, *first))
	second, err := g.k.ExportGenesis(g.ctx)
	require.NoError(t, err)
	require.Equal(t, first, second)

	// The reverse index is rebuilt from the members.
	vals, err := g.k.MemberValidators(g.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, 2, len(vals))
	msg, broken := g.k.RegistryInvariant(g.ctx)
	require.False(t, broken, msg)
}

func TestInitGenesisRejectsABadAddress(t *testing.T) {
	f := newFixture(t)
	gs := types.DefaultGenesis()
	gs.Pending = []string{"not-an-address"}
	require.Error(t, f.k.InitGenesis(f.ctx, *gs))
}

func TestInitGenesisRestoresTheKeyOwners(t *testing.T) {
	f := newFixture(t)
	k := vrfKey(t, 3)
	pk := hex.EncodeToString(k.PublicKey())
	gs := types.DefaultGenesis()
	gs.VrfKeyOffers = []types.VrfKeyOffer{{Validator: val(1).String(), VrfPublicKey: pk, Height: 5}}
	gs.VrfKeyOwners = []types.VrfKeyOwner{{VrfPublicKey: pk, Validator: val(1).String()}}
	require.NoError(t, f.k.InitGenesis(f.ctx, *gs))
	owner, err := f.k.VrfKeyOwners.Get(f.ctx, k.PublicKey())
	require.NoError(t, err)
	require.Equal(t, []byte(val(1)), owner)
	offer, err := f.k.VrfKeyOffers.Get(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, int64(5), offer.Height)

	bad := types.DefaultGenesis()
	bad.VrfKeyOwners = []types.VrfKeyOwner{{VrfPublicKey: "zz", Validator: val(1).String()}}
	g := newFixture(t)
	require.Error(t, g.k.InitGenesis(g.ctx, *bad))
}

func TestGenesisRoundTripsDeparturesAndJoinedHeights(t *testing.T) {
	f := newFixture(t)
	// Revoke in reverse key order, so the export order is not the insertion order.
	f.at(10)
	for _, a := range []types.Attestation{att("acme", val(3), val(1)), att("beta", val(2))} {
		_, err := f.k.Attest(f.ctx, f.attestor, a)
		require.NoError(t, err)
	}
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_ACTIVE
	f.at(20)
	_, err := f.k.Revoke(f.ctx, f.attestor, "beta", "lapsed")
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	f.at(30)
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_UNBONDING
	require.NoError(t, f.k.EndBlock(f.ctx))

	first, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, first.Members, 1)
	require.Equal(t, int64(10), first.Members[0].JoinedHeight)
	require.Equal(t, []types.Departure{
		{Validator: val(2).String(), EntityId: "beta", JoinedHeight: 10, LeftHeight: 20},
		{Validator: val(3).String(), EntityId: "acme", JoinedHeight: 10, LeftHeight: 20, Frozen: true, ChargedUntil: 30},
	}, first.Departures, "departures in key order")

	g := newFixture(t)
	require.NoError(t, g.k.InitGenesis(g.ctx, *first))
	second, err := g.k.ExportGenesis(g.ctx)
	require.NoError(t, err)
	require.Equal(t, first, second)
	got, ok, err := g.k.EntityAt(g.ctx, val(3), 25)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "acme", got)

	bad := types.DefaultGenesis()
	bad.Departures = []types.Departure{{Validator: "not-an-address", EntityId: "acme"}}
	h := newFixture(t)
	require.Error(t, h.k.InitGenesis(h.ctx, *bad))
}
