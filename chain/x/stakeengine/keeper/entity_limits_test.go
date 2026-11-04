package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"
)

func TestEntityLimitsInvariant(t *testing.T) {
	setup := func(t *testing.T, entityCap math.Int) *fixture {
		f := newFixture(t)
		f.caps(t)
		f.staking.addValidator(val(2), nscn(50_000))
		f.ent.members[string(val(1))] = "acme"
		f.ent.members[string(val(2))] = "acme"
		f.ent.seats["acme"] = 2
		st, err := f.k.CapState.Get(f.ctx)
		require.NoError(t, err)
		st.EntityCap = entityCap
		require.NoError(t, f.k.CapState.Set(f.ctx, st))
		return f
	}

	t.Run("within limits", func(t *testing.T) {
		f := setup(t, nscn(1_050_000)) // exactly the aggregate
		msg, broken := f.k.EntityLimitsInvariant(f.ctx)
		require.False(t, broken, msg)
	})
	t.Run("no entities at all", func(t *testing.T) {
		f := newFixture(t)
		f.caps(t)
		msg, broken := f.k.EntityLimitsInvariant(f.ctx)
		require.False(t, broken, msg)
	})
	t.Run("more active seats than the limit", func(t *testing.T) {
		f := setup(t, nscn(2_000_000))
		f.ent.seats["acme"] = 3
		msg, broken := f.k.EntityLimitsInvariant(f.ctx)
		require.True(t, broken)
		require.Contains(t, msg, "acme")
		require.Contains(t, msg, "active seats")
	})
	t.Run("aggregate above the entity cap", func(t *testing.T) {
		f := setup(t, nscn(1_049_999))
		msg, broken := f.k.EntityLimitsInvariant(f.ctx)
		require.True(t, broken)
		require.Contains(t, msg, "acme")
		require.Contains(t, msg, "aggregate stake")
	})
}
