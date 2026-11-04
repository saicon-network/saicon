package keeper_test

import (
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestAttestCreatesAndIndexes(t *testing.T) {
	f := newFixture(t)
	replaced, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2)))
	require.NoError(t, err)
	require.False(t, replaced)
	e, err := f.k.Entities.Get(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, types.EntityStatus_ENTITY_STATUS_ATTESTED, e.Status)
	require.Equal(t, int64(10), e.AttestedHeight)
	m, found, err := f.k.EntityOf(f.ctx, val(2))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "acme", m.EntityId)
	vals, err := f.k.MemberValidators(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, []sdk.ValAddress{val(1), val(2)}, vals)
	requireEvent(t, f.ctx, types.EventTypeEntityAttested, "entity_id", "acme")
}

func TestOnlyAttestorsAndGovMaySign(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, acc(99), att("acme", val(1)))
	require.ErrorIs(t, err, types.ErrNotAttestor)
	gov := authtypes.NewModuleAddress(types.GovModuleName)
	_, err = f.k.Attest(f.ctx, gov, att("acme", val(1)))
	require.NoError(t, err)
}

func TestReattestReplacesMembersAndFreezesARemovedActiveOne(t *testing.T) {
	f := newFixture(t)
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_BONDING
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2), val(3)))
	require.NoError(t, err)

	replaced, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	require.True(t, replaced)
	for _, b := range []byte{2, 3} {
		_, found, err := f.k.EntityOf(f.ctx, val(b))
		require.NoError(t, err)
		require.False(t, found)
	}
	frozen, err := f.k.IsFrozenSeat(f.ctx, val(2))
	require.NoError(t, err)
	require.True(t, frozen)
	seat, err := f.k.Frozen.Get(f.ctx, val(2))
	require.NoError(t, err)
	require.Equal(t, "acme", seat.FormerEntityId)
	require.Equal(t, "removed from entity acme", seat.Reason)
	require.Equal(t, int64(10), seat.Height)
	// A removed member that was only BONDING holds no seat to freeze.
	frozen, err = f.k.IsFrozenSeat(f.ctx, val(3))
	require.NoError(t, err)
	require.False(t, frozen)
	requireEvent(t, f.ctx, types.EventTypeMemberRemoved, "validator", val(3).String())
	requireEvent(t, f.ctx, types.EventTypeSeatFrozen, "validator", val(2).String())
}

func TestAMemberOfAnotherEntityIsRefusedAndNothingIsWritten(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("beta", val(2), val(1)))
	require.ErrorIs(t, err, types.ErrMemberOfAnotherEntity)
	has, err := f.k.Entities.Has(f.ctx, "beta")
	require.NoError(t, err)
	require.False(t, has)
	_, found, err := f.k.EntityOf(f.ctx, val(2))
	require.NoError(t, err)
	require.False(t, found)
	m, found, err := f.k.EntityOf(f.ctx, val(1))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "acme", m.EntityId)
}

func TestReattestClearsAFrozenSeat(t *testing.T) {
	f := newFixture(t)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "document withdrawn")
	require.NoError(t, err)
	frozen, err := f.k.IsFrozenSeat(f.ctx, val(1))
	require.NoError(t, err)
	require.True(t, frozen)

	_, err = f.k.Attest(f.ctx, f.attestor, att("beta", val(1)))
	require.NoError(t, err)
	frozen, err = f.k.IsFrozenSeat(f.ctx, val(1))
	require.NoError(t, err)
	require.False(t, frozen)
	m, found, err := f.k.EntityOf(f.ctx, val(1))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "beta", m.EntityId)
}

func TestRevokeFreezesOnlyActiveMembers(t *testing.T) {
	f := newFixture(t)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_BONDING
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_QUARANTINED
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2), val(3)))
	require.NoError(t, err)

	frozen, err := f.k.Revoke(f.ctx, f.attestor, "acme", "document withdrawn")
	require.NoError(t, err)
	require.Equal(t, []sdk.ValAddress{val(1)}, frozen)
	e, err := f.k.Entities.Get(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, types.EntityStatus_ENTITY_STATUS_REVOKED, e.Status)
	require.Equal(t, int64(10), e.RevokedHeight)
	require.Equal(t, "document withdrawn", e.RevokeReason)
	vals, err := f.k.MemberValidators(f.ctx, "acme")
	require.NoError(t, err)
	require.Empty(t, vals)
	for _, b := range []byte{1, 2, 3} {
		_, found, err := f.k.EntityOf(f.ctx, val(b))
		require.NoError(t, err)
		require.False(t, found)
	}
	seat, err := f.k.Frozen.Get(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, "entity acme revoked: document withdrawn", seat.Reason)
	for _, b := range []byte{2, 3} {
		has, err := f.k.Frozen.Has(f.ctx, val(b))
		require.NoError(t, err)
		require.False(t, has)
	}
	requireEvent(t, f.ctx, types.EventTypeEntityRevoked, "entity_id", "acme")
	requireEvent(t, f.ctx, types.EventTypeEntityRevoked, "authority", f.attestor.String())
	requireEvent(t, f.ctx, types.EventTypeEntityRevoked, "reason", "document withdrawn")
}

