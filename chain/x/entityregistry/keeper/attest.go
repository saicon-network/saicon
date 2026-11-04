package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// authorized accepts a listed attestor or the gov authority (spec J9). The
// comparison is on bytes: params hold saicon1… strings, decoded with the
// explicit prefix, while the signer arrives in whatever prefix the app uses.
func (k Keeper) authorized(ctx context.Context, signer sdk.AccAddress) error {
	if bytes.Equal(signer, k.authority) {
		return nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	for _, a := range params.Attestors {
		bz, err := types.DecodeAccount(a)
		if err != nil {
			return err
		}
		if bytes.Equal(signer, bz) {
			return nil
		}
	}
	return errorsmod.Wrap(types.ErrNotAttestor, signer.String())
}

// Attest records an attestation, replacing the entity's member list when the id
// exists (spec §6.1). It reports whether an existing entity was replaced. Every
// read and refusal comes before the first write, so a refused attestation
// writes nothing.
func (k Keeper) Attest(ctx context.Context, signer sdk.AccAddress, a types.Attestation) (bool, error) {
	if err := k.authorized(ctx, signer); err != nil {
		return false, err
	}
	if err := a.Validate(); err != nil {
		return false, err
	}
	listed := make(map[string]bool, len(a.Validators))
	// staying holds the current row of every listed validator that is already
	// a member of this entity: its key and height are carried over (spec §6.2).
	staying := make(map[string]types.Member, len(a.Validators))
	for _, v := range a.Validators {
		m, found, err := k.EntityOf(ctx, v)
		if err != nil {
			return false, err
		}
		if found && m.EntityId != a.EntityID {
			return false, errorsmod.Wrapf(types.ErrMemberOfAnotherEntity,
				"%s belongs to %s; re-attest %s without it first", v, m.EntityId, m.EntityId)
		}
		if found {
			staying[string(v)] = m
		}
		listed[string(v)] = true
	}
	bindings := make(map[string][]byte, len(a.VrfKeys))
	for _, b := range a.VrfKeys {
		if err := k.checkOffered(ctx, b); err != nil {
			return false, err
		}
		bindings[string(b.Validator)] = b.PublicKey
	}
	_, err := k.Entities.Get(ctx, a.EntityID)
	replaced := err == nil
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	signerStr, err := k.addressCodec.BytesToString(signer)
	if err != nil {
		return false, err
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	old, err := k.MemberValidators(ctx, a.EntityID)
	if err != nil {
		return false, err
	}
	// Removal order is the store order (operator bytes), so events are deterministic.
	for _, v := range old {
		if listed[string(v)] {
			continue
		}
		if _, err := k.removeMember(ctx, a.EntityID, v, "removed from entity "+a.EntityID); err != nil {
			return false, err
		}
	}
	if err := k.Entities.Set(ctx, a.EntityID, types.Entity{
		Id:              a.EntityID,
		DisplayName:     a.DisplayName,
		AttestationHash: a.AttestationHash,
		Attestor:        signerStr,
		Status:          types.EntityStatus_ENTITY_STATUS_ATTESTED,
		AttestedHeight:  height,
	}); err != nil {
		return false, err
	}
	members := make([]string, 0, len(a.Validators))
	var rekeyed []string
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	for _, v := range a.Validators {
		row := types.Member{Validator: v.String(), EntityId: a.EntityID, AttestedHeight: height, JoinedHeight: height}
		// The four key rules of spec §6.2: a staying member keeps its key and
		// height unless it is named with a different key; a new member has a
		// key only when named. A staying member also keeps the height it joined.
		if old, ok := staying[string(v)]; ok {
			row.VrfPublicKey, row.VrfKeyHeight = old.VrfPublicKey, old.VrfKeyHeight
			row.JoinedHeight = old.JoinedHeight
		}
		if pk, ok := bindings[string(v)]; ok && hex.EncodeToString(pk) != row.VrfPublicKey {
			row.VrfPublicKey, row.VrfKeyHeight = hex.EncodeToString(pk), height
			rekeyed = append(rekeyed, v.String())
			sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
				types.EventTypeVrfKeyBound,
				sdk.NewAttribute(types.AttributeKeyEntityID, a.EntityID),
				sdk.NewAttribute(types.AttributeKeyValidator, v.String()),
				sdk.NewAttribute(types.AttributeKeyVrfPublicKey, row.VrfPublicKey),
			))
		}
		if err := k.Members.Set(ctx, v, row); err != nil {
			return false, err
		}
		if err := k.MembersByEntity.Set(ctx, collections.Join(a.EntityID, []byte(v))); err != nil {
			return false, err
		}
		// Joining an entity clears a seat frozen by an earlier removal, and
		// with it the charge that seat put on its former entity.
		hadSeat, err := k.Frozen.Has(ctx, v)
		if err != nil {
			return false, err
		}
		if hadSeat {
			if err := k.endCharge(ctx, v, height); err != nil {
				return false, err
			}
			if err := k.Frozen.Remove(ctx, v); err != nil {
				return false, err
			}
		}
		members = append(members, v.String())
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeEntityAttested,
		sdk.NewAttribute(types.AttributeKeyEntityID, a.EntityID),
		sdk.NewAttribute(types.AttributeKeyAttestor, signerStr),
		sdk.NewAttribute(types.AttributeKeyAttestationHash, a.AttestationHash),
		sdk.NewAttribute(types.AttributeKeyMembers, strings.Join(members, ",")),
		sdk.NewAttribute(types.AttributeKeyReplaced, strconv.FormatBool(replaced)),
		sdk.NewAttribute(types.AttributeKeyVrfKeys, strings.Join(rekeyed, ",")),
	))
	return replaced, nil
}

