package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestActiveSeatsCountsOnlyActive(t *testing.T) {
	f := newFixture(t)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_QUARANTINED
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_ACTIVE
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2), val(3)))
	require.NoError(t, err)
	n, err := f.k.ActiveSeats(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, uint32(2), n)
}

func TestMaxActiveSeatsReadsParams(t *testing.T) {
	f := newFixture(t)
	n, err := f.k.MaxActiveSeats(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(2), n)
}

func TestTicketsOnePerAttestedEntity(t *testing.T) {
	f := newFixture(t)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	for _, e := range []struct {
		id   string
		vals []sdk.ValAddress
	}{
		{"beta", []sdk.ValAddress{val(3)}},
		{"acme", []sdk.ValAddress{val(1), val(2)}},
		{"gamma", []sdk.ValAddress{val(4)}},
	} {
		_, err := f.k.Attest(f.ctx, f.attestor, att(e.id, e.vals...))
		require.NoError(t, err)
	}
	_, err := f.k.Revoke(f.ctx, f.attestor, "gamma", "gone")
	require.NoError(t, err)

	tickets, err := f.k.Tickets(f.ctx)
	require.NoError(t, err)
	require.Equal(t, []types.Ticket{
		{EntityId: "acme", Members: []string{val(1).String(), val(2).String()}, ActiveSeats: 1, Tickets: 1},
		{EntityId: "beta", Members: []string{val(3).String()}, ActiveSeats: 0, Tickets: 1},
	}, tickets)
	ids, err := f.k.AttestedEntityIDs(f.ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"acme", "beta"}, ids)
}

// The staking stub is what Task 4's gate tests bond and unbond through; this
// pins its contract so those tests can rely on it.
func TestStubStakingContract(t *testing.T) {
	f := newFixture(t)
	_, err := f.staking.GetValidator(f.ctx, val(1))
	require.ErrorIs(t, err, stakingtypes.ErrNoValidatorFound)
	f.staking.bond(val(1), math.NewInt(5), "one")
	v, err := f.staking.GetValidator(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, stakingtypes.Bonded, v.Status)
	require.Equal(t, "one", v.Description.Moniker)
	f.staking.unbond(val(1))
	v, err = f.staking.GetValidator(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, stakingtypes.Unbonding, v.Status)
}

// The by-entity index is a (string, bytes) pair: an entity id that is a prefix
// of another must not see the other's members, in either direction.
func TestMemberValidatorsIsolatesEntityIDPrefixes(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2)))
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme-2", val(3)))
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acm", val(4)))
	require.NoError(t, err)

	got, err := f.k.MemberValidators(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, []sdk.ValAddress{val(1), val(2)}, got)
	got, err = f.k.MemberValidators(f.ctx, "acme-2")
	require.NoError(t, err)
	require.Equal(t, []sdk.ValAddress{val(3)}, got)
	got, err = f.k.MemberValidators(f.ctx, "acm")
	require.NoError(t, err)
	require.Equal(t, []sdk.ValAddress{val(4)}, got)
}
