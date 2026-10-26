package keeper

import (
	"context"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// LockupExpiry returns the instant the self-stake lock-up ends
// (bonding_start + lockup_duration_seconds). Returns the zero time when the
// window is not tracked (no bonding_start, or non-positive duration).
//
// A duration too long to express in nanoseconds is read as the longest that can
// be (MaxRepresentableLockupSeconds, ~292 years). Registration refuses anything
// above ten years, but a record stored before that cap may carry such a value, and
// multiplying it into a time.Duration would overflow and put the expiry in the
// past. Saturating keeps that record locked; every representable value, including
// an honest lock-up longer than ten years, is honoured exactly as written.
func LockupExpiry(bondingStart time.Time, lockupDurationSeconds int64) time.Time {
	if bondingStart.IsZero() || lockupDurationSeconds <= 0 {
		return time.Time{}
	}
	if lockupDurationSeconds > types.MaxRepresentableLockupSeconds {
		lockupDurationSeconds = types.MaxRepresentableLockupSeconds
	}
	return bondingStart.Add(time.Duration(lockupDurationSeconds) * time.Second)
}

// IsLocked reports whether `now` is strictly before the lock-up expiry.
// Not locked when the window is untracked or `now` is at/after expiry.
func IsLocked(bondingStart time.Time, lockupDurationSeconds int64, now time.Time) bool {
	expiry := LockupExpiry(bondingStart, lockupDurationSeconds)
	if expiry.IsZero() {
		return false
	}
	return now.Before(expiry)
}

// SelfStakeLockup reports whether the validator's self-stake is currently locked
// (bonding_start + lockup_duration_seconds not yet reached), and when it unlocks.
// A validator with no metadata is treated as not locked (unlockAt zero).
func (k Keeper) SelfStakeLockup(ctx context.Context, valAddr sdk.ValAddress) (locked bool, unlockAt time.Time, err error) {
	md, found, err := k.GetValidatorMetadata(ctx, valAddr)
	if err != nil {
		return false, time.Time{}, err
	}
	if !found {
		return false, time.Time{}, nil
	}
	now := sdk.UnwrapSDKContext(ctx).BlockTime()
	unlockAt = LockupExpiry(md.BondingStart, md.LockupDurationSeconds)
	return IsLocked(md.BondingStart, md.LockupDurationSeconds, now), unlockAt, nil
}
