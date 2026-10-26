package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/comet"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// RecordEpochVotes tallies one block's precommit votes into the current-epoch
// per-validator counters. For each vote: resolve the consensus address to a
// validator operator via the staking keeper (skip + debug-log if unknown), then
// bump EpochBlocksSigned if the validator committed, else EpochBlocksMissed.
func (k Keeper) RecordEpochVotes(ctx context.Context, votes []comet.VoteInfo) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	for _, v := range votes {
		consAddr := sdk.ConsAddress(v.Validator().Address())

		val, err := k.stakingKeeper.GetValidatorByConsAddr(ctx, consAddr)
		if err != nil {
			sdkCtx.Logger().Debug(
				"RecordEpochVotes: validator not found by cons addr, skipping",
				"cons_addr", consAddr.String(),
				"error", err,
			)
			continue
		}

		valAddr, err := sdk.ValAddressFromBech32(val.GetOperator())
		if err != nil {
			sdkCtx.Logger().Debug(
				"RecordEpochVotes: invalid operator address, skipping",
				"operator", val.GetOperator(),
				"error", err,
			)
			continue
		}

		if v.GetBlockIDFlag() == comet.BlockIDFlagCommit {
			if err := incr(ctx, k.EpochBlocksSigned, valAddr.Bytes()); err != nil {
				return err
			}
		} else {
			if err := incr(ctx, k.EpochBlocksMissed, valAddr.Bytes()); err != nil {
				return err
			}
		}
	}
	return nil
}

// incr atomically increments a uint64 counter in the given Map for the
// provided key. A missing entry (ErrNotFound) is treated as 0.
func incr(ctx context.Context, m collections.Map[[]byte, uint64], addr []byte) error {
	cur, err := m.Get(ctx, addr)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		cur = 0
	}
	return m.Set(ctx, addr, cur+1)
}

// epochUptime returns the current-epoch uptime fraction for a validator:
// signed/(signed+missed), clamped to [0,1]. If no blocks have been observed
// for the validator this epoch (total==0), returns 1 (benefit of the doubt —
// the validator was not yet in the set; do not penalize).
func (k Keeper) epochUptime(ctx context.Context, valAddr sdk.ValAddress) (uptime math.LegacyDec, signed, missed uint64, err error) {
	signed, err = k.EpochBlocksSigned.Get(ctx, valAddr.Bytes())
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return math.LegacyZeroDec(), 0, 0, err
		}
		signed = 0
	}

	missed, err = k.EpochBlocksMissed.Get(ctx, valAddr.Bytes())
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return math.LegacyZeroDec(), 0, 0, err
		}
		missed = 0
	}

	total := signed + missed
	if total == 0 {
		// Benefit of the doubt: validator was not yet in the active set.
		return math.LegacyOneDec(), 0, 0, nil
	}

	uptime = math.LegacyNewDec(int64(signed)).Quo(math.LegacyNewDec(int64(total)))
	// Defensive clamp to [0,1].
	if uptime.IsNegative() {
		uptime = math.LegacyZeroDec()
	}
	if uptime.GT(math.LegacyOneDec()) {
		uptime = math.LegacyOneDec()
	}
	return uptime, signed, missed, nil
}

// ResetEpochCounters clears both per-validator epoch tallies. Called at the end
// of ComputeEpoch so the next epoch starts from zero.
func (k Keeper) ResetEpochCounters(ctx context.Context) error {
	if err := k.EpochBlocksSigned.Clear(ctx, nil); err != nil {
		return err
	}
	return k.EpochBlocksMissed.Clear(ctx, nil)
}
