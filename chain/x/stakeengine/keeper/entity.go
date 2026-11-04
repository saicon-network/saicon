package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// EntityStanding is a validator's position under the entity aggregate cap
// (ADR-0012 D4, spec §8.2):
//   - a member of an attested entity: the entity's id, its tokens summed over
//     every member in every bond status and over every frozen seat still
//     charged to it, the stored entity cap, and Frozen when the entity holds
//     more active seats (frozen seats charged to it included) than
//     max_active_seats;
//   - not a member but holding a frozen seat: Frozen, with no entity;
//   - otherwise the zero standing, so only the per-validator cap applies
//     (attestation does not gate bonding or delegation, ADR-0014 D1).
func (k Keeper) EntityStanding(ctx context.Context, v sdk.ValAddress) (types.EntityStanding, error) {
	m, isMember, err := k.entityKeeper.EntityOf(ctx, v)
	if err != nil {
		return types.EntityStanding{}, err
	}
	if !isMember {
		frozen, err := k.entityKeeper.IsFrozenSeat(ctx, v)
		if err != nil {
			return types.EntityStanding{}, err
		}
		s := types.EntityStanding{Tokens: math.ZeroInt(), Cap: math.ZeroInt()}
		if frozen {
			s.Frozen = true
			s.Reason = "validator holds an active seat without a live attestation"
		}
		return s, nil
	}
	return k.standingOfEntity(ctx, m.EntityId)
}

// standingOfEntity is the standing every member of the entity shares. Its
// tokens include the frozen seats charged to the entity — validators frozen out
// of it that are still ACTIVE and earning — so neither a re-attestation nor a
// revoke followed by a re-attestation frees room the owner still holds.
func (k Keeper) standingOfEntity(ctx context.Context, entityID string) (types.EntityStanding, error) {
	entityCap, err := k.EntityCap(ctx)
	if err != nil {
		return types.EntityStanding{}, err
	}
	members, err := k.entityKeeper.MemberValidators(ctx, entityID)
	if err != nil {
		return types.EntityStanding{}, err
	}
	charged, err := k.entityKeeper.ChargedFrozenSeats(ctx, entityID)
	if err != nil {
		return types.EntityStanding{}, err
	}
	tokens := math.ZeroInt()
	for _, mv := range append(members, charged...) {
		t, err := k.ValidatorTokens(ctx, mv)
		if err != nil {
			return types.EntityStanding{}, err
		}
		tokens = tokens.Add(t)
	}
	s := types.EntityStanding{EntityID: entityID, Tokens: tokens, Cap: entityCap}
	seats, err := k.entityKeeper.ActiveSeats(ctx, entityID)
	if err != nil {
		return types.EntityStanding{}, err
	}
	limit, err := k.entityKeeper.MaxActiveSeats(ctx)
	if err != nil {
		return types.EntityStanding{}, err
	}
	if seats > limit {
		s.Frozen = true
		s.Reason = fmt.Sprintf("entity %s holds %d active seats, above the limit of %d", entityID, seats, limit)
	}
	return s, nil
}
