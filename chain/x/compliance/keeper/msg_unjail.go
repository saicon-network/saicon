package keeper

import (
	"context"
	"strconv"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// Unjail restores a JAILED validator to ACTIVE via governance (authority-gated).
// Gates: status must be JAILED; the block-based cooldown must have elapsed
// (now - last_status_change_height >= Params.UnjailCooldownBlocks); the validator
// must not be tombstoned in x/slashing (double-sign is irreversible). On success
// it reuses SetCompliance(true, RESTORED) to set C=1 and transition JAILED ->
// ACTIVE through the V-1 ChangeStatus gate, then emits validator_unjailed.
func (k msgServer) Unjail(ctx context.Context, req *types.MsgUnjail) (*types.MsgUnjailResponse, error) {
	if err := k.assertAuthority(req.Authority); err != nil {
		return nil, err
	}

	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidValidator, err.Error())
	}

	md, found, err := k.validatorregistryKeeper.GetValidatorMetadata(ctx, valAddr)
	if err != nil {
		return nil, err
	}
	if !found || md.Status != vrtypes.Status_STATUS_JAILED {
		return nil, errorsmod.Wrapf(types.ErrNotJailed, "validator %s", valAddr.String())
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	now := sdk.UnwrapSDKContext(ctx).BlockHeight()
	// Overflow-safe: compare the block-height DIFFERENCE (bounded, non-negative
	// since the jail height is never in the future) against the cooldown, rather
	// than jailHeight + cooldown which can overflow int64 for a pathological
	// gov-set param and wrap negative — bypassing the gate (fail-open).
	elapsed := now - md.LastStatusChangeHeight
	// A NEGATIVE elapsed means the recorded jail height is ahead of the current
	// one, which is not a real cooldown — it means the height lost its meaning,
	// as after a height-resetting export. Treating it as "cooldown never
	// satisfied" would brick jail recovery permanently. The registry rebases these
	// heights on import so this should not occur; the guard keeps a stale record
	// from becoming unrecoverable if one ever slips through.
	if elapsed >= 0 && elapsed < params.UnjailCooldownBlocks {
		return nil, errorsmod.Wrapf(types.ErrUnjailCooldown,
			"validator %s: %d of %d cooldown blocks elapsed since jail at height %d",
			valAddr.String(), elapsed, params.UnjailCooldownBlocks, md.LastStatusChangeHeight)
	}

	val, err := k.stakingKeeper.GetValidator(ctx, valAddr)
	if err != nil {
		return nil, errorsmod.Wrapf(types.ErrValidatorNotFound, "validator %s: %v", valAddr.String(), err)
	}
	consBytes, err := val.GetConsAddr()
	if err != nil {
		return nil, errorsmod.Wrapf(types.ErrValidatorNotFound, "validator %s consensus address: %v", valAddr.String(), err)
	}
	if k.slashingKeeper.IsTombstoned(ctx, sdk.ConsAddress(consBytes)) {
		return nil, errorsmod.Wrapf(types.ErrValidatorTombstoned, "validator %s", valAddr.String())
	}

	reason := req.Reason
	if reason == "" {
		reason = "governed unjail"
	}
	if err := k.SetCompliance(ctx, valAddr, true, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_RESTORED, reason); err != nil {
		return nil, err
	}

	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeValidatorUnjailed,
		sdk.NewAttribute(types.AttributeKeyValidator, valAddr.String()),
		sdk.NewAttribute(types.AttributeKeyHeight, strconv.FormatInt(now, 10)),
	))

	return &types.MsgUnjailResponse{}, nil
}
