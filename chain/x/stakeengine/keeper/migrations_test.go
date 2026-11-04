package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/keeper"
	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func TestMigrate1to2(t *testing.T) {
	f := newFixture(t)
	// A v0.9.x record has no fields 12, 13 or 6; a non-nullable custom type is
	// always written, so zero stands in for the absent value, and the migration
	// treats nil and zero alike.
	p := types.DefaultParams()
	p.EntityCapFraction = math.LegacyZeroDec()
	p.EntityCapFloor = math.ZeroInt()
	p.ValidatorCapFloor = nscn(2_000_000)
	p.MinQueueEntry = math.NewInt(1e8)
	require.NoError(t, f.k.Params.Set(f.ctx, p))
	before := types.CapState{
		Epoch:             7,
		PublicCirculating: nscn(5_000_000),
		GlobalCap:         nscn(1_680_000),
		EwmaBonded:        math.LegacyNewDecFromInt(nscn(1_000_000)),
		ValidatorCap:      nscn(2_000_000),
		EntityCap:         math.ZeroInt(),
	}
	require.NoError(t, f.k.CapState.Set(f.ctx, before))

	require.NoError(t, keeper.NewMigrator(f.k).Migrate1to2(f.ctx))

	got, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, math.LegacyNewDecWithPrec(5, 2), got.EntityCapFraction)
	require.Equal(t, nscn(2_000_000), got.EntityCapFloor)
	require.NoError(t, got.Validate())

	st, err := f.k.CapState.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, nscn(2_000_000), st.EntityCap) // max(5% of 1,000,000 SCN, the floor)
	require.Equal(t, before.EwmaBonded, st.EwmaBonded, "the EWMA is not advanced")
	require.Equal(t, before.Epoch, st.Epoch)
	require.Equal(t, before.ValidatorCap, st.ValidatorCap)
	require.Equal(t, before.GlobalCap, st.GlobalCap)
}

func TestMigrate1to2WithoutACapState(t *testing.T) {
	f := newFixture(t)
	p := types.DefaultParams()
	p.EntityCapFraction = math.LegacyZeroDec()
	p.EntityCapFloor = math.ZeroInt()
	require.NoError(t, f.k.Params.Set(f.ctx, p))
	require.NoError(t, keeper.NewMigrator(f.k).Migrate1to2(f.ctx))
	got, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	require.NoError(t, got.Validate())
	_, err = f.k.CapState.Get(f.ctx)
	require.Error(t, err, "the migration does not invent a snapshot")
}
