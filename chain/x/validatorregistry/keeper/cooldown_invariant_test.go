package keeper_test

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// seedExited writes an INACTIVE validator whose cooldown ends at completion.
// A zero completion means "nothing was recorded".
func seedExited(t *testing.T, f *fixture, tag string, completion time.Time) sdk.ValAddress {
	t.Helper()
	addr := seedValidator(t, f, tag, types.Status_STATUS_INACTIVE)
	md, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)
	md.UnbondingCompleteTime = completion
	require.NoError(t, f.keeper.ValidatorMetadata.Set(f.ctx, addr.Bytes(), md))
	return addr
}

func TestUnstakeCooldownInvariant(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

	withNow := func(t *testing.T) *fixture {
		f := initFixtureWithStaking(t, stubStakingKeeper{})
		f.ctx = sdk.UnwrapSDKContext(f.ctx).WithBlockTime(now)
		return f
	}

	// Cooldown finished before the exit -> clean.
	t.Run("cooldown_elapsed_ok", func(t *testing.T) {
		f := withNow(t)
		seedExited(t, f, "v5-inv-elapsed", now.Add(-time.Hour))

		_, broken := f.keeper.UnstakeCooldownInvariant(f.ctx)
		require.False(t, broken)
	})

	// Boundary: completion exactly now -> the cooldown HAS elapsed, not broken.
	t.Run("cooldown_boundary_ok", func(t *testing.T) {
		f := withNow(t)
		seedExited(t, f, "v5-inv-boundary", now)

		_, broken := f.keeper.UnstakeCooldownInvariant(f.ctx)
		require.False(t, broken)
	})

	// INACTIVE while the cooldown still runs -> broken, names the validator.
	t.Run("exited_early_broken", func(t *testing.T) {
		f := withNow(t)
		addr := seedExited(t, f, "v5-inv-early", now.Add(24*time.Hour))

		msg, broken := f.keeper.UnstakeCooldownInvariant(f.ctx)
		require.True(t, broken)
		require.Contains(t, msg, addr.String())
	})

	// Nothing recorded -> unverifiable, must NOT halt the chain.
	t.Run("zero_completion_is_skipped", func(t *testing.T) {
		f := withNow(t)
		seedExited(t, f, "v5-inv-zero", time.Time{})

		_, broken := f.keeper.UnstakeCooldownInvariant(f.ctx)
		require.False(t, broken, "an unverifiable exit must be skipped, never halt")
	})

	// A validator still unbonding is out of scope even with a future completion.
	t.Run("non_inactive_ignored", func(t *testing.T) {
		f := withNow(t)
		addr := seedValidator(t, f, "v5-inv-unbonding", types.Status_STATUS_UNBONDING)
		md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
		require.NoError(t, err)
		md.UnbondingCompleteTime = now.Add(24 * time.Hour)
		require.NoError(t, f.keeper.ValidatorMetadata.Set(f.ctx, addr.Bytes(), md))

		_, broken := f.keeper.UnstakeCooldownInvariant(f.ctx)
		require.False(t, broken, "only INACTIVE validators are in scope")
	})
}
