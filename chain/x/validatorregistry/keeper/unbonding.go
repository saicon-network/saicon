package keeper

import (
	"context"
	"strconv"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// BeginUnbonding records the validator's exit timing and, when the validator is
// not jailed, moves it to STATUS_UNBONDING. It is invoked from the staking
// AfterValidatorBeginUnbonding hook, which fires on EVERY bonded→unbonding
// transition — including a jail, because jailing removes the validator from the
// bonded set through the same path.
//
// Status handling (design decision 2): only ACTIVE and QUARANTINED move to
// UNBONDING. A validator that is already JAILED stays JAILED — it is being
// penalised, not voluntarily exiting.
//
// Timing handling (design decision 5): the timestamps are recorded
// UNCONDITIONALLY, including for a jailed validator. A jailed validator can exit
// straight to INACTIVE (JAILED → INACTIVE), and without recorded timing the V-5
// invariant would have nothing to verify — or would flag a legitimate exit.
//
// The completion instant is taken from the staking record (design decision 6),
// which staking has already updated to `blockTime + UnbondingTime` before firing
// the hook. Freezing it here makes the V-5 invariant immune to a later governance
// change of the staking UnbondingTime parameter.
//
// A validator that is not in the registry is a no-op: the hook must never fail.
func (k Keeper) BeginUnbonding(ctx context.Context, valAddr sdk.ValAddress) error {
	md, found, err := k.GetValidatorMetadata(ctx, valAddr)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	// Terminal and not-yet-bonded records must not be touched AT ALL — not even
	// the timings.
	//
	// INACTIVE is the dangerous one: x/staking deletes a removed validator's keys,
	// so an operator may re-create a validator at the SAME valoper address. The
	// registry metadata is never deleted, so the stale INACTIVE record survives and
	// ActivateOnBond deliberately skips it. If that re-created validator later
	// leaves the bonded set (voluntary unbond, downtime jail, or simply dropping out
	// by power), writing a FUTURE completion instant onto an INACTIVE record makes
	// UnstakeCooldownInvariant panic in the same EndBlock — a network-wide,
	// deterministic, restart-surviving halt. BONDING cannot legitimately be leaving
	// a bonded set either, so it gets the same treatment.
	switch md.Status {
	case types.Status_STATUS_INACTIVE, types.Status_STATUS_BONDING:
		return nil
	}

	now := sdk.UnwrapSDKContext(ctx).BlockTime()
	md.UnbondingStart = &now
	md.UnbondingCompleteTime = k.unbondingCompletion(ctx, valAddr, now)
	// Record what the validator is leaving, so an ABORTED exit (the operator
	// repairs the node and re-bonds) can be undone without guessing. Both ACTIVE
	// and QUARANTINED land in UNBONDING, and the compliance mirror cannot stand in
	// for the difference — it is hardcoded true on two write paths and its V-4.1
	// invariant is not enforced. See ActivateOnBond.
	md.PreUnbondingStatus = md.Status

	if err := k.SetValidatorMetadata(ctx, valAddr, md); err != nil {
		return err
	}

	switch md.Status {
	case types.Status_STATUS_ACTIVE, types.Status_STATUS_QUARANTINED:
		return k.ChangeStatus(ctx, valAddr, types.Status_STATUS_UNBONDING)
	default:
		// JAILED keeps its status (timings recorded so its JAILED → INACTIVE exit
		// stays verifiable); UNBONDING refreshes timings without a status change.
		return nil
	}
}

// ResetOnValidatorCreated discards a TERMINAL registry record when x/staking
// creates a validator at an address the registry still holds metadata for.
//
// x/staking deletes a removed validator's keys, so an operator may re-create a
// validator at the SAME valoper address. The registry never deleted its metadata,
// so the previous incarnation's record survived: ActivateOnBond deliberately skips
// a terminal INACTIVE record, leaving the re-created validator bonded and signing
// while the registry reported it as exited — permanently ineligible for rewards
// (the gate needs ACTIVE) and invisible to participation scoring.
//
// Per the spec, re-onboarding after a full exit IS a fresh registration, so the
// terminal record is discarded here and the subsequent bond re-creates it through
// the normal ActivateOnBond path. The discarded record's status is emitted as an
// event so the audit trail survives the deletion.
//
// ONLY a terminal record is discarded. STATUS_INACTIVE is the one status meaning
// "a previous incarnation completed its exit"; every other status describes a LIVE
// record that this creation must not destroy:
//
//   - BONDING is a pre-registration made through MsgRegisterValidator, carrying a
//     lock-up duration the proto freezes for that validator. Deleting it let the
//     ordinary sequence (register with a 5-year lock-up, then MsgCreateValidator)
//     silently fall back to the 60-day default with compliance reset to true.
//   - JAILED (or QUARANTINED) is an active sanction; dropping it would let a
//     re-creation launder the penalty.
//
// A live record found here is logged: it is either a legitimate pre-registration
// or a genuine registry/staking divergence worth an operator's attention.
//
// A first-time creation has no metadata and is a silent no-op — this also covers
// genesis, where staking fires the hook before any validator is bonded.
func (k Keeper) ResetOnValidatorCreated(ctx context.Context, valAddr sdk.ValAddress) error {
	md, found, err := k.GetValidatorMetadata(ctx, valAddr)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	if md.Status != types.Status_STATUS_INACTIVE {
		return k.rebaseOnValidatorCreated(ctx, valAddr, md)
	}

	if err := k.RemoveValidatorMetadata(ctx, valAddr); err != nil {
		return err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeRegistryReset,
		sdk.NewAttribute(types.AttributeKeyValidator, valAddr.String()),
		sdk.NewAttribute(types.AttributeKeyPreviousStatus, md.Status.HumanString()),
		sdk.NewAttribute(types.AttributeKeyHeight, strconv.FormatInt(sdkCtx.BlockHeight(), 10)),
	))
	return nil
}