// Revoke withdraws an entity's attestation. Its members leave the entity; the
// active ones keep their seat as a frozen seat. It returns the validators whose
// seat was frozen, in operator byte order.
func (k Keeper) Revoke(ctx context.Context, signer sdk.AccAddress, entityID, reason string) ([]sdk.ValAddress, error) {
	if err := k.authorized(ctx, signer); err != nil {
		return nil, err
	}
	if err := types.ValidateReason(reason); err != nil {
		return nil, err
	}
	e, err := k.Entities.Get(ctx, entityID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrap(types.ErrEntityNotFound, entityID)
		}
		return nil, err
	}
	if e.Status != types.EntityStatus_ENTITY_STATUS_ATTESTED {
		return nil, errorsmod.Wrap(types.ErrEntityRevoked, entityID)
	}
	members, err := k.MemberValidators(ctx, entityID)
	if err != nil {
		return nil, err
	}
	var frozen []sdk.ValAddress
	for _, v := range members {
		froze, err := k.removeMember(ctx, entityID, v, "entity "+entityID+" revoked: "+reason)
		if err != nil {
			return nil, err
		}
		if froze {
			frozen = append(frozen, v)
		}
	}
	e.Status = types.EntityStatus_ENTITY_STATUS_REVOKED
	e.RevokedHeight = sdk.UnwrapSDKContext(ctx).BlockHeight()
	e.RevokeReason = reason
	if err := k.Entities.Set(ctx, entityID, e); err != nil {
		return nil, err
	}
	signerStr, err := k.addressCodec.BytesToString(signer)
	if err != nil {
		return nil, err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeEntityRevoked,
		sdk.NewAttribute(types.AttributeKeyEntityID, entityID),
		sdk.NewAttribute(types.AttributeKeyAuthority, signerStr),
		sdk.NewAttribute(types.AttributeKeyReason, reason),
	))
	return frozen, nil
}

// removeMember drops a validator from an entity. A validator the registry
// reports ACTIVE keeps its seat as a frozen seat (freeze, never shed). Every
// removal leaves a departure row (spec §6.2). It returns whether a seat was
// frozen.
func (k Keeper) removeMember(ctx context.Context, entityID string, v sdk.ValAddress, reason string) (bool, error) {
	row, err := k.Members.Get(ctx, v)
	if err != nil {
		return false, err
	}
	if err := k.Members.Remove(ctx, v); err != nil {
		return false, err
	}
	if err := k.MembersByEntity.Remove(ctx, collections.Join(entityID, []byte(v))); err != nil {
		return false, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	status, found, err := k.registryKeeper.GetValidatorStatus(ctx, v)
	if err != nil {
		return false, err
	}
	froze := found && status == vrtypes.Status_STATUS_ACTIVE
	if froze {
		if err := k.Frozen.Set(ctx, v, types.FrozenSeat{
			Validator: v.String(), FormerEntityId: entityID, Reason: reason, Height: sdkCtx.BlockHeight(),
		}); err != nil {
			return false, err
		}
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
			types.EventTypeSeatFrozen,
			sdk.NewAttribute(types.AttributeKeyEntityID, entityID),
			sdk.NewAttribute(types.AttributeKeyValidator, v.String()),
			sdk.NewAttribute(types.AttributeKeyReason, reason),
		))
	}
	if err := k.Departures.Set(ctx, v, types.Departure{
		Validator:    v.String(),
		EntityId:     entityID,
		JoinedHeight: row.JoinedHeight,
		LeftHeight:   sdkCtx.BlockHeight(),
		Frozen:       froze,
	}); err != nil {
		return false, err
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeMemberRemoved,
		sdk.NewAttribute(types.AttributeKeyEntityID, entityID),
		sdk.NewAttribute(types.AttributeKeyValidator, v.String()),
		sdk.NewAttribute(types.AttributeKeyReason, reason),
	))
	return froze, nil
}

// endCharge records the height at which a validator's frozen seat stopped being
// charged to its former entity: it sets charged_until on the validator's
// departure while that charge is still open (spec §6.2). It does nothing when
// there is no such departure.
func (k Keeper) endCharge(ctx context.Context, v sdk.ValAddress, height int64) error {
	d, err := k.Departures.Get(ctx, v)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	if !d.Frozen || d.ChargedUntil != 0 {
		return nil
	}
	d.ChargedUntil = height
	return k.Departures.Set(ctx, v, d)
}
