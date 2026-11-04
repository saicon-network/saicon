package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// Every function in this file is read-only: the activation gate calls them
// before a refusal, and the staking bond hook is not branch-cached.

// EntityOf returns the membership of a validator, if it has one.
func (k Keeper) EntityOf(ctx context.Context, val sdk.ValAddress) (types.Member, bool, error) {
	m, err := k.Members.Get(ctx, val)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Member{}, false, nil
		}
		return types.Member{}, false, err
	}
	return m, true, nil
}

// EntityAt returns the entity a validator belonged to when it voted at height h,
// judged on the state committed at h-1 (spec §6.2): a member that joined below h,
// or a departure that left at or after h, or whose frozen seat was still charged
// at h. Only the last departure per validator is kept.
func (k Keeper) EntityAt(ctx context.Context, val sdk.ValAddress, h int64) (string, bool, error) {
	m, found, err := k.EntityOf(ctx, val)
	if err != nil {
		return "", false, err
	}
	if found && m.JoinedHeight < h {
		return m.EntityId, true, nil
	}
	d, err := k.Departures.Get(ctx, val)
	if errors.Is(err, collections.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if d.JoinedHeight < h && (h <= d.LeftHeight || (d.Frozen && (d.ChargedUntil == 0 || h <= d.ChargedUntil))) {
		return d.EntityId, true, nil
	}
	return "", false, nil
}

// HasEntity reports whether an entity with this id exists, attested or revoked.
func (k Keeper) HasEntity(ctx context.Context, entityID string) (bool, error) {
	return k.Entities.Has(ctx, entityID)
}

// MemberValidators returns an entity's validators sorted by operator bytes.
func (k Keeper) MemberValidators(ctx context.Context, entityID string) ([]sdk.ValAddress, error) {
	rng := collections.NewPrefixedPairRange[string, []byte](entityID)
	it, err := k.MembersByEntity.Iterate(ctx, rng)
	if err != nil {
		return nil, err
	}
	defer func() { _ = it.Close() }()
	var out []sdk.ValAddress
	for ; it.Valid(); it.Next() {
		key, err := it.Key()
		if err != nil {
			return nil, err
		}
		out = append(out, sdk.ValAddress(key.K2()))
	}
	return out, nil
}

// ActiveSeats counts the seats charged to an entity: its members the registry
// reports STATUS_ACTIVE, plus the frozen seats charged to it (ChargedFrozenSeats).
func (k Keeper) ActiveSeats(ctx context.Context, entityID string) (uint32, error) {
	members, err := k.MemberValidators(ctx, entityID)
	if err != nil {
		return 0, err
	}
	var n uint32
	for _, v := range members {
		status, found, err := k.registryKeeper.GetValidatorStatus(ctx, v)
		if err != nil {
			return 0, err
		}
		if found && status == vrtypes.Status_STATUS_ACTIVE {
			n++
		}
	}
	charged, err := k.ChargedFrozenSeats(ctx, entityID)
	if err != nil {
		return 0, err
	}
	return n + uint32(len(charged)), nil
}

// ChargedFrozenSeats returns the validators whose frozen seat is charged to an
// entity, sorted by operator bytes: every FrozenSeat whose former entity is
// entityID and whose validator the registry still reports STATUS_ACTIVE.
//
// A frozen seat keeps earning (J2), so it keeps counting against the owner it
// was frozen out of — its seat here, its tokens in x/stakeengine's aggregate.
// Without the charge, re-attesting an entity with fresh validators, or revoking
// and re-attesting it, would let one owner exceed max_active_seats and the
// entity cap by growth. The charge ends the moment the validator leaves ACTIVE;
// EndBlock then sweeps the row. A frozen validator that joins an entity loses
// its row (Attest) and counts as that entity's member instead.
//
// The walk covers the whole Frozen collection. It is bounded by the ACTIVE set,
// because EndBlock sweeps every row whose validator is not ACTIVE, and it is
// empty unless an ACTIVE validator was revoked or removed.
func (k Keeper) ChargedFrozenSeats(ctx context.Context, entityID string) ([]sdk.ValAddress, error) {
	var out []sdk.ValAddress
	err := k.Frozen.Walk(ctx, nil, func(key []byte, seat types.FrozenSeat) (bool, error) {
		if seat.FormerEntityId != entityID {
			return false, nil
		}
		status, found, err := k.registryKeeper.GetValidatorStatus(ctx, sdk.ValAddress(key))
		if err != nil {
			return true, err
		}
		if found && status == vrtypes.Status_STATUS_ACTIVE {
			out = append(out, sdk.ValAddress(key))
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MaxActiveSeats returns params.max_active_seats.
func (k Keeper) MaxActiveSeats(ctx context.Context) (uint32, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	return params.MaxActiveSeats, nil
}

// IsFrozenSeat reports whether the validator holds a frozen seat.
func (k Keeper) IsFrozenSeat(ctx context.Context, val sdk.ValAddress) (bool, error) {
	return k.Frozen.Has(ctx, val)
}

// AttestedEntityIDs returns the ids of entities in ATTESTED status, sorted.
func (k Keeper) AttestedEntityIDs(ctx context.Context) ([]string, error) {
	it, err := k.Entities.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = it.Close() }()
	var ids []string
	for ; it.Valid(); it.Next() {
		kv, err := it.KeyValue()
		if err != nil {
			return nil, err
		}
		if kv.Value.Status == types.EntityStatus_ENTITY_STATUS_ATTESTED {
			ids = append(ids, kv.Key)
		}
	}
	return ids, nil
}

// Tickets returns one lottery ticket per attested entity, sorted by entity id.
func (k Keeper) Tickets(ctx context.Context) ([]types.Ticket, error) {
	ids, err := k.AttestedEntityIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]types.Ticket, 0, len(ids))
	for _, id := range ids {
		members, err := k.MemberValidators(ctx, id)
		if err != nil {
			return nil, err
		}
		strs := make([]string, len(members))
		for i, v := range members {
			strs[i] = v.String()
		}
		seats, err := k.ActiveSeats(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, types.Ticket{
			EntityId: id, Members: strs, ActiveSeats: seats, Tickets: types.TicketsPerEntity,
		})
	}
	return out, nil
}
