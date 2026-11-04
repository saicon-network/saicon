package keeper

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

func isNotFound(err error) bool { return errors.Is(err, collections.ErrNotFound) }

// InitGenesis restores the module's state. Addresses are decoded with the
// global bech32 config, like every keeper. The MembersByEntity index is derived
// from the members. The stateless checks are GenesisState.Validate's, which the
// SDK does not run at InitChain: only the CLI (`saicond genesis validate`)
// calls it, so a genesis that skipped the CLI reaches this function unchecked.
// Whether every registry-ACTIVE validator is a member or frozen needs the
// registry's state, so V-12 checks it at the first EndBlock (spec §15).
func (k Keeper) InitGenesis(ctx context.Context, gs types.GenesisState) error {
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}
	for _, e := range gs.Entities {
		if err := k.Entities.Set(ctx, e.Id, e); err != nil {
			return fmt.Errorf("entityregistry genesis: entity %s: %w", e.Id, err)
		}
	}
	for _, m := range gs.Members {
		addr, err := sdk.ValAddressFromBech32(m.Validator)
		if err != nil {
			return fmt.Errorf("entityregistry genesis: member %s: %w", m.Validator, err)
		}
		if err := k.Members.Set(ctx, addr, m); err != nil {
			return err
		}
		if err := k.MembersByEntity.Set(ctx, collections.Join(m.EntityId, []byte(addr))); err != nil {
			return err
		}
	}
	for _, f := range gs.Frozen {
		addr, err := sdk.ValAddressFromBech32(f.Validator)
		if err != nil {
			return fmt.Errorf("entityregistry genesis: frozen %s: %w", f.Validator, err)
		}
		if err := k.Frozen.Set(ctx, addr, f); err != nil {
			return err
		}
	}
	for _, p := range gs.Pending {
		addr, err := sdk.ValAddressFromBech32(p)
		if err != nil {
			return fmt.Errorf("entityregistry genesis: pending %s: %w", p, err)
		}
		if err := k.Pending.Set(ctx, addr); err != nil {
			return err
		}
	}
	for _, o := range gs.VrfKeyOffers {
		addr, err := sdk.ValAddressFromBech32(o.Validator)
		if err != nil {
			return fmt.Errorf("entityregistry genesis: vrf key offer %s: %w", o.Validator, err)
		}
		if err := k.VrfKeyOffers.Set(ctx, addr, o); err != nil {
			return err
		}
	}
	for _, o := range gs.VrfKeyOwners {
		pk, err := types.DecodeVrfPublicKey(o.VrfPublicKey)
		if err != nil {
			return fmt.Errorf("entityregistry genesis: vrf key owner: %w", err)
		}
		addr, err := sdk.ValAddressFromBech32(o.Validator)
		if err != nil {
			return fmt.Errorf("entityregistry genesis: vrf key owner %s: %w", o.Validator, err)
		}
		if err := k.VrfKeyOwners.Set(ctx, pk, addr); err != nil {
			return err
		}
	}
	for _, d := range gs.Departures {
		addr, err := sdk.ValAddressFromBech32(d.Validator)
		if err != nil {
			return fmt.Errorf("entityregistry genesis: departure %s: %w", d.Validator, err)
		}
		if err := k.Departures.Set(ctx, addr, d); err != nil {
			return err
		}
	}
	return nil
}

// ExportGenesis returns the module's state. Entities are walked by id and
// members, frozen seats, pending validators, key offers, key owners and departures by key bytes, so two nodes
// export byte-identical genesis.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	gs := types.DefaultGenesis()
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.Params = params
	if err := k.Entities.Walk(ctx, nil, func(_ string, e types.Entity) (bool, error) {
		gs.Entities = append(gs.Entities, e)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Members.Walk(ctx, nil, func(_ []byte, m types.Member) (bool, error) {
		gs.Members = append(gs.Members, m)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Frozen.Walk(ctx, nil, func(_ []byte, f types.FrozenSeat) (bool, error) {
		gs.Frozen = append(gs.Frozen, f)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Pending.Walk(ctx, nil, func(key []byte) (bool, error) {
		gs.Pending = append(gs.Pending, sdk.ValAddress(key).String())
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.VrfKeyOffers.Walk(ctx, nil, func(_ []byte, o types.VrfKeyOffer) (bool, error) {
		gs.VrfKeyOffers = append(gs.VrfKeyOffers, o)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.VrfKeyOwners.Walk(ctx, nil, func(pk, owner []byte) (bool, error) {
		gs.VrfKeyOwners = append(gs.VrfKeyOwners, types.VrfKeyOwner{
			VrfPublicKey: hex.EncodeToString(pk), Validator: sdk.ValAddress(owner).String(),
		})
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Departures.Walk(ctx, nil, func(_ []byte, d types.Departure) (bool, error) {
		gs.Departures = append(gs.Departures, d)
		return false, nil
	}); err != nil {
		return nil, err
	}
	return gs, nil
}
