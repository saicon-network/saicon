package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func TestMigrate1to2(t *testing.T) {
	f := initFixture(t)
	ctx := sdk.UnwrapSDKContext(f.ctx)

	// A v0.9.1 store: a cooldown, no new fields and no sanction sequence.
	require.NoError(t, f.keeper.Params.Set(ctx, types.Params{UnjailCooldownBlocks: 1234}))
	before, err := f.keeper.SanctionSeq.Peek(ctx)
	require.NoError(t, err)
	require.Zero(t, before)

	require.NoError(t, keeper.NewMigrator(f.keeper).Migrate1to2(ctx))

	got, err := f.keeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, types.NewParams(1234), got)
	require.Equal(t, "0.300000000000000000", got.CoordinatedAttackFraction.String())
	require.Equal(t, uint64(604_800), got.AppealWindowSeconds)

	seq, err := f.keeper.SanctionSeq.Peek(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), seq)
	first, err := f.keeper.SanctionSeq.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), first, "the first sanction is 1")
}

func TestMigrate1to2RefusesAnInvalidCooldownAndWritesNothing(t *testing.T) {
	f := initFixture(t)
	ctx := sdk.UnwrapSDKContext(f.ctx)

	stored := types.Params{UnjailCooldownBlocks: -5}
	require.NoError(t, f.keeper.Params.Set(ctx, stored))

	require.Error(t, keeper.NewMigrator(f.keeper).Migrate1to2(ctx))

	got, err := f.keeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(-5), got.UnjailCooldownBlocks)
	require.True(t, got.CoordinatedAttackFraction.IsNil() || got.CoordinatedAttackFraction.IsZero(), "params unchanged")
	require.Zero(t, got.AppealWindowSeconds)
	seq, err := f.keeper.SanctionSeq.Peek(ctx)
	require.NoError(t, err)
	require.Zero(t, seq, "the sequence is untouched")
}
