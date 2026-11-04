package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func scn(n int64) math.Int { return math.NewInt(n).MulRaw(100_000_000) }

// hold puts a bonded validator with the given tokens into BONDING and pending,
// as the gate leaves it.
func hold(t *testing.T, f *fixture, v sdk.ValAddress, tokens math.Int) {
	t.Helper()
	f.reg.status[v.String()] = vrtypes.Status_STATUS_BONDING
	f.staking.bond(v, tokens, "m")
	require.NoError(t, f.k.Pending.Set(f.ctx, v))
}

func pending(t *testing.T, f *fixture, v sdk.ValAddress) bool {
	t.Helper()
	has, err := f.k.Pending.Has(f.ctx, v)
	require.NoError(t, err)
	return has
}

func TestEndBlockPromotesAnAttestedHeldValidator(t *testing.T) {
	f := newFixture(t)
	hold(t, f, val(1), scn(100))
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)

	require.NoError(t, f.k.EndBlock(f.ctx))
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, f.reg.status[val(1).String()])
	require.False(t, pending(t, f, val(1)))
	requireEvent(t, f.ctx, types.EventTypeActivationReleased, "validator", val(1).String())
	requireEvent(t, f.ctx, types.EventTypeActivationReleased, "entity_id", "acme")
}

func TestPromotionOrderIsTokensThenAddress(t *testing.T) {
	setup := func(t *testing.T, t2, t3 int64) *fixture {
		f := newFixture(t)
		_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2), val(3)))
		require.NoError(t, err)
		f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
		hold(t, f, val(2), scn(t2))
		hold(t, f, val(3), scn(t3))
		return f
	}
	t.Run("more tokens first", func(t *testing.T) {
		f := setup(t, 100, 200)
		require.NoError(t, f.k.EndBlock(f.ctx))
		require.Equal(t, vrtypes.Status_STATUS_ACTIVE, f.reg.status[val(3).String()])
		require.Equal(t, vrtypes.Status_STATUS_BONDING, f.reg.status[val(2).String()])
		require.True(t, pending(t, f, val(2)), "the loser stays held")
		require.False(t, pending(t, f, val(3)))
	})
	t.Run("equal tokens: the lower address", func(t *testing.T) {
		f := setup(t, 100, 100)
		require.NoError(t, f.k.EndBlock(f.ctx))
		require.Equal(t, vrtypes.Status_STATUS_ACTIVE, f.reg.status[val(2).String()])
		require.Equal(t, vrtypes.Status_STATUS_BONDING, f.reg.status[val(3).String()])
		require.True(t, pending(t, f, val(3)))
	})
}

func TestAStillBlockedHeldValidatorChangesNothingInEndBlock(t *testing.T) {
	f := newFixture(t)
	hold(t, f, val(5), scn(100)) // never attested
	before, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	events := len(f.ctx.EventManager().Events())

	require.NoError(t, f.k.EndBlock(f.ctx))

	after, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Len(t, f.ctx.EventManager().Events(), events, "no held event is repeated every block")
	require.Equal(t, vrtypes.Status_STATUS_BONDING, f.reg.status[val(5).String()])
}

func TestPendingDropsWhatIsNoLongerBondedOrBonding(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2)))
	require.NoError(t, err)
	hold(t, f, val(1), scn(100))
	f.staking.unbond(val(1))
	hold(t, f, val(2), scn(100))
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_QUARANTINED
	// val(3) is unknown to x/staking altogether.
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_BONDING
	require.NoError(t, f.k.Pending.Set(f.ctx, val(3)))

	require.NoError(t, f.k.EndBlock(f.ctx))
	for _, b := range []byte{1, 2, 3} {
		require.False(t, pending(t, f, val(b)), "validator %d", b)
	}
	require.Equal(t, vrtypes.Status_STATUS_BONDING, f.reg.status[val(1).String()])
	require.Equal(t, vrtypes.Status_STATUS_QUARANTINED, f.reg.status[val(2).String()])
}

func TestAnUnbondingHeldValidatorIsPromoted(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	hold(t, f, val(1), scn(100))
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_UNBONDING
	require.NoError(t, f.k.EndBlock(f.ctx))
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, f.reg.status[val(1).String()])
}

func TestAFailedPromotionStaysPending(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	hold(t, f, val(1), scn(100))
	f.reg.failActivate = true
	require.NoError(t, f.k.EndBlock(f.ctx))
	require.True(t, pending(t, f, val(1)))
	require.Equal(t, vrtypes.Status_STATUS_BONDING, f.reg.status[val(1).String()])
}

func TestFrozenRowsAreSweptWhenTheSeatIsGone(t *testing.T) {
	f := newFixture(t)
	for _, b := range []byte{1, 2, 3} {
		require.NoError(t, f.k.Frozen.Set(f.ctx, val(b), types.FrozenSeat{
			Validator: val(b).String(), FormerEntityId: "acme",
		}))
	}
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_UNBONDING
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_ACTIVE
	// val(3) has no registry record at all.

	require.NoError(t, f.k.EndBlock(f.ctx))
	has1, _ := f.k.Frozen.Has(f.ctx, val(1))
	has2, _ := f.k.Frozen.Has(f.ctx, val(2))
	has3, _ := f.k.Frozen.Has(f.ctx, val(3))
	require.False(t, has1)
	require.True(t, has2, "an ACTIVE frozen seat is kept")
	require.False(t, has3)
}

func TestRevokingAnActiveMemberDoesNotHaltAtEndBlock(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "lapsed")
	require.NoError(t, err)
	require.NotPanics(t, func() { require.NoError(t, f.k.EndBlock(f.ctx)) })
	frozen, err := f.k.IsFrozenSeat(f.ctx, val(1))
	require.NoError(t, err)
	require.True(t, frozen)
}

// Review Focus 2, sequence one: revoke an entity whose member is ACTIVE, then
// re-attest it in the same block.
func TestRevokeThenReattestInOneBlockKeepsV12(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE

	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "lapsed")
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)

	require.NotPanics(t, func() { require.NoError(t, f.k.EndBlock(f.ctx)) })
	msg, broken := f.k.RegistryInvariant(f.ctx)
	require.False(t, broken, msg)
	frozen, err := f.k.IsFrozenSeat(f.ctx, val(1))
	require.NoError(t, err)
	require.False(t, frozen, "re-attesting clears the frozen seat")
}

// Review Focus 2, sequence two: remove an ACTIVE member and attest it into
// another entity in the same block.
func TestMoveAnActiveMemberInOneBlockKeepsV12(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2)))
	require.NoError(t, err)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_ACTIVE

	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("beta", val(2)))
	require.NoError(t, err)

	require.NotPanics(t, func() { require.NoError(t, f.k.EndBlock(f.ctx)) })
	m, found, err := f.k.EntityOf(f.ctx, val(2))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "beta", m.EntityId)
	frozen, err := f.k.IsFrozenSeat(f.ctx, val(2))
	require.NoError(t, err)
	require.False(t, frozen)
}
