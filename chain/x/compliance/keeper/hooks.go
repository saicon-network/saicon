package keeper

import (
	"context"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

// Hooks wraps the compliance Keeper to implement stakingtypes.StakingHooks.
// The only hook that does work is BeforeValidatorSlashed, which auto-flags
// double-sign slashes; all other hooks are no-ops.
type Hooks struct {
	k Keeper
}

var _ stakingtypes.StakingHooks = Hooks{}

// Hooks returns the staking hooks for the compliance module.
func (k Keeper) Hooks() Hooks {
	return Hooks{k: k}
}

// BeforeValidatorSlashed is the single slash signal exposed by the SDK. It does
// not carry an infraction type, so the infraction must be inferred — and under
// the Register's slashing policy (ADR-0010) the inference is exact rather than
// heuristic: ANY POSITIVE BURN IS A DOUBLE-SIGN.
//
// Why that is safe by construction, in two steps:
//
//  1. Ordinary downtime carries a ZERO stake slash in Phase 1
//     (`slash_fraction_downtime = 0`), so the only infraction configured with a
//     positive fraction is double-sign.
//  2. x/staking never calls this hook when the burn is zero — it returns early
//     at `cosmos-sdk@v0.53.6/x/staking/keeper/slash.go:148-151` before reaching
//     the hooks. So a zero-burn downtime slash cannot arrive here at all.
//
// This replaces an earlier midpoint threshold of 0.025, justified as the middle
// of the SDK's 0.01 downtime / 0.05 double-sign band. With downtime at 0 and
// double-sign at 0.10 that band is empty, and keeping the midpoint would MISS
// real double-signs: the fraction passed here is the *effective* burn
// (tokensToBurn / validator.Tokens), which falls arbitrarily far below the
// configured fraction when the validator's bonded tokens shrank between the
// infraction and the slash.
//
// The zero guard below is defensive, not load-bearing — the SDK does not deliver
// a zero fraction today, but this method is exported and must not jail a
// validator for burning nothing.
//
// The hook must never error or halt the chain: validators not present in the
// SAiCoN registry are silently ignored.
func (h Hooks) BeforeValidatorSlashed(ctx context.Context, valAddr sdk.ValAddress, fraction math.LegacyDec) error {
	if fraction.IsNil() || !fraction.IsPositive() {
		return nil
	}

	_, found, err := h.k.validatorregistryKeeper.GetValidatorStatus(ctx, valAddr)
	if err != nil {
		sdk.UnwrapSDKContext(ctx).Logger().Error("compliance: double-sign auto-flag failed", "validator", valAddr.String(), "err", err)
		return err
	}
	// Guard needed: without it an unregistered validator would still get a ComplianceState record written before the inner transition no-ops.
	if !found {
		return nil
	}

	if err := h.k.SetCompliance(
		ctx,
		valAddr,
		false,
		types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION,
		"double-sign slash detected via staking hook",
	); err != nil {
		sdk.UnwrapSDKContext(ctx).Logger().Error("compliance: double-sign auto-flag failed", "validator", valAddr.String(), "err", err)
		return err
	}

	h.k.zeroReputationBestEffort(ctx, valAddr)

	return nil
}

// The remaining StakingHooks methods are no-ops for the compliance module.

func (h Hooks) AfterValidatorCreated(_ context.Context, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) BeforeValidatorModified(_ context.Context, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterValidatorRemoved(_ context.Context, _ sdk.ConsAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterValidatorBonded(_ context.Context, _ sdk.ConsAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterValidatorBeginUnbonding(_ context.Context, _ sdk.ConsAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) BeforeDelegationCreated(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) BeforeDelegationSharesModified(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) BeforeDelegationRemoved(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterDelegationModified(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterUnbondingInitiated(_ context.Context, _ uint64) error {
	return nil
}

// zeroReputationBestEffort applies the reputation half of the double-sign
// sanction (ADR-0010 §4): the in-flight epoch's participation score goes to
// zero, and — once the WARP engine exists — so does WARP.
//
// BEST EFFORT, deliberately. The load-bearing sanctions are the stake burn
// (x/slashing), C=0 and the jail, and all three have already landed by the time
// this runs. If the participation layer cannot be reached, the right outcome is
// a punished validator with a stale score, not a halted chain — this executes
// inside x/staking's slash path, where a returned error propagates.
//
// It cannot touch EP: types.ParticipationKeeper does not expose a way to.
func (k Keeper) zeroReputationBestEffort(ctx context.Context, valAddr sdk.ValAddress) {
	logger := sdk.UnwrapSDKContext(ctx).Logger()

	epochLen, err := k.participationKeeper.EpochLengthBlocks(ctx)
	if err != nil || epochLen == 0 {
		logger.Error("compliance: cannot determine the in-flight epoch; reputation left as scored",
			"validator", valAddr.String(), "err", err)
		return
	}

	epoch := uint64(sdk.UnwrapSDKContext(ctx).BlockHeight()) / epochLen
	if err := k.participationKeeper.ZeroReputation(ctx, valAddr, epoch); err != nil {
		logger.Error("compliance: zeroing reputation failed; validator is still jailed and flagged",
			"validator", valAddr.String(), "epoch", epoch, "err", err)
	}
}
