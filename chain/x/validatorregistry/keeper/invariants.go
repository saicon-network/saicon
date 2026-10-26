package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// ValidatorStatusInvariant implements invariant V-1
// (chain/docs/validators/README.md §8): every stored validator must carry a
// valid status (never STATUS_UNSPECIFIED).
//
// It is a pure, side-effect-free scan reused by unit tests and by the module
// EndBlocker. Returns (message, broken). broken == true indicates a violation.
//
// V-1's transition correctness is enforced at the gate (Keeper.ChangeStatus);
// this invariant is defense-in-depth that catches direct-Set corruption.
func (k Keeper) ValidatorStatusInvariant(ctx context.Context) (string, bool) {
	var (
		broken bool
		count  int
		detail string
	)

	err := k.IterateValidatorMetadata(ctx, func(addr sdk.ValAddress, md types.ValidatorMetadata) bool {
		if !md.Status.IsValid() {
			broken = true
			count++
			detail += fmt.Sprintf("  validator %s has invalid status %d\n", addr.String(), md.Status)
		}
		return false
	})
	if err != nil {
		return fmt.Sprintf("validatorregistry V-1 invariant: iteration error: %v", err), true
	}

	if broken {
		return fmt.Sprintf("validatorregistry V-1 invariant broken: %d validator(s) with invalid status\n%s", count, detail), true
	}
	return "validatorregistry V-1 invariant: all validator statuses valid", false
}

// SelfStakeLockupInvariant implements invariant V-3
// (chain/docs/validators/README.md §8): no validator still inside its self-stake
// lock-up window may have a self-stake exit that was INITIATED while locked — a
// self-unbonding (MsgUndelegate) or a self-redelegation moving stake to another
// validator (MsgBeginRedelegate). The ante decorator prevents both per-tx; this
// is fail-loud defense-in-depth (a bypass — e.g. a gov/group-executed message
// that never passes through ante — halts the chain rather than being silently
// accepted). Redelegation is a distinct escape: it creates a Redelegation, not an
// UnbondingDelegation, so it is checked separately.
//
// CREATION-TIME GUARD: we flag only exit *entries* whose CreationHeight is at or
// after md.BondingStartHeight (the block the lock-up began). An exit created
// BEFORE the validator ever locked — e.g. a validator that self-undelegated while
// still unbonded (no metadata, ante correctly allowed it) and only later bonded —
// legitimately lingers into the window (unbonding takes ~21d); flagging it on mere
// presence would panic-halt the whole chain on a legitimately-allowed action.
//
// KNOWN LIMITATION (see design doc): an *instant* self-redelegation whose source
// validator is already Unbonded completes immediately in x/staking and creates NO
// Redelegation object, so this object-scan cannot see it. That sub-case is covered
// only by the ante (which blocks it pre-execution); it is not reachable by any
// current on-chain path (gov/group cannot sign a MsgBeginRedelegate for a
// validator's own self-stake), so it is a latent gap for hypothetical future
// non-ante dispatchers, not a live escape.
func (k Keeper) SelfStakeLockupInvariant(ctx context.Context) (string, bool) {
	var (
		broken bool
		count  int
		detail string
	)
	now := sdk.UnwrapSDKContext(ctx).BlockTime()

	err := k.IterateValidatorMetadata(ctx, func(addr sdk.ValAddress, md types.ValidatorMetadata) bool {
		if !IsLocked(md.BondingStart, md.LockupDurationSeconds, now) {
			return false
		}
		selfAcc := sdk.AccAddress(addr.Bytes())
		lockStart := md.BondingStartHeight

		// FAIL-SAFE: a non-positive lock start is unverifiable, and comparing
		// against it would flag EVERY exit entry — CreationHeight >= 0 always
		// holds — turning this fail-loud check into a false-positive network-wide
		// halt. Zero is reachable both as the proto3 default of an unset field and
		// as the height reported during InitChain (baseapp treats the init-chain
		// height as 0), so it means "not recorded", not "block zero". Skip rather
		// than halt: the ante is the primary enforcement, this invariant is
		// defense-in-depth, and the V-3 lesson is never to halt on state we cannot
		// verify. New records avoid the ambiguity entirely via types.LockStartHeight.
		if lockStart <= 0 {
			return false
		}

		// (i) self-unbonding with an entry created at/after the lock started?
		ubd, e := k.stakingKeeper.GetUnbondingDelegation(ctx, selfAcc, addr)
		if e == nil {
			if anyUBDEntryAtOrAfter(ubd.Entries, lockStart) {
				broken = true
				count++
				detail += fmt.Sprintf("  validator %s has a self-unbonding created while locked\n", addr.String())
			}
		} else if !errors.Is(e, stakingtypes.ErrNoUnbondingDelegation) {
			broken = true
			count++
			detail += fmt.Sprintf("  validator %s unbonding check errored: %v\n", addr.String(), e)
		}

		// (ii) self-redelegation moving self-stake away, entry created while locked?
		// All returned redelegations are sourced from addr; flag any whose
		// delegator is the validator's own account (self-stake).
		reds, e := k.stakingKeeper.GetRedelegationsFromSrcValidator(ctx, addr)
		if e != nil {
			broken = true
			count++
			detail += fmt.Sprintf("  validator %s redelegation check errored: %v\n", addr.String(), e)
		} else {
			for _, red := range reds {
				delAcc, derr := sdk.AccAddressFromBech32(red.DelegatorAddress)
				if derr == nil && bytes.Equal(delAcc.Bytes(), addr.Bytes()) && anyREDEntryAtOrAfter(red.Entries, lockStart) {
					broken = true
					count++
					detail += fmt.Sprintf("  validator %s has a self-redelegation created while locked\n", addr.String())
					break
				}
			}
		}
		return false
	})
	if err != nil {
		return fmt.Sprintf("validatorregistry V-3 invariant: iteration error: %v", err), true
	}
	if broken {
		return fmt.Sprintf("validatorregistry V-3 invariant broken: %d locked validator(s) exiting self-stake (unbond/redelegate)\n%s", count, detail), true
	}
	return "validatorregistry V-3 invariant: no locked validator is unbonding or redelegating self-stake", false
}

