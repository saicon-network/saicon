package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/participation/types"
)

func TestSetAndGetSnapshot(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("participation-val-20!"))[:20]

	snap := types.ParticipationScoreSnapshot{
		ValidatorAddress:  addr.String(),
		EpochId:           7,
		BlocksSigned:      95,
		BlocksMissed:      5,
		VotesParticipated: 90,
		UptimeFraction:    math.LegacyNewDecWithPrec(95, 2),
		ComputedScore:     math.LegacyNewDecWithPrec(95, 2),
		ComputedAt:        time.Now().UTC(),
	}

	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 7, addr, snap))

	got, found, err := f.keeper.GetSnapshot(f.ctx, 7, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(95), got.BlocksSigned)
	require.Equal(t, "0.950000000000000000", got.UptimeFraction.String())

	// A different epoch for the same validator is not found.
	_, found, err = f.keeper.GetSnapshot(f.ctx, 8, addr)
	require.NoError(t, err)
	require.False(t, found)
}

func TestSnapshot_MultiEpochAndOverwrite(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("part-multiepoch-20by"))[:20]

	snap7 := types.ParticipationScoreSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          7,
		BlocksSigned:     90,
		UptimeFraction:   math.LegacyNewDecWithPrec(90, 2),
		ComputedScore:    math.LegacyNewDecWithPrec(90, 2),
		ComputedAt:       time.Now().UTC(),
	}
	snap8 := types.ParticipationScoreSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          8,
		BlocksSigned:     80,
		UptimeFraction:   math.LegacyNewDecWithPrec(80, 2),
		ComputedScore:    math.LegacyNewDecWithPrec(80, 2),
		ComputedAt:       time.Now().UTC(),
	}

	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 7, addr, snap7))
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 8, addr, snap8))

	// Both epochs coexist independently for the same validator.
	got7, found, err := f.keeper.GetSnapshot(f.ctx, 7, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(90), got7.BlocksSigned)

	got8, found, err := f.keeper.GetSnapshot(f.ctx, 8, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(80), got8.BlocksSigned)

	// Overwriting (7, addr) replaces the value rather than erroring or duplicating.
	snap7.BlocksSigned = 99
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 7, addr, snap7))

	got7, found, err = f.keeper.GetSnapshot(f.ctx, 7, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(99), got7.BlocksSigned)

	// Epoch 8 is unaffected by the epoch 7 overwrite.
	got8, found, err = f.keeper.GetSnapshot(f.ctx, 8, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(80), got8.BlocksSigned)
}

func TestSetAndGetEP(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("participation-ep-20b!"))[:20]

	require.NoError(t, f.keeper.SetEP(f.ctx, addr, types.EmpiricalPointsState{
		ValidatorAddress: addr.String(),
		AccumulatedEp:    math.NewInt(42),
		LastEpochUpdate:  7,
	}))

	got, found, err := f.keeper.GetEP(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(42), got.AccumulatedEp.Int64())
	require.Equal(t, uint64(7), got.LastEpochUpdate)

	// An absent validator is not found.
	absent := sdk.ValAddress([]byte("participation-absent!"))[:20]
	_, found, err = f.keeper.GetEP(f.ctx, absent)
	require.NoError(t, err)
	require.False(t, found)
}
