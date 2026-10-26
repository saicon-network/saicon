package keeper_test

// NOTE: this file was originally `package keeper` (white-box) when it only
// tested the pure helpers LockupExpiry/IsLocked. TestSelfStakeLockup needs
// the shared black-box fixture (initFixture) defined in keeper_test.go, and
// Go does not allow a `package keeper` file to see identifiers from
// `package keeper_test` files in the same directory. Converted to
// `package keeper_test` (matching the rest of the suite) and qualified the
// two pure-helper calls with `keeper.` accordingly; behavior/assertions are
// unchanged.

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/keeper"
	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestIsLocked(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dur := int64(180 * 24 * 3600) // 180 days
	expiry := start.Add(time.Duration(dur) * time.Second)

	require.True(t, keeper.IsLocked(start, dur, start))                    // at bonding: locked
	require.True(t, keeper.IsLocked(start, dur, expiry.Add(-time.Second))) // 1s before expiry: locked
	require.False(t, keeper.IsLocked(start, dur, expiry))                  // exactly at expiry: unlocked
	require.False(t, keeper.IsLocked(start, dur, expiry.Add(time.Second))) // after expiry: unlocked
	require.False(t, keeper.IsLocked(time.Time{}, dur, start))             // zero bonding_start: not tracked
	require.False(t, keeper.IsLocked(start, 0, start))                     // zero duration: not tracked
	require.False(t, keeper.IsLocked(start, -5, start))                    // negative duration: not tracked

	require.Equal(t, expiry, keeper.LockupExpiry(start, dur))
	require.True(t, keeper.LockupExpiry(time.Time{}, dur).IsZero())
	require.True(t, keeper.LockupExpiry(start, 0).IsZero())
}

// A record stored before the registration cap may carry a duration that overflows
// when multiplied into nanoseconds. The expiry saturates at the longest
// representable lock-up instead of wrapping into the past, so such a record stays
// locked; a long but representable lock-up is honoured exactly.
func TestLockupExpirySaturatesInsteadOfOverflowing(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	longest := start.Add(time.Duration(types.MaxRepresentableLockupSeconds) * time.Second)

	require.Equal(t, longest, keeper.LockupExpiry(start, types.MaxRepresentableLockupSeconds))
	require.Equal(t, longest, keeper.LockupExpiry(start, types.MaxRepresentableLockupSeconds+1))
	require.Equal(t, longest, keeper.LockupExpiry(start, 1<<62))
	require.True(t, keeper.IsLocked(start, types.MaxRepresentableLockupSeconds+1, start.Add(100*365*24*time.Hour)))

	twentyYears := int64(20 * 365 * 24 * 60 * 60)
	require.Equal(t, start.Add(time.Duration(twentyYears)*time.Second), keeper.LockupExpiry(start, twentyYears),
		"a stored lock-up above the registration cap but representable keeps its full length")
}

// TestSelfStakeLockup covers Keeper.SelfStakeLockup: a metadata read + block-time
// wrapper around the pure IsLocked/LockupExpiry helpers above.
func TestSelfStakeLockup(t *testing.T) {
	blockTime := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	const lockupDurationSeconds = int64(180 * 24 * 3600) // 180 days

	t.Run("NoMetadata_NotLockedNoError", func(t *testing.T) {
		f := initFixture(t)
		sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)
		addr := sdk.ValAddress([]byte("validator-ghost-padding-bytes!!"))[:20]

		locked, unlockAt, err := f.keeper.SelfStakeLockup(sdkCtx, addr)
		require.NoError(t, err)
		require.False(t, locked, "validator with no metadata must not be reported as locked")
		require.True(t, unlockAt.IsZero(), "unlockAt must be zero when there is no metadata")
	})

	t.Run("BondingStartsNow_LockedUntilBondingStartPlusDuration", func(t *testing.T) {
		f := initFixture(t)
		sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)
		addr := sdk.ValAddress([]byte("validator-locked-padding-bytes!"))[:20]
		addrStr, err := f.addressCodec.BytesToString(addr)
		require.NoError(t, err)

		md := types.ValidatorMetadata{
			ValidatorAddress:      addrStr,
			Status:                types.Status_STATUS_ACTIVE,
			BondingStart:          blockTime,
			LockupDurationSeconds: lockupDurationSeconds,
		}
		require.NoError(t, f.keeper.SetValidatorMetadata(sdkCtx, addr, md))

		locked, unlockAt, err := f.keeper.SelfStakeLockup(sdkCtx, addr)
		require.NoError(t, err)
		require.True(t, locked, "validator bonded exactly at block time must still be locked")
		require.Equal(t, blockTime.Add(time.Duration(lockupDurationSeconds)*time.Second), unlockAt)
	})

	t.Run("LockupExpired_NotLocked", func(t *testing.T) {
		f := initFixture(t)
		sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)
		addr := sdk.ValAddress([]byte("validator-expired-padding-bytes"))[:20]
		addrStr, err := f.addressCodec.BytesToString(addr)
		require.NoError(t, err)

		bondingStart := blockTime.Add(-200 * 24 * time.Hour) // 200 days ago: past the 180-day window
		md := types.ValidatorMetadata{
			ValidatorAddress:      addrStr,
			Status:                types.Status_STATUS_ACTIVE,
			BondingStart:          bondingStart,
			LockupDurationSeconds: lockupDurationSeconds,
		}
		require.NoError(t, f.keeper.SetValidatorMetadata(sdkCtx, addr, md))

		locked, unlockAt, err := f.keeper.SelfStakeLockup(sdkCtx, addr)
		require.NoError(t, err)
		require.False(t, locked, "lock-up window elapsed 20 days ago; must not be locked")
		require.Equal(t, bondingStart.Add(time.Duration(lockupDurationSeconds)*time.Second), unlockAt)
	})
}

// TestDefaultLockupIsSixtyDays pins the number itself, not the behaviour.
//
// Every other test in this package asserts against
// types.DefaultLockupDurationSeconds symbolically, so they follow the constant
// wherever it goes and would stay green if someone changed it. That is the right
// shape for behaviour, and the wrong shape for a figure the white paper, the
// lite paper and the public roadmap all publish: a silent edit here would make
// those documents false without failing anything.
//
// Lowered from 180 days by project decision on 2026-09-22. Moving it again is a
// deliberate act: change this test in the same commit, and the published
// documents with it.
func TestDefaultLockupIsSixtyDays(t *testing.T) {
	const sixtyDays = int64(60 * 24 * 60 * 60)
	require.Equal(t, sixtyDays, types.DefaultLockupDurationSeconds,
		"default lock-up is a published protocol parameter; update the papers, the Register and the site in the same commit")
}