// rebaseOnValidatorCreated keeps a live record through a validator creation but
// restarts its self-stake lock-up clock on the creating block.
//
// Keeping the record is what protects a legitimate MsgRegisterValidator
// pre-registration and, more importantly, any live sanction: discarding a JAILED
// or QUARANTINED record would let an operator launder the penalty by re-creating
// the validator at the same address.
//
// Restarting the clock is what stops the opposite abuse. Only the auto-create
// path stamps BondingStart, so a record left over from an earlier incarnation
// carries that incarnation's lock-up window — a validator that bonded two years
// ago, exited without the record reaching a terminal state, and re-created here
// would come back already unlocked. The new incarnation serves a full fresh
// lock-up; its status and compliance flag are untouched.
//
// The duration is clamped up to the chain minimum as well, so a record written
// before that floor existed cannot carry a shorter commitment forward.
func (k Keeper) rebaseOnValidatorCreated(ctx context.Context, valAddr sdk.ValAddress, md types.ValidatorMetadata) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	md.BondingStart = sdkCtx.BlockTime()
	md.BondingStartHeight = types.LockStartHeight(sdkCtx.BlockHeight())
	if md.LockupDurationSeconds < types.DefaultLockupDurationSeconds {
		md.LockupDurationSeconds = types.DefaultLockupDurationSeconds
	}
	// The previous incarnation's exit is void — this address holds a new validator.
	md.UnbondingStart = nil
	md.UnbondingCompleteTime = time.Time{}
	md.PreUnbondingStatus = types.Status_STATUS_UNSPECIFIED

	sdkCtx.Logger().Info(
		"validatorregistry: validator created over a live registry record; keeping status, restarting the lock-up",
		"validator", valAddr.String(),
		"status", md.Status.HumanString(),
		"lockup_seconds", md.LockupDurationSeconds,
	)

	return k.SetValidatorMetadata(ctx, valAddr, md)
}

// unbondingCompletion resolves the instant the unstake cooldown ends. Primary
// source is the staking validator record (already carries UnbondingTime when the
// hook fires); the params-based value is a fallback for the case where the record
// cannot be read. A zero result means "unknown" — the V-5 invariant treats that as
// unverifiable and skips it rather than halting the chain.
func (k Keeper) unbondingCompletion(ctx context.Context, valAddr sdk.ValAddress, now time.Time) time.Time {
	if val, err := k.stakingKeeper.GetValidator(ctx, valAddr); err == nil && !val.UnbondingTime.IsZero() {
		return val.UnbondingTime
	}
	if d, err := k.stakingKeeper.UnbondingTime(ctx); err == nil && d > 0 {
		return now.Add(d)
	}
	return time.Time{}
}

// CompleteExit moves a validator to STATUS_INACTIVE. It is invoked from the
// staking AfterValidatorRemoved hook. staking only removes a validator holding zero
// tokens, so for a validator that WAS bonded the unbonding period has necessarily
// matured — the cooldown is unbypassable by construction. (A validator that never
// bonded is removed immediately; it owes no cooldown and carries no completion
// instant, which the invariant treats as nothing to verify.) UnstakeCooldownInvariant
// is the fail-loud backstop.
//
// Legal sources are UNBONDING (normal exit) and JAILED (a jailed validator that
// fully unbonds — see the JAILED → INACTIVE edge). Already-INACTIVE is an
// idempotent no-op; any other status is rejected by the V-1 gate in ChangeStatus.
//
// A validator that is not in the registry is a no-op.
func (k Keeper) CompleteExit(ctx context.Context, valAddr sdk.ValAddress) error {
	md, found, err := k.GetValidatorMetadata(ctx, valAddr)
	if err != nil {
		return err
	}
	if !found || md.Status == types.Status_STATUS_INACTIVE {
		return nil
	}
	return k.ChangeStatus(ctx, valAddr, types.Status_STATUS_INACTIVE)
}
