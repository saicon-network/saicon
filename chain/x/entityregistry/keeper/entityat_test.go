package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// at moves the fixture to a block height.
func (f *fixture) at(h int64) { f.ctx = f.ctx.WithBlockHeight(h) }

// requireEntityAt fails unless EntityAt(v, h) answers want (empty: no entity).
func requireEntityAt(t *testing.T, f *fixture, v sdk.ValAddress, h int64, want string) {
	t.Helper()
	got, ok, err := f.k.EntityAt(f.ctx, v, h)
	require.NoError(t, err)
	require.Equal(t, want != "", ok, "linked at %d", h)
	require.Equal(t, want, got, "entity at %d", h)
}

func requireDeparture(t *testing.T, f *fixture, v sdk.ValAddress, want types.Departure) {
	t.Helper()
	got, err := f.k.Departures.Get(f.ctx, v)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestEntityAtFollowsTheVoteConvention pins spec §6.2: a vote at h is judged on
// the state committed at h-1.
func TestEntityAtFollowsTheVoteConvention(t *testing.T) {
	f := newFixture(t)
	v, w := val(1), val(2)

	// Joined at 10: a vote at 10 is judged on the state committed at 9.
	f.at(10)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", v))
	require.NoError(t, err)
	requireEntityAt(t, f, v, 10, "")
	requireEntityAt(t, f, v, 11, "acme")

	// Removed at 20 while BONDING: no frozen seat, so the link ends after 20.
	f.at(20)
	f.reg.status[v.String()] = vrtypes.Status_STATUS_BONDING
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", w))
	require.NoError(t, err)
	requireDeparture(t, f, v, types.Departure{
		Validator: v.String(), EntityId: "acme", JoinedHeight: 10, LeftHeight: 20,
	})
	requireEntityAt(t, f, v, 20, "acme")
	requireEntityAt(t, f, v, 21, "")
	requireEntityAt(t, f, v, 15, "acme")

	// Re-joins at 30 ACTIVE, then the entity is revoked at 40: the seat freezes.
	f.at(30)
	f.reg.status[v.String()] = vrtypes.Status_STATUS_ACTIVE
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", v, w))
	require.NoError(t, err)
	f.at(40)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "lapsed")
	require.NoError(t, err)
	requireDeparture(t, f, v, types.Departure{
		Validator: v.String(), EntityId: "acme", JoinedHeight: 30, LeftHeight: 40, Frozen: true,
	})
	requireEntityAt(t, f, v, 41, "acme")
	requireEntityAt(t, f, v, 100, "acme")

	// The charge ends when the validator leaves ACTIVE and EndBlock sweeps the seat.
	f.at(50)
	f.reg.status[v.String()] = vrtypes.Status_STATUS_UNBONDING
	require.NoError(t, f.k.EndBlock(f.ctx))
	requireDeparture(t, f, v, types.Departure{
		Validator: v.String(), EntityId: "acme", JoinedHeight: 30, LeftHeight: 40, Frozen: true, ChargedUntil: 50,
	})
	requireEntityAt(t, f, v, 50, "acme")
	requireEntityAt(t, f, v, 51, "")

	// Joins beta at 60: too young for a vote at 60, and the departure is over.
	f.at(60)
	_, err = f.k.Attest(f.ctx, f.attestor, att("beta", v))
	require.NoError(t, err)
	requireEntityAt(t, f, v, 60, "")
	requireEntityAt(t, f, v, 61, "beta")
	requireEntityAt(t, f, v, 45, "acme")
}

// TestEntityAtEdges pins the three boundaries one at a time.
func TestEntityAtEdges(t *testing.T) {
	f := newFixture(t)
	v := val(1)
	f.at(10)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", v))
	require.NoError(t, err)
	f.reg.status[v.String()] = vrtypes.Status_STATUS_ACTIVE
	f.at(20)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "lapsed")
	require.NoError(t, err)

	// h == J: not yet; h == J+1: yes. A zero-height query is never linked.
	requireEntityAt(t, f, v, 10, "")
	requireEntityAt(t, f, v, 11, "acme")
	requireEntityAt(t, f, v, 0, "")
	// h == left_height, then well past it while the seat is still charged.
	requireEntityAt(t, f, v, 20, "acme")
	requireEntityAt(t, f, v, 21, "acme")

	// h == charged_until is still charged, h == charged_until+1 is not.
	f.at(30)
	f.reg.status[v.String()] = vrtypes.Status_STATUS_BONDING
	require.NoError(t, f.k.EndBlock(f.ctx))
	requireEntityAt(t, f, v, 30, "acme")
	requireEntityAt(t, f, v, 31, "")

	// A validator that never attested is linked to nothing.
	requireEntityAt(t, f, val(9), 100, "")
}

