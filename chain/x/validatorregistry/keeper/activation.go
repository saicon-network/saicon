package keeper

import (
	"context"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// ActivateOnBond moves a validator to STATUS_ACTIVE in response to it bonding in
// x/staking (called from the AfterValidatorBonded staking hook).
//
// Behaviour by current state:
//   - no metadata     -> auto-create as STATUS_BONDING (lock-up timer started,
//     compliant by default) then transition to ACTIVE. This
//     covers genesis validators that never call
//     MsgRegisterValidator.
//   - STATUS_BONDING  -> transition to ACTIVE.
//   - STATUS_ACTIVE   -> no-op (idempotent; the hook may fire again on re-bond).
//   - STATUS_UNBONDING -> the exit was ABORTED: resume service, restoring the
//     status recorded at begin-unbonding (see below).
//   - JAILED / QUARANTINED / INACTIVE -> skip. A bond event must never silently
//     reactivate a validator whose penalty or exit was governed elsewhere;
//     recovery goes through the compliance/unjail path.
//
// Transitions always go through ChangeStatus so the V-1 gate validates the edge
// and the status-changed event is emitted (audit trail). The intermediate
// BONDING in the auto-create path never persists across a block boundary, so the
// EndBlock V-1 invariant never observes it.
func (k Keeper) ActivateOnBond(ctx context.Context, valAddr sdk.ValAddress) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	md, found, err := k.GetValidatorMetadata(ctx, valAddr)
	if err != nil {
		return err
	}

	if !found {
		created := types.ValidatorMetadata{
			Status:                 types.Status_STATUS_BONDING,
			BondingStart:           sdkCtx.BlockTime(),
			BondingStartHeight:     types.LockStartHeight(sdkCtx.BlockHeight()),
			LockupDurationSeconds:  types.DefaultLockupDurationSeconds,
			ComplianceFlag:         true,
			LastStatusChangeHeight: types.LockStartHeight(sdkCtx.BlockHeight()),
		}
		if err := k.SetValidatorMetadata(ctx, valAddr, created); err != nil {
			return err
		}

		// Emit the creation event ("none" → BONDING) so genesis validators are
		// equally traceable to those registered via MsgRegisterValidator.
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
			types.EventTypeStatusChanged,
			sdk.NewAttribute(types.AttributeKeyValidator, valAddr.String()),
			sdk.NewAttribute(types.AttributeKeyFromStatus, "none"),
			sdk.NewAttribute(types.AttributeKeyToStatus, types.Status_STATUS_BONDING.HumanString()),
			sdk.NewAttribute(types.AttributeKeyHeight, strconv.FormatInt(sdkCtx.BlockHeight(), 10)),
		))

		return k.ChangeStatus(ctx, valAddr, types.Status_STATUS_ACTIVE)
	}

	switch md.Status {
	case types.Status_STATUS_BONDING:
		return k.ChangeStatus(ctx, valAddr, types.Status_STATUS_ACTIVE)
	case types.Status_STATUS_ACTIVE:
		return nil
	case types.Status_STATUS_UNBONDING:
		return k.resumeAfterAbortedExit(ctx, valAddr, md)
	default:
		sdkCtx.Logger().Debug(
			"validatorregistry: skip activation on bond for non-bonding validator",
			"validator", valAddr.String(),
			"status", md.Status.HumanString(),
		)
		return nil
	}
}

// resumeAfterAbortedExit restores a validator that re-entered the bonded set
// while the registry had it UNBONDING.
//
// This closes a BLOCKER, not an edge case. An ordinary downtime jail is not a
// SAiCoN compliance event — the slash fraction is far below x/compliance's
// double-sign threshold, so the registry keeps saying ACTIVE — yet x/staking
// still removes the validator from the bonded set, which drives the registry to
// UNBONDING. When the operator repairs the node and re-bonds, x/staking moves it
// unbonding -> bonded WITHOUT firing AfterValidatorCreated, so this hook is the
// only place that can react. Before this branch the record stayed UNBONDING
// forever: participation granted no score and no EP, rewards denied every epoch,
// and the governed unjail could not help because it requires STATUS_JAILED.
//
// The restore is FAIL-CLOSED on a conjunction: ACTIVE only when the status
// recorded at begin-unbonding was ACTIVE *and* the compliance mirror is clean;
// anything else returns QUARANTINED, which is recoverable through the governed
// MsgClearFlag. Neither signal is trusted alone — pre_unbonding_status is absent
// on records that entered unbonding before it existed, and the mirror is
// hardcoded to true on two write paths with its V-4.1 invariant unenforced. A
// quarantine must not be laundered by unbonding and re-bonding.
//
// The exit timings and the recorded pre-status are cleared by ChangeStatus for
// any live target, so a later genuine exit starts from a clean slate.
func (k Keeper) resumeAfterAbortedExit(ctx context.Context, valAddr sdk.ValAddress, md types.ValidatorMetadata) error {
	restored := types.Status_STATUS_QUARANTINED
	if md.PreUnbondingStatus == types.Status_STATUS_ACTIVE && md.ComplianceFlag {
		restored = types.Status_STATUS_ACTIVE
	}

	sdk.UnwrapSDKContext(ctx).Logger().Info(
		"validatorregistry: validator re-bonded while unbonding; resuming service",
		"validator", valAddr.String(),
		"pre_unbonding_status", md.PreUnbondingStatus.HumanString(),
		"compliant", md.ComplianceFlag,
		"restored_to", restored.HumanString(),
	)

	return k.ChangeStatus(ctx, valAddr, restored)
}
