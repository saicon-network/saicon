package keeper

import (
	"bytes"
	"context"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

var _ vrtypes.ActivationGate = Keeper{}

// activationBlock is the rule of spec §7.2 without side effects: an empty
// reason when val may activate, else why not. It also returns the validator's
// entity id when it has one. It only reads, so a caller can ask the question
// without touching the store or the event manager.
func (k Keeper) activationBlock(ctx context.Context, val sdk.ValAddress) (reason, entityID string, err error) {
	m, found, err := k.EntityOf(ctx, val)
	if err != nil {
		return "", "", err
	}
	if !found {
		return "not attested by an entity (ADR-0014 D1)", "", nil
	}
	members, err := k.MemberValidators(ctx, m.EntityId)
	if err != nil {
		return "", "", err
	}
	// The validator itself never counts: re-checking an active member must pass.
	var seats uint32
	for _, v := range members {
		if bytes.Equal(v, val) {
			continue
		}
		st, ok, err := k.registryKeeper.GetValidatorStatus(ctx, v)
		if err != nil {
			return "", "", err
		}
		if ok && st == vrtypes.Status_STATUS_ACTIVE {
			seats++
		}
	}
	// Frozen seats charged to the entity count too (ChargedFrozenSeats); val is
	// never among them, because a member holds no frozen seat (V-12 (c)).
	charged, err := k.ChargedFrozenSeats(ctx, m.EntityId)
	if err != nil {
		return "", "", err
	}
	seats += uint32(len(charged))
	limit, err := k.MaxActiveSeats(ctx)
	if err != nil {
		return "", "", err
	}
	if seats >= limit {
		return fmt.Sprintf("entity %s already holds %d active seats; the limit is %d", m.EntityId, seats, limit), m.EntityId, nil
	}
	return "", m.EntityId, nil
}

// MayActivate is the activation gate x/validatorregistry consults before every
// move to STATUS_ACTIVE (spec §7). A refusal wraps vrtypes.ErrActivationHeld,
// records the validator as pending and emits validator_activation_held; an
// allowed activation takes it off the pending list. The verdict is computed
// before the first write, so an error leaves the store and events untouched.
func (k Keeper) MayActivate(ctx context.Context, val sdk.ValAddress) error {
	reason, _, err := k.activationBlock(ctx, val)
	if err != nil {
		return err
	}
	if reason == "" {
		return k.Pending.Remove(ctx, val)
	}
	if err := k.Pending.Set(ctx, val); err != nil {
		return err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeActivationHeld,
		sdk.NewAttribute(types.AttributeKeyValidator, val.String()),
		sdk.NewAttribute(types.AttributeKeyReason, reason),
	))
	return errorsmod.Wrap(vrtypes.ErrActivationHeld, reason)
}