// anyUBDEntryAtOrAfter reports whether any unbonding-delegation entry was created
// at or after the given block height (the lock-up start). Entries created earlier
// predate the lock and are legitimate.
func anyUBDEntryAtOrAfter(entries []stakingtypes.UnbondingDelegationEntry, height int64) bool {
	for _, e := range entries {
		if e.CreationHeight >= height {
			return true
		}
	}
	return false
}

// anyREDEntryAtOrAfter reports whether any redelegation entry was created at or
// after the given block height (the lock-up start).
func anyREDEntryAtOrAfter(entries []stakingtypes.RedelegationEntry, height int64) bool {
	for _, e := range entries {
		if e.CreationHeight >= height {
			return true
		}
	}
	return false
}

// UnstakeCooldownInvariant implements invariant V-5
// (chain/docs/validators/README.md §8): no validator reaches STATUS_INACTIVE
// before its unstake cooldown has fully elapsed.
//
// The cooldown holds by construction — INACTIVE is only ever set from the staking
// AfterValidatorRemoved hook, and staking refuses to remove a validator that still
// holds tokens, i.e. removal implies the unbonding period matured. This invariant
// is the fail-loud backstop for that guarantee (staking merely LOGS an error
// returned by the hook, so a broken exit path would otherwise be silent).
//
// The comparison uses the completion instant FROZEN at begin-unbonding
// (unbonding_complete_time), not a recomputed one: a later governance change of the
// staking UnbondingTime parameter must not retroactively turn a legitimate exit
// into a violation.
//
// Fail-safe: a validator whose completion instant is zero (nothing was ever
// recorded — e.g. it never passed through begin-unbonding) is SKIPPED rather than
// flagged. Halting the chain on state we cannot verify would be worse than the gap
// it guards (the V-3 false-positive-halt lesson).
func (k Keeper) UnstakeCooldownInvariant(ctx context.Context) (string, bool) {
	var (
		broken bool
		count  int
		detail string
	)
	now := sdk.UnwrapSDKContext(ctx).BlockTime()

	err := k.IterateValidatorMetadata(ctx, func(addr sdk.ValAddress, md types.ValidatorMetadata) bool {
		if md.Status != types.Status_STATUS_INACTIVE {
			return false
		}
		// A zero completion instant (nothing recorded — e.g. a validator that never
		// bonded, or a pre-V-5 record) is inherently not in the future, so the
		// comparison below already treats it as nothing to verify. No separate guard.
		if now.Before(md.UnbondingCompleteTime) {
			broken = true
			count++
			detail += fmt.Sprintf("  validator %s is INACTIVE at %s but its cooldown ends %s\n",
				addr.String(), now.UTC().Format(time.RFC3339), md.UnbondingCompleteTime.UTC().Format(time.RFC3339))
		}
		return false
	})
	if err != nil {
		return fmt.Sprintf("validatorregistry V-5 invariant: iteration error: %v", err), true
	}
	if broken {
		return fmt.Sprintf("validatorregistry V-5 invariant broken: %d validator(s) went INACTIVE before the unstake cooldown elapsed\n%s", count, detail), true
	}
	return "validatorregistry V-5 invariant: no validator exited before its unstake cooldown elapsed", false
}
