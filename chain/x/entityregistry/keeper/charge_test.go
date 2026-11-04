package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// A frozen seat whose validator is still ACTIVE keeps earning (J2), so it keeps
// counting against the entity it was frozen out of: otherwise re-attesting an
// entity with fresh validators, or revoking and re-attesting it, would let one
// owner hold more than max_active_seats.

// fullEntity attests acme = {1, 2}, both ACTIVE, at the two-seat limit.
func fullEntity(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2)))
	require.NoError(t, err)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_BONDING
	f.reg.status[val(4).String()] = vrtypes.Status_STATUS_BONDING
	return f
}

func requireSeats(t *testing.T, f *fixture, id string, want uint32) {
	t.Helper()
	n, err := f.k.ActiveSeats(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, want, n)
}

func requireHeldForSeats(t *testing.T, f *fixture, v sdk.ValAddress) {
	t.Helper()
	err := f.k.MayActivate(f.ctx, v)
	require.ErrorIs(t, err, vrtypes.ErrActivationHeld)
	require.Contains(t, err.Error(), "entity acme already holds 2 active seats; the limit is 2")
}

func TestReattestingAFullEntityChargesItsFrozenSeats(t *testing.T) {
	f := fullEntity(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(3), val(4)))
	require.NoError(t, err)
	frozen, err := f.k.IsFrozenSeat(f.ctx, val(1))
	require.NoError(t, err)
	require.True(t, frozen, "1 is frozen, still ACTIVE")

	requireSeats(t, f, "acme", 2)
	requireHeldForSeats(t, f, val(3))
	requireHeldForSeats(t, f, val(4))
}

func TestRevokeThenReattestChargesTheFrozenSeats(t *testing.T) {
	f := fullEntity(t)
	_, err := f.k.Revoke(f.ctx, f.attestor, "acme", "lapsed")
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", val(3), val(4)))
	require.NoError(t, err)

	requireSeats(t, f, "acme", 2)
	requireHeldForSeats(t, f, val(3))
}

func TestTheRoomReturnsWhenAFrozenValidatorLeavesActive(t *testing.T) {
	f := fullEntity(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(3), val(4)))
	require.NoError(t, err)
	requireHeldForSeats(t, f, val(3))

	// 1 leaves ACTIVE: its charge ends at once, before EndBlock sweeps the row.
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_UNBONDING
	requireSeats(t, f, "acme", 1)
	require.NoError(t, f.k.MayActivate(f.ctx, val(3)))
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_ACTIVE
	requireHeldForSeats(t, f, val(4))

	// EndBlock sweeps 1's row; 2 still counts.
	require.NoError(t, f.k.EndBlock(f.ctx))
	frozen, err := f.k.IsFrozenSeat(f.ctx, val(1))
	require.NoError(t, err)
	require.False(t, frozen)
	requireSeats(t, f, "acme", 2)
}

func TestAFrozenSeatCountsOnlyForItsFormerEntity(t *testing.T) {
	f := fullEntity(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(3)))
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme-2", val(4)))
	require.NoError(t, err)

	requireSeats(t, f, "acme", 2)
	requireSeats(t, f, "acme-2", 0)
	require.NoError(t, f.k.MayActivate(f.ctx, val(4)), "acme-2 is charged nothing")
}

// Joining an entity clears the frozen row, so a validator moved into another
// entity counts there once, and no longer for its former one.
func TestAFrozenValidatorJoiningAnotherEntityCountsThereOnly(t *testing.T) {
	f := fullEntity(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(3)))
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("beta", val(1)))
	require.NoError(t, err)

	requireSeats(t, f, "acme", 1)
	requireSeats(t, f, "beta", 1)
	require.NoError(t, f.k.MayActivate(f.ctx, val(3)))
}
