package keeper

import (
	"context"
	"strconv"
	"time"

	"cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// ChangeStatus is the sole authorized mutator of ValidatorMetadata.Status.
//
// It enforces invariant V-1 (chain/docs/validators/README.md §2.2/§2.3) at the
// gate: it loads the current metadata (error if absent), rejects any transition
// not permitted by types.CanTransition with ErrInvalidTransition, then updates
// Status + LastStatusChangeHeight, persists, and emits a typed event.
//
// Because this is the only path that changes status, illegal transitions are
// impossible by construction. The EndBlock invariant (keeper.ValidatorStatusInvariant)
// is defense-in-depth against direct-Set corruption.
func (k Keeper) ChangeStatus(ctx context.Context, addr sdk.ValAddress, newStatus types.Status) error {
	md, found, err := k.GetValidatorMetadata(ctx, addr)
	if err != nil {
		return err
	}
	if !found {
		return errors.Wrapf(types.ErrInvalidTransition, "no validator metadata for %s", addr.String())
	}

	from := md.Status
	if !types.CanTransition(from, newStatus) {
		return errors.Wrapf(types.ErrInvalidTransition,
			"%s -> %s not allowed", from.HumanString(), newStatus.HumanString())
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	// Normalised the same way as at creation and on import: baseapp reports the
	// init-chain block as height 0, and a stored 0 cannot be told apart from "never
	// recorded". The unjail cooldown reads this field as `now - stored`, so a 0 makes
	// the gate pass immediately.
	height := types.LockStartHeight(sdkCtx.BlockHeight())

	md.Status = newStatus
	md.LastStatusChangeHeight = height

	// Reaching a LIVE status means any exit in progress was aborted, so the exit
	// bookkeeping must not linger. Doing it here rather than in the callers covers
	// every edge at once — the re-bond path (UNBONDING -> ACTIVE/QUARANTINED), the
	// governed unjail (JAILED -> ACTIVE, where BeginUnbonding stamps timings even
	// though the status stays JAILED), and the compliance messages, which gate on
	// this same table and can therefore drive these edges too.
	//
	// A stale FUTURE completion instant left on a live record is precisely the
	// shape of the CRITICAL this lifecycle already had to fix once: the record
	// later reaches INACTIVE still carrying it, and UnstakeCooldownInvariant panics
	// the whole network in EndBlock.
	if newStatus == types.Status_STATUS_ACTIVE || newStatus == types.Status_STATUS_QUARANTINED {
		md.UnbondingStart = nil
		md.UnbondingCompleteTime = time.Time{}
		md.PreUnbondingStatus = types.Status_STATUS_UNSPECIFIED
	}

	if err := k.SetValidatorMetadata(ctx, addr, md); err != nil {
		return err
	}

	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeStatusChanged,
		sdk.NewAttribute(types.AttributeKeyValidator, addr.String()),
		sdk.NewAttribute(types.AttributeKeyFromStatus, from.HumanString()),
		sdk.NewAttribute(types.AttributeKeyToStatus, newStatus.HumanString()),
		sdk.NewAttribute(types.AttributeKeyHeight, strconv.FormatInt(height, 10)),
	))

	// TODO(WI-008): invoke status-change hooks here (x/compliance consumer).
	return nil
}
