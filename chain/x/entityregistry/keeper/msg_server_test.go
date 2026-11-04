package keeper_test

import (
	"encoding/hex"
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/keeper"
	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func attestMsg(f *fixture, id string, vals ...sdk.ValAddress) *types.MsgAttestEntity {
	strs := make([]string, len(vals))
	for i, v := range vals {
		strs[i] = v.String()
	}
	return &types.MsgAttestEntity{
		Attestor:        f.attestor.String(),
		EntityId:        id,
		DisplayName:     "Entity " + id,
		AttestationHash: strings.Repeat("ab", 32),
		Validators:      strs,
	}
}

func govAddress() string {
	return sdk.AccAddress(authtypes.NewModuleAddress(types.GovModuleName)).String()
}

func TestMsgAttestEntityReplaced(t *testing.T) {
	f := newFixture(t)
	srv := keeper.NewMsgServerImpl(f.k)

	res, err := srv.AttestEntity(f.ctx, attestMsg(f, "acme", val(1), val(2), val(3)))
	require.NoError(t, err)
	require.False(t, res.Replaced)

	res, err = srv.AttestEntity(f.ctx, attestMsg(f, "acme", val(1), val(2), val(3)))
	require.NoError(t, err)
	require.True(t, res.Replaced)

	members, err := f.k.MemberValidators(f.ctx, "acme")
	require.NoError(t, err)
	require.Len(t, members, 3)
}

func TestMsgAttestEntityBadAddresses(t *testing.T) {
	f := newFixture(t)
	srv := keeper.NewMsgServerImpl(f.k)

	msg := attestMsg(f, "acme", val(1), val(2))
	msg.Validators[1] = "not-a-valoper"
	_, err := srv.AttestEntity(f.ctx, msg)
	require.ErrorIs(t, err, types.ErrInvalidEntity)
	require.ErrorContains(t, err, "validators[1]")

	msg = attestMsg(f, "acme", val(1))
	msg.Attestor = "not-an-address"
	_, err = srv.AttestEntity(f.ctx, msg)
	require.Error(t, err)
	ok, herr := f.k.Entities.Has(f.ctx, "acme")
	require.NoError(t, herr)
	require.False(t, ok, "a refused message writes nothing")
}

func TestMsgAttestEntityNotAttestor(t *testing.T) {
	f := newFixture(t)
	srv := keeper.NewMsgServerImpl(f.k)
	msg := attestMsg(f, "acme", val(1))
	msg.Attestor = acc(77).String()
	_, err := srv.AttestEntity(f.ctx, msg)
	require.ErrorIs(t, err, types.ErrNotAttestor)
}

func TestMsgRevokeEntityReturnsFrozenValopers(t *testing.T) {
	f := newFixture(t)
	srv := keeper.NewMsgServerImpl(f.k)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_BONDING
	_, err := srv.AttestEntity(f.ctx, attestMsg(f, "acme", val(1), val(2)))
	require.NoError(t, err)

	res, err := srv.RevokeEntity(f.ctx, &types.MsgRevokeEntity{
		Authority: f.attestor.String(), EntityId: "acme", Reason: "key compromise",
	})
	require.NoError(t, err)
	require.Equal(t, []string{val(1).String()}, res.Frozen)

	_, err = srv.RevokeEntity(f.ctx, &types.MsgRevokeEntity{
		Authority: f.attestor.String(), EntityId: "acme", Reason: "again",
	})
	require.ErrorIs(t, err, types.ErrEntityRevoked)

	_, err = srv.RevokeEntity(f.ctx, &types.MsgRevokeEntity{
		Authority: "not-an-address", EntityId: "acme", Reason: "x",
	})
	require.Error(t, err)
}

func TestMsgUpdateParams(t *testing.T) {
	f := newFixture(t)
	srv := keeper.NewMsgServerImpl(f.k)

	_, err := srv.UpdateParams(f.ctx, &types.MsgUpdateParams{
		Authority: f.attestor.String(), Params: types.Params{MaxActiveSeats: 3},
	})
	require.ErrorIs(t, err, types.ErrInvalidSigner)

	_, err = srv.UpdateParams(f.ctx, &types.MsgUpdateParams{
		Authority: govAddress(), Params: types.Params{MaxActiveSeats: 4},
	})
	require.Error(t, err)
	p, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(2), p.MaxActiveSeats, "an invalid update stores nothing")

	_, err = srv.UpdateParams(f.ctx, &types.MsgUpdateParams{
		Authority: govAddress(), Params: types.Params{MaxActiveSeats: 3},
	})
	require.NoError(t, err)
	p, err = f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(3), p.MaxActiveSeats)
}

func TestMsgOfferVrfKey(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	srv := keeper.NewMsgServerImpl(f.k)
	k := vrfKey(t, 1)
	pk, pop := offerFor(t, k, val(1), testChainID)

	_, err := srv.OfferVrfKey(f.ctx, &types.MsgOfferVrfKey{Operator: acc(1).String(), VrfPublicKey: pk, ProofOfPossession: pop})
	require.NoError(t, err)
	offer, err := f.k.VrfKeyOffers.Get(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, pk, offer.VrfPublicKey)

	_, err = srv.OfferVrfKey(f.ctx, &types.MsgOfferVrfKey{Operator: "not-an-address", VrfPublicKey: pk, ProofOfPossession: pop})
	require.ErrorIs(t, err, sdkerrors.ErrInvalidAddress)
}

func TestMsgAttestEntityPassesVrfKeys(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	srv := keeper.NewMsgServerImpl(f.k)
	k := vrfKey(t, 1)
	f.offer(t, k, val(1))
	pk := hex.EncodeToString(k.PublicKey())

	msg := attestMsg(f, "acme", val(1))
	msg.VrfKeys = []types.MemberVrfKey{{Validator: val(1).String(), VrfPublicKey: pk}}
	_, err := srv.AttestEntity(f.ctx, msg)
	require.NoError(t, err)
	got, _ := memberKey(t, f, val(1))
	require.Equal(t, pk, got)

	bad := attestMsg(f, "beta", val(2))
	bad.VrfKeys = []types.MemberVrfKey{{Validator: "not-a-valoper", VrfPublicKey: pk}}
	_, err = srv.AttestEntity(f.ctx, bad)
	require.ErrorIs(t, err, types.ErrInvalidEntity)
	require.ErrorContains(t, err, "vrf_keys[0]")

	bad.VrfKeys = []types.MemberVrfKey{{Validator: val(2).String(), VrfPublicKey: "zz"}}
	_, err = srv.AttestEntity(f.ctx, bad)
	require.ErrorIs(t, err, types.ErrInvalidVrfKey)
}
