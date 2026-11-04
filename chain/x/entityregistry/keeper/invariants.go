package keeper

import (
	"context"
	"fmt"
	"strings"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// RegistryInvariant implements V-12, the entity registry integrity invariant
// (spec §16): (a) Members and MembersByEntity are the same relation; (b) every
// member's entity exists and is ATTESTED, and every ATTESTED entity has 1 to
// MaxMembersPerEntity members; (c) Frozen and Members are disjoint; (d) every
// registry-ACTIVE validator is a member or has a frozen seat. It is read-only,
// lists every violation it finds, and reports a read error as broken, like V-10.
func (k Keeper) RegistryInvariant(ctx context.Context) (string, bool) {
	msg, broken, err := k.checkRegistry(ctx)
	if err != nil {
		return fmt.Sprintf("entityregistry V-12 broken: read error: %v", err), true
	}
	return msg, broken
}

func (k Keeper) checkRegistry(ctx context.Context) (string, bool, error) {
	var violations []string
	add := func(format string, args ...any) { violations = append(violations, fmt.Sprintf(format, args...)) }

	entities := map[string]types.Entity{}
	var ids []string
	if err := k.Entities.Walk(ctx, nil, func(id string, e types.Entity) (bool, error) {
		entities[id] = e
		ids = append(ids, id)
		return false, nil
	}); err != nil {
		return "", false, err
	}

	counts := map[string]int{}
	if err := k.Members.Walk(ctx, nil, func(key []byte, m types.Member) (bool, error) {
		addr := sdk.ValAddress(key)
		has, err := k.MembersByEntity.Has(ctx, collections.Join(m.EntityId, key))
		if err != nil {
			return true, err
		}
		if !has {
			add("member %s of entity %s has no MembersByEntity row", addr, m.EntityId)
		}
		counts[m.EntityId]++
		e, ok := entities[m.EntityId]
		switch {
		case !ok:
			add("member %s names entity %s, which does not exist", addr, m.EntityId)
		case e.Status != types.EntityStatus_ENTITY_STATUS_ATTESTED:
			add("member %s names entity %s, which is not attested", addr, m.EntityId)
		}
		frozen, err := k.Frozen.Has(ctx, key)
		if err != nil {
			return true, err
		}
		if frozen {
			add("validator %s is both a member and frozen", addr)
		}
		return false, nil
	}); err != nil {
		return "", false, err
	}

	if err := k.MembersByEntity.Walk(ctx, nil, func(key collections.Pair[string, []byte]) (bool, error) {
		addr := sdk.ValAddress(key.K2())
		m, err := k.Members.Get(ctx, key.K2())
		switch {
		case err != nil && !isNotFound(err):
			return true, err
		case err != nil:
			add("MembersByEntity row (%s, %s) has no Members row", key.K1(), addr)
		case m.EntityId != key.K1():
			add("MembersByEntity row (%s, %s) disagrees: the Members row says %s", key.K1(), addr, m.EntityId)
		}
		return false, nil
	}); err != nil {
		return "", false, err
	}

	for _, id := range ids {
		if entities[id].Status != types.EntityStatus_ENTITY_STATUS_ATTESTED {
			continue
		}
		if n := counts[id]; n < 1 || n > types.MaxMembersPerEntity {
			add("entity %s has %d members, want 1-%d", id, n, types.MaxMembersPerEntity)
		}
	}

	var scanErr error
	if err := k.registryKeeper.IterateValidatorMetadata(ctx, func(addr sdk.ValAddress, md vrtypes.ValidatorMetadata) bool {
		if md.Status != vrtypes.Status_STATUS_ACTIVE {
			return false
		}
		member, err := k.Members.Has(ctx, addr)
		if err != nil {
			scanErr = err
			return true
		}
		frozen, err := k.Frozen.Has(ctx, addr)
		if err != nil {
			scanErr = err
			return true
		}
		if !member && !frozen {
			add("validator %s holds an active seat without an attestation", addr)
		}
		return false
	}); err != nil {
		return "", false, fmt.Errorf("iteration error: %w", err)
	}
	if scanErr != nil {
		return "", false, scanErr
	}

	if len(violations) == 0 {
		return "entityregistry V-12: registry consistent", false, nil
	}
	return fmt.Sprintf("entityregistry V-12 broken: %d violation(s): %s", len(violations), strings.Join(violations, "; ")), true, nil
}