// The event names whoever signed, the gov authority included.
func TestRevokeEventNamesTheGovAuthority(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	gov := sdk.AccAddress(f.k.GetAuthority())
	_, err = f.k.Revoke(f.ctx, gov, "acme", "by vote")
	require.NoError(t, err)
	requireEvent(t, f.ctx, types.EventTypeEntityRevoked, "authority", gov.String())
}

func TestRevokeErrors(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Revoke(f.ctx, f.attestor, "ghost", "gone")
	require.ErrorIs(t, err, types.ErrEntityNotFound)

	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "")
	require.ErrorIs(t, err, types.ErrInvalidEntity)
	_, err = f.k.Revoke(f.ctx, acc(99), "acme", "gone")
	require.ErrorIs(t, err, types.ErrNotAttestor)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "gone")
	require.NoError(t, err)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "gone")
	require.ErrorIs(t, err, types.ErrEntityRevoked)
}

func TestReattestARevokedIdRestoresIt(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "gone")
	require.NoError(t, err)

	replaced, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	require.True(t, replaced)
	e, err := f.k.Entities.Get(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, types.EntityStatus_ENTITY_STATUS_ATTESTED, e.Status)
	require.Zero(t, e.RevokedHeight)
	require.Empty(t, e.RevokeReason)
}

// bind builds a binding of key k to validator v.
func bind(k vrf.PrivateKey, v sdk.ValAddress) types.VrfKeyBinding {
	return types.VrfKeyBinding{Validator: v, PublicKey: k.PublicKey()}
}

func TestAttestBindsAnOfferedKey(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k1 := vrfKey(t, 1)
	f.offer(t, k1, val(1))

	_, err := f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1)}, bind(k1, val(1))))
	require.NoError(t, err)
	m, found, err := f.k.EntityOf(f.ctx, val(1))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, hex.EncodeToString(k1.PublicKey()), m.VrfPublicKey)
	require.Equal(t, int64(10), m.VrfKeyHeight)
	requireEvent(t, f.ctx, types.EventTypeVrfKeyBound, "validator", val(1).String())
	requireEvent(t, f.ctx, types.EventTypeVrfKeyBound, "entity_id", "acme")
	requireEvent(t, f.ctx, types.EventTypeVrfKeyBound, "vrf_public_key", m.VrfPublicKey)
	requireEvent(t, f.ctx, types.EventTypeEntityAttested, "vrf_keys", val(1).String())
}

func TestAttestRefusesAKeyThatWasNotOffered(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k1, k2 := vrfKey(t, 1), vrfKey(t, 2)

	_, err := f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1)}, bind(k1, val(1))))
	require.ErrorIs(t, err, types.ErrVrfKeyNotOffered)

	f.offer(t, k1, val(1))
	_, err = f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1)}, bind(k2, val(1))))
	require.ErrorIs(t, err, types.ErrVrfKeyNotOffered)

	ok, err := f.k.Entities.Has(f.ctx, "acme")
	require.NoError(t, err)
	require.False(t, ok, "a refused attestation writes nothing")
	_, found, err := f.k.EntityOf(f.ctx, val(1))
	require.NoError(t, err)
	require.False(t, found, "no Members row either")
	vals, err := f.k.MemberValidators(f.ctx, "acme")
	require.NoError(t, err)
	require.Empty(t, vals)
}

// An attestor cannot hand one validator's offered key to another validator: the
// binding must name the key that the validator's own operator offered.
func TestAttestRefusesAKeyOfferedByAnotherValidator(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k1, k2 := vrfKey(t, 1), vrfKey(t, 2)
	f.offer(t, k1, val(1))

	// val(2) has offered nothing.
	_, err := f.k.Attest(f.ctx, f.attestor,
		attWithKeys("acme", []sdk.ValAddress{val(2)}, bind(k1, val(2))))
	require.ErrorIs(t, err, types.ErrVrfKeyNotOffered)

	// val(2) has offered its own, different key.
	f.offer(t, k2, val(2))
	_, err = f.k.Attest(f.ctx, f.attestor,
		attWithKeys("acme", []sdk.ValAddress{val(2)}, bind(k1, val(2))))
	require.ErrorIs(t, err, types.ErrVrfKeyNotOffered)

	for _, v := range []sdk.ValAddress{val(1), val(2)} {
		_, found, err := f.k.EntityOf(f.ctx, v)
		require.NoError(t, err)
		require.False(t, found, "nothing was written for %s", v)
	}
	ok, err := f.k.Entities.Has(f.ctx, "acme")
	require.NoError(t, err)
	require.False(t, ok)
}