// TestJoinedHeightIsKeptForAStayingMember pins spec §6.2: a staying member keeps
// the height it joined; a new member gets the current one.
func TestJoinedHeightIsKeptForAStayingMember(t *testing.T) {
	f := newFixture(t)
	v, w := val(1), val(2)
	f.at(10)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", v))
	require.NoError(t, err)
	f.at(20)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", v, w))
	require.NoError(t, err)

	mv, found, err := f.k.EntityOf(f.ctx, v)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(10), mv.JoinedHeight)
	require.Equal(t, int64(20), mv.AttestedHeight)
	mw, found, err := f.k.EntityOf(f.ctx, w)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(20), mw.JoinedHeight)
}

// TestEveryRemovalWritesADeparture covers revocation and re-attestation.
func TestEveryRemovalWritesADeparture(t *testing.T) {
	f := newFixture(t)
	a, b, c := val(1), val(2), val(3)
	f.at(10)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", a, b))
	require.NoError(t, err)
	f.at(12)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", a, b, c))
	require.NoError(t, err)
	f.reg.status[a.String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[b.String()] = vrtypes.Status_STATUS_BONDING
	f.reg.status[c.String()] = vrtypes.Status_STATUS_BONDING

	// A re-attestation that drops a member writes one departure, with its join height.
	f.at(15)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", a, b))
	require.NoError(t, err)
	requireDeparture(t, f, c, types.Departure{
		Validator: c.String(), EntityId: "acme", JoinedHeight: 12, LeftHeight: 15,
	})
	n, err := countDepartures(f)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// A revocation writes one per member: frozen for the ACTIVE one only.
	f.at(20)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "lapsed")
	require.NoError(t, err)
	requireDeparture(t, f, a, types.Departure{
		Validator: a.String(), EntityId: "acme", JoinedHeight: 10, LeftHeight: 20, Frozen: true,
	})
	requireDeparture(t, f, b, types.Departure{
		Validator: b.String(), EntityId: "acme", JoinedHeight: 10, LeftHeight: 20,
	})
	n, err = countDepartures(f)
	require.NoError(t, err)
	require.Equal(t, 3, n)
}

func countDepartures(f *fixture) (int, error) {
	n := 0
	err := f.k.Departures.Walk(f.ctx, nil, func(_ []byte, _ types.Departure) (bool, error) {
		n++
		return false, nil
	})
	return n, err
}

// TestJoiningAnotherEntityEndsTheCharge: a frozen seat stops being charged to
// its former entity when the validator joins another one.
func TestJoiningAnotherEntityEndsTheCharge(t *testing.T) {
	f := newFixture(t)
	v := val(1)
	f.at(10)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", v))
	require.NoError(t, err)
	f.reg.status[v.String()] = vrtypes.Status_STATUS_ACTIVE
	f.at(40)
	_, err = f.k.Revoke(f.ctx, f.attestor, "acme", "lapsed")
	require.NoError(t, err)
	f.at(45)
	_, err = f.k.Attest(f.ctx, f.attestor, att("beta", v))
	require.NoError(t, err)

	requireDeparture(t, f, v, types.Departure{
		Validator: v.String(), EntityId: "acme", JoinedHeight: 10, LeftHeight: 40, Frozen: true, ChargedUntil: 45,
	})
	requireEntityAt(t, f, v, 45, "acme")
	requireEntityAt(t, f, v, 46, "beta")
}

// TestJoiningTheSameEntityAgainEndsTheCharge: a frozen seat removed from acme
// and re-attested into acme also loses its row, so its charge ends there.
func TestJoiningTheSameEntityAgainEndsTheCharge(t *testing.T) {
	f := newFixture(t)
	v, w := val(1), val(2)
	f.at(10)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", v, w))
	require.NoError(t, err)
	f.reg.status[v.String()] = vrtypes.Status_STATUS_ACTIVE
	f.at(20)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", w))
	require.NoError(t, err)
	f.at(25)
	_, err = f.k.Attest(f.ctx, f.attestor, att("acme", v, w))
	require.NoError(t, err)

	requireDeparture(t, f, v, types.Departure{
		Validator: v.String(), EntityId: "acme", JoinedHeight: 10, LeftHeight: 20, Frozen: true, ChargedUntil: 25,
	})
	m, _, err := f.k.EntityOf(f.ctx, v)
	require.NoError(t, err)
	require.Equal(t, int64(25), m.JoinedHeight)
}

func TestHasEntity(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	_, err = f.k.Attest(f.ctx, f.attestor, att("beta", val(2)))
	require.NoError(t, err)
	_, err = f.k.Revoke(f.ctx, f.attestor, "beta", "lapsed")
	require.NoError(t, err)

	for _, tc := range []struct {
		id   string
		want bool
	}{{"acme", true}, {"beta", true}, {"nowhere", false}} {
		got, err := f.k.HasEntity(f.ctx, tc.id)
		require.NoError(t, err)
		require.Equal(t, tc.want, got, tc.id)
	}
}
