package keeper_test

import (
	"fmt"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// cleanFixture holds acme {1,2} and beta {3} attested, val(1) ACTIVE, and gamma
// revoked with its ACTIVE member val(4) frozen: V-12 holds.
func cleanFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	for _, a := range []types.Attestation{att("acme", val(1), val(2)), att("beta", val(3)), att("gamma", val(4))} {
		_, err := f.k.Attest(f.ctx, f.attestor, a)
		require.NoError(t, err)
	}
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(4).String()] = vrtypes.Status_STATUS_ACTIVE
	_, err := f.k.Revoke(f.ctx, f.attestor, "gamma", "lapsed")
	require.NoError(t, err)
	return f
}

func TestRegistryInvariantHoldsOnACleanState(t *testing.T) {
	f := cleanFixture(t)
	msg, broken := f.k.RegistryInvariant(f.ctx)
	require.False(t, broken, msg)
}

func TestRegistryInvariantNamesEachBrokenClause(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(t *testing.T, f *fixture)
		want   string
	}{
		{"member without its by-entity row", func(t *testing.T, f *fixture) {
			require.NoError(t, f.k.Members.Set(f.ctx, val(9), types.Member{Validator: val(9).String(), EntityId: "acme"}))
		}, "has no MembersByEntity row"},
		{"by-entity row without its member", func(t *testing.T, f *fixture) {
			require.NoError(t, f.k.MembersByEntity.Set(f.ctx, collections.Join("acme", []byte(val(9)))))
		}, "has no Members row"},
		{"by-entity row names another entity", func(t *testing.T, f *fixture) {
			require.NoError(t, f.k.MembersByEntity.Set(f.ctx, collections.Join("beta", []byte(val(1)))))
		}, "Members row says"},
		{"member of a revoked entity", func(t *testing.T, f *fixture) {
			e, err := f.k.Entities.Get(f.ctx, "beta")
			require.NoError(t, err)
			e.Status = types.EntityStatus_ENTITY_STATUS_REVOKED
			require.NoError(t, f.k.Entities.Set(f.ctx, "beta", e))
		}, "is not attested"},
		{"member of an unknown entity", func(t *testing.T, f *fixture) {
			require.NoError(t, f.k.Entities.Remove(f.ctx, "beta"))
		}, "does not exist"},
		{"attested entity without a member", func(t *testing.T, f *fixture) {
			require.NoError(t, f.k.Entities.Set(f.ctx, "ghost", types.Entity{
				Id: "ghost", Status: types.EntityStatus_ENTITY_STATUS_ATTESTED,
			}))
		}, "entity ghost has 0 members"},
		{"attested entity over the member limit", func(t *testing.T, f *fixture) {
			for i := 0; i <= types.MaxMembersPerEntity; i++ {
				v := val(byte(50 + i))
				require.NoError(t, f.k.Members.Set(f.ctx, v, types.Member{Validator: v.String(), EntityId: "beta"}))
				require.NoError(t, f.k.MembersByEntity.Set(f.ctx, collections.Join("beta", []byte(v))))
			}
		}, "entity beta has 10 members"},
		{"frozen and member", func(t *testing.T, f *fixture) {
			require.NoError(t, f.k.Frozen.Set(f.ctx, val(1), types.FrozenSeat{Validator: val(1).String(), FormerEntityId: "acme"}))
		}, "is both a member and frozen"},
		{"active seat without an attestation", func(t *testing.T, f *fixture) {
			f.reg.status[val(9).String()] = vrtypes.Status_STATUS_ACTIVE
		}, "holds an active seat without an attestation"},
		{"a registry read error", func(t *testing.T, f *fixture) {
			f.reg.failIterate = true
		}, "iteration error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := cleanFixture(t)
			c.break_(t, f)
			msg, broken := f.k.RegistryInvariant(f.ctx)
			require.True(t, broken)
			require.Contains(t, msg, "entityregistry V-12 broken")
			require.Contains(t, msg, c.want)
		})
	}
}

func TestRegistryInvariantListsEveryViolation(t *testing.T) {
	f := cleanFixture(t)
	f.reg.status[val(8).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(9).String()] = vrtypes.Status_STATUS_ACTIVE
	msg, broken := f.k.RegistryInvariant(f.ctx)
	require.True(t, broken)
	require.Contains(t, msg, fmt.Sprintf("validator %s holds", sdk.ValAddress(val(8))))
	require.Contains(t, msg, fmt.Sprintf("validator %s holds", sdk.ValAddress(val(9))))
}

func TestEndBlockPanicsOnV12(t *testing.T) {
	f := cleanFixture(t)
	f.reg.status[val(9).String()] = vrtypes.Status_STATUS_ACTIVE
	require.Panics(t, func() { _ = f.k.EndBlock(f.ctx) })
}