// Revoking an entity does not free its members' keys: the key stays owned by the
// validator that offered it, nobody else can offer it, and an attestor cannot
// bind it to anyone else.
func TestARevokedEntitysKeyStaysWithItsOwner(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k1 := vrfKey(t, 1)
	f.offer(t, k1, val(1))
	_, err := f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1)}, bind(k1, val(1))))
	require.NoError(t, err)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "document withdrawn")
	require.NoError(t, err)

	pk, pop := offerFor(t, k1, val(3), testChainID)
	require.ErrorIs(t, f.k.OfferVrfKey(f.ctx, acc(3), pk, pop), types.ErrVrfKeyInUse)
	_, err = f.k.Attest(f.ctx, f.attestor, attWithKeys("beta", []sdk.ValAddress{val(3)}, bind(k1, val(3))))
	require.ErrorIs(t, err, types.ErrVrfKeyNotOffered)

	_, found, err := f.k.EntityOf(f.ctx, val(3))
	require.NoError(t, err)
	require.False(t, found)
	owner, err := f.k.VrfKeyOwners.Get(f.ctx, k1.PublicKey())
	require.NoError(t, err)
	require.Equal(t, val(1).Bytes(), owner)
}

// A validator that re-attests into another entity after a revocation starts the
// key's seasoning again: the binding's height is the new attestation's.
func TestReattestIntoAnotherEntityResetsTheKeyHeight(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k1 := vrfKey(t, 1)
	f.offer(t, k1, val(1))
	_, err := f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1)}, bind(k1, val(1))))
	require.NoError(t, err)
	_, h := memberKey(t, f, val(1))
	require.Equal(t, int64(10), h)

	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "document withdrawn")
	require.NoError(t, err)
	f.ctx = f.ctx.WithBlockHeight(50)
	_, err = f.k.Attest(f.ctx, f.attestor, attWithKeys("beta", []sdk.ValAddress{val(1)}, bind(k1, val(1))))
	require.NoError(t, err)
	pk, h := memberKey(t, f, val(1))
	require.Equal(t, hex.EncodeToString(k1.PublicKey()), pk)
	require.Equal(t, int64(50), h, "a new entity means a new seasoning period")
}

// memberKey returns the key and height on a validator's member row.
func memberKey(t *testing.T, f *fixture, v sdk.ValAddress) (string, int64) {
	t.Helper()
	m, found, err := f.k.EntityOf(f.ctx, v)
	require.NoError(t, err)
	require.True(t, found)
	return m.VrfPublicKey, m.VrfKeyHeight
}

func TestReattestKeepsAStayingMembersKey(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k1 := vrfKey(t, 1)
	f.offer(t, k1, val(1))
	_, err := f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1)}, bind(k1, val(1))))
	require.NoError(t, err)

	f.ctx = f.ctx.WithBlockHeight(20)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2)))
	require.NoError(t, err)
	pk, h := memberKey(t, f, val(1))
	require.Equal(t, hex.EncodeToString(k1.PublicKey()), pk)
	require.Equal(t, int64(10), h)
	pk, h = memberKey(t, f, val(2))
	require.Empty(t, pk)
	require.Zero(t, h)
}

func TestRestatingTheSameKeyKeepsItsHeight(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k1, k2 := vrfKey(t, 1), vrfKey(t, 2)
	f.offer(t, k1, val(1))
	_, err := f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1)}, bind(k1, val(1))))
	require.NoError(t, err)

	f.ctx = f.ctx.WithBlockHeight(20).WithEventManager(sdk.NewEventManager())
	_, err = f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1)}, bind(k1, val(1))))
	require.NoError(t, err)
	_, h := memberKey(t, f, val(1))
	require.Equal(t, int64(10), h, "re-stating a key never resets its seasoning")
	for _, ev := range f.ctx.EventManager().Events() {
		require.NotEqual(t, types.EventTypeVrfKeyBound, ev.Type, "nothing was bound")
	}
	requireEvent(t, f.ctx, types.EventTypeEntityAttested, "vrf_keys", "")

	f.offer(t, k2, val(1))
	f.ctx = f.ctx.WithBlockHeight(30)
	_, err = f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1)}, bind(k2, val(1))))
	require.NoError(t, err)
	pk, h := memberKey(t, f, val(1))
	require.Equal(t, hex.EncodeToString(k2.PublicKey()), pk)
	require.Equal(t, int64(30), h)
}

func TestRemovalDeletesTheKeyAndKeepsTheOffer(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k1 := vrfKey(t, 1)
	f.offer(t, k1, val(1))
	_, err := f.k.Attest(f.ctx, f.attestor, attWithKeys("acme", []sdk.ValAddress{val(1), val(2)}, bind(k1, val(1))))
	require.NoError(t, err)

	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", val(2)))
	require.NoError(t, err)
	_, found, err := f.k.EntityOf(f.ctx, val(1))
	require.NoError(t, err)
	require.False(t, found)
	offer, err := f.k.VrfKeyOffers.Get(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(k1.PublicKey()), offer.VrfPublicKey)

	f.ctx = f.ctx.WithBlockHeight(40)
	_, err = f.k.Attest(f.ctx, f.attestor, attWithKeys("beta", []sdk.ValAddress{val(1)}, bind(k1, val(1))))
	require.NoError(t, err)
	pk, h := memberKey(t, f, val(1))
	require.Equal(t, hex.EncodeToString(k1.PublicKey()), pk)
	require.Equal(t, int64(40), h)
}
