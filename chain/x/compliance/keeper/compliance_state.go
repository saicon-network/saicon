package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// SetCompliance records a compliance event for a validator, mirrors the
// resulting C flag into x/validatorregistry (invariant V-4.1), and drives the
// compliance-triggered status transition (chain/docs/validators/README.md §4.2):
//
//   - non-compliant + CRITICAL_VIOLATION → jail
//   - non-compliant + other events       → quarantine
//   - restored (compliant)               → back to active (from quarantined)
//
// The status transition is applied only if the validator's current status
// permits it per the V-1 table (e.g. a bonding validator is not consensus-
// active, so flagging it changes only the C flag, not the status).
func (k Keeper) SetCompliance(
	ctx context.Context,
	addr sdk.ValAddress,
	compliant bool,
	event types.ComplianceEventType,
	reason string,
) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	state := types.ComplianceState{
		ValidatorAddress: addr.String(),
		Compliant:        compliant,
		LastEvent:        event,
		LastEventTime:    sdkCtx.BlockTime(),
		Reason:           reason,
	}

	if err := k.ComplianceState.Set(ctx, addr.Bytes(), state); err != nil {
		return err
	}

	// Mirror the C flag into the validator registry cache (V-4.1).
	if err := k.validatorregistryKeeper.SetComplianceFlag(ctx, addr, compliant); err != nil {
		return err
	}

	evType := types.EventTypeComplianceFlagged
	if compliant {
		evType = types.EventTypeComplianceCleared
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		evType,
		sdk.NewAttribute(types.AttributeKeyValidator, addr.String()),
		sdk.NewAttribute(types.AttributeKeyEvent, event.String()),
		sdk.NewAttribute(types.AttributeKeyReason, reason),
	))

	return k.applyStatusTransition(ctx, addr, compliant, event)
}

// applyStatusTransition moves the validator to the status implied by the
// compliance change, but only if the current status allows the transition.
func (k Keeper) applyStatusTransition(
	ctx context.Context,
	addr sdk.ValAddress,
	compliant bool,
	event types.ComplianceEventType,
) error {
	current, found, err := k.validatorregistryKeeper.GetValidatorStatus(ctx, addr)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	var target vrtypes.Status
	if !compliant {
		if event == types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION {
			target = vrtypes.Status_STATUS_JAILED
		} else {
			target = vrtypes.Status_STATUS_QUARANTINED
		}
	} else {
		// Restore: only meaningful from quarantined back to active.
		target = vrtypes.Status_STATUS_ACTIVE
	}

	if !vrtypes.CanTransition(current, target) {
		// Current status doesn't permit this transition (e.g. bonding validator,
		// or already in target). Flag change stands; status unchanged.
		return nil
	}

	return k.validatorregistryKeeper.ChangeStatus(ctx, addr, target)
}

// GetCompliance returns the compliance state for a validator.
// Returns (state, true, nil) when present, (zero, false, nil) when absent.
func (k Keeper) GetCompliance(ctx context.Context, addr sdk.ValAddress) (types.ComplianceState, bool, error) {
	state, err := k.ComplianceState.Get(ctx, addr.Bytes())
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.ComplianceState{}, false, nil
		}
		return types.ComplianceState{}, false, err
	}
	return state, true, nil
}
