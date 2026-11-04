package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func TestStandingOfAValidatorWithNoEntity(t *testing.T) {
	f := newFixture(t)
	f.caps(t)
	s, err := f.k.EntityStanding(f.ctx, val(1))
	require.NoError(t, err)
	require.Empty(t, s.EntityID)
	require.False(t, s.Frozen)
	require.True(t, s.Tokens.IsZero())
	require.True(t, s.Cap.IsZero())
}

func TestStandingOfAFrozenSeat(t *testing.T) {
	f := newFixture(t)
	f.caps(t)
	f.ent.frozen[string(val(1))] = true
	s, err := f.k.EntityStanding(f.ctx, val(1))
	require.NoError(t, err)
	require.Empty(t, s.EntityID)
	require.True(t, s.Frozen)
	require.Contains(t, s.Reason, "active seat without a live attestation")
	require.True(t, s.RoomFor(nscn(100)).IsZero())
}

func TestStandingSumsEveryMembersTokens(t *testing.T) {
	f := newFixture(t)
	st := f.caps(t)
	f.staking.addValidator(val(2), nscn(50_000))
	v := f.staking.validators[string(val(2))]
	v.Status = stakingtypes.Unbonded // an unbonded member still holds its tokens against the cap
	f.staking.validators[string(val(2))] = v
	f.ent.members[string(val(1))] = "acme"
	f.ent.members[string(val(2))] = "acme"

	for _, member := range []byte{1, 2} {
		s, err := f.k.EntityStanding(f.ctx, val(member))
		require.NoError(t, err)
		require.Equal(t, "acme", s.EntityID)
		require.False(t, s.Frozen)
		require.Equal(t, nscn(1_050_000), s.Tokens)
		require.Equal(t, st.EntityCap, s.Cap)
	}
}

// A frozen seat whose validator is still ACTIVE is charged to its former
// entity, so its tokens count in the entity's aggregate (spec §8.1).
func TestStandingChargesTheFrozenSeatsOfTheEntity(t *testing.T) {
	f := newFixture(t)
	f.caps(t)
	f.staking.addValidator(val(2), nscn(50_000))
	f.staking.addValidator(val(3), nscn(70_000))
	f.ent.members[string(val(1))] = "acme"
	f.ent.frozen[string(val(2))] = true
	f.ent.charged["acme"] = []sdk.ValAddress{val(2)}
	f.ent.frozen[string(val(3))] = true
	f.ent.charged["beta"] = []sdk.ValAddress{val(3)}

	s, err := f.k.EntityStanding(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, "acme", s.EntityID)
	require.Equal(t, nscn(1_050_000), s.Tokens, "1 and the frozen 2; never beta's 3")

	// The frozen validator's own standing stays frozen, with no entity.
	s, err = f.k.EntityStanding(f.ctx, val(2))
	require.NoError(t, err)
	require.Empty(t, s.EntityID)
	require.True(t, s.Frozen)
}

func TestStandingFreezesAnEntityOverItsSeats(t *testing.T) {
	f := newFixture(t)
	f.caps(t)
	f.ent.members[string(val(1))] = "acme"
	f.ent.seats["acme"] = 3
	f.ent.max = 2
	s, err := f.k.EntityStanding(f.ctx, val(1))
	require.NoError(t, err)
	require.True(t, s.Frozen)
	require.Equal(t, "entity acme holds 3 active seats, above the limit of 2", s.Reason)
	require.True(t, s.RoomFor(nscn(100)).IsZero())

	f.ent.seats["acme"] = 2
	s, err = f.k.EntityStanding(f.ctx, val(1))
	require.NoError(t, err)
	require.False(t, s.Frozen, "at the limit is not over it")
}

func TestStandingBeforeTheCapsExistIsAnError(t *testing.T) {
	f := newFixture(t)
	f.ent.members[string(val(1))] = "acme"
	_, err := f.k.EntityStanding(f.ctx, val(1))
	require.ErrorIs(t, err, types.ErrCapsNotComputed)
}

func TestRecomputeCapsWritesTheEntityCap(t *testing.T) {
	f := newFixture(t)
	f.setParams(t, func(p *types.Params) { p.EntityCapFloor = nscn(10) })
	st := f.caps(t)
	params, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.EntityCap(params, st.EwmaBonded), st.EntityCap)
	require.Equal(t, nscn(50_000), st.EntityCap) // 5% of the 1,000,000 SCN EWMA
	stored, err := f.k.CapState.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, st, stored)
	got, err := f.k.EntityCap(f.ctx)
	require.NoError(t, err)
	require.Equal(t, st.EntityCap, got)
}

func TestEntityCapReadsTheStoredValueAndNeverBackfills(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.CapState.Set(f.ctx, types.CapState{
		PublicCirculating: math.ZeroInt(), GlobalCap: math.ZeroInt(), ValidatorCap: math.ZeroInt(),
		EwmaBonded: math.LegacyNewDecFromInt(nscn(1_000_000)), EntityCap: math.ZeroInt(),
	}))
	got, err := f.k.EntityCap(f.ctx)
	require.NoError(t, err)
	require.True(t, got.IsZero(), "a read never differs from the stored state")
}
