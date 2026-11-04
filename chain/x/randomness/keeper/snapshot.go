package keeper

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// openRound takes the pool snapshot and opens the window (spec §7.3, §7.4). It
// runs in a cache context written only when every read succeeded, so a failed
// snapshot leaves the round SCHEDULED and writes nothing; the caller retries it
// at the next block.
//
// The pool is every attested entity with a registry-ACTIVE member whose
// aggregate stake is within the entity cap. Each pool entry carries its
// designated submitter: the lowest-bytes ACTIVE member holding a key bound
// before the round's key cutoff, with that key copied in.
func (k Keeper) openRound(ctx context.Context, r types.Round) error {
	cctx, write := sdk.UnwrapSDKContext(ctx).CacheContext()

	// The snapshot is the only source of a pool and of designations. Whatever a
	// SCHEDULED round carries (a stale pool or index from an imported genesis)
	// is dropped first, so it can never designate a submitter.
	for _, wipe := range []func() error{
		func() error { return k.Pool.Clear(cctx, nil) },
		func() error { return k.Designated.Clear(cctx, nil) },
		func() error { return k.Submissions.Clear(cctx, nil) },
	} {
		if err := wipe(); err != nil {
			return fmt.Errorf("clearing the previous snapshot: %w", err)
		}
	}

	tickets, err := k.er.Tickets(cctx)
	if err != nil {
		return fmt.Errorf("tickets: %w", err)
	}
	var poolSize, keyed uint32
	for _, t := range tickets {
		active, err := k.activeMembers(cctx, t.Members)
		if err != nil {
			return fmt.Errorf("entity %s: %w", t.EntityId, err)
		}
		if len(active) == 0 {
			continue
		}
		standing, err := k.se.EntityStanding(cctx, active[0])
		if err != nil {
			return fmt.Errorf("entity %s standing: %w", t.EntityId, err)
		}
		if !standing.Tokens.IsNil() && !standing.Cap.IsNil() && standing.Tokens.GT(standing.Cap) {
			continue
		}
		entry := types.PoolEntry{EntityId: t.EntityId}
		designated, key, err := k.designate(cctx, active, r.KeyCutoffHeight)
		if err != nil {
			return fmt.Errorf("entity %s: %w", t.EntityId, err)
		}
		if designated != nil {
			entry.Validator = designated.String()
			entry.VrfPublicKey = key
			if err := k.Designated.Set(cctx, designated, t.EntityId); err != nil {
				return err
			}
			keyed++
		}
		if err := k.Pool.Set(cctx, t.EntityId, entry); err != nil {
			return err
		}
		poolSize++
	}

	r.Status = types.RoundStatus_ROUND_STATUS_OPEN
	if err := k.CurrentRound.Set(cctx, r); err != nil {
		return err
	}
	cctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeWindowOpened,
		sdk.NewAttribute(types.AttributeKeyRound, strconv.FormatUint(r.Number, 10)),
		sdk.NewAttribute(types.AttributeKeyPoolSize, strconv.FormatUint(uint64(poolSize), 10)),
		sdk.NewAttribute(types.AttributeKeyKeyed, strconv.FormatUint(uint64(keyed), 10)),
		sdk.NewAttribute(types.AttributeKeyRequired, strconv.FormatUint(uint64(types.Required(poolSize)), 10)),
	))
	write()
	return nil
}

// activeMembers decodes an entity's members and keeps the registry-ACTIVE ones,
// sorted by operator bytes. QUARANTINED and JAILED members are not active.
func (k Keeper) activeMembers(ctx context.Context, members []string) ([]sdk.ValAddress, error) {
	var active []sdk.ValAddress
	for _, m := range members {
		v, err := sdk.ValAddressFromBech32(m)
		if err != nil {
			return nil, fmt.Errorf("member %q: %w", m, err)
		}
		status, found, err := k.vr.GetValidatorStatus(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("status of %s: %w", m, err)
		}
		if found && status == vrtypes.Status_STATUS_ACTIVE {
			active = append(active, v)
		}
	}
	sort.Slice(active, func(i, j int) bool { return bytes.Compare(active[i], active[j]) < 0 })
	return active, nil
}

// designate returns the first of the byte-ordered active members that holds a
// well-formed key bound strictly before the cutoff (spec §6.4), with the key; a
// nil validator when none does.
func (k Keeper) designate(ctx context.Context, active []sdk.ValAddress, cutoff int64) (sdk.ValAddress, string, error) {
	for _, v := range active {
		m, found, err := k.er.EntityOf(ctx, v)
		if err != nil {
			return nil, "", fmt.Errorf("member row of %s: %w", v, err)
		}
		if !found || m.VrfPublicKey == "" || m.VrfKeyHeight >= cutoff {
			continue
		}
		pub, err := types.DecodeHex(m.VrfPublicKey, vrf.PublicKeySize)
		if err != nil || vrf.ValidatePublicKey(pub) != nil {
			continue // a malformed key never designates a submitter
		}
		return v, m.VrfPublicKey, nil
	}
	return nil, "", nil
}
