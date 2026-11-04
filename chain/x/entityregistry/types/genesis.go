package types

import "fmt"

// DefaultGenesis returns an empty registry with the default parameters.
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:   DefaultParams(),
		Entities: []Entity{},
		Members:  []Member{},
		Frozen:   []FrozenSeat{},
		Pending:  []string{},

		VrfKeyOffers: []VrfKeyOffer{},
		VrfKeyOwners: []VrfKeyOwner{},
		Departures:   []Departure{},
	}
}

// Validate performs the stateless genesis checks (spec §15). That every
// registry-ACTIVE validator is a member or frozen needs the other module's
// state; V-12 checks it at the first EndBlock.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}

	status := make(map[string]EntityStatus, len(gs.Entities))
	for _, e := range gs.Entities {
		if err := ValidateEntityID(e.Id); err != nil {
			return err
		}
		if _, dup := status[e.Id]; dup {
			return fmt.Errorf("entity %q appears twice", e.Id)
		}
		if err := ValidateDisplayName(e.DisplayName); err != nil {
			return fmt.Errorf("entity %q: %w", e.Id, err)
		}
		if err := ValidateAttestationHash(e.AttestationHash); err != nil {
			return fmt.Errorf("entity %q: %w", e.Id, err)
		}
		if _, err := DecodeAccount(e.Attestor); err != nil {
			return fmt.Errorf("entity %q attestor: %w", e.Id, err)
		}
		if e.Status != EntityStatus_ENTITY_STATUS_ATTESTED && e.Status != EntityStatus_ENTITY_STATUS_REVOKED {
			return fmt.Errorf("entity %q has status %s", e.Id, e.Status)
		}
		status[e.Id] = e.Status
	}

	// The owner index first: member and offer keys are checked against it.
	owners := make(map[string]string, len(gs.VrfKeyOwners)) // key bytes -> validator bytes
	for _, o := range gs.VrfKeyOwners {
		pk, err := DecodeVrfPublicKey(o.VrfPublicKey)
		if err != nil {
			return fmt.Errorf("vrf key owner: %w", err)
		}
		v, err := DecodeValidator(o.Validator)
		if err != nil {
			return fmt.Errorf("vrf key owner: %w", err)
		}
		if _, dup := owners[string(pk)]; dup {
			return fmt.Errorf("vrf key %s has two owner entries", o.VrfPublicKey)
		}
		owners[string(pk)] = string(v)
	}

	seen := make(map[string]bool, len(gs.Members))
	count := make(map[string]int, len(gs.Entities))
	for _, m := range gs.Members {
		// Keys are decoded bytes: bech32 accepts an all-uppercase spelling of
		// the same address, which must count as the same validator.
		addr, err := DecodeValidator(m.Validator)
		if err != nil {
			return fmt.Errorf("member: %w", err)
		}
		st, ok := status[m.EntityId]
		if !ok {
			return fmt.Errorf("member %s names unknown entity %q", m.Validator, m.EntityId)
		}
		if st != EntityStatus_ENTITY_STATUS_ATTESTED {
			return fmt.Errorf("member %s names entity %q, which is not attested", m.Validator, m.EntityId)
		}
		if seen[string(addr)] {
			return fmt.Errorf("member %s appears twice", m.Validator)
		}
		seen[string(addr)] = true
		count[m.EntityId]++
		if m.JoinedHeight < 0 {
			return fmt.Errorf("member %s has negative joined_height %d", m.Validator, m.JoinedHeight)
		}
		if m.VrfKeyHeight < 0 {
			return fmt.Errorf("member %s has negative vrf_key_height %d", m.Validator, m.VrfKeyHeight)
		}
		if m.VrfPublicKey == "" {
			if m.VrfKeyHeight != 0 {
				return fmt.Errorf("member %s has vrf_key_height %d without a key", m.Validator, m.VrfKeyHeight)
			}
		} else if err := checkKeyOwner(owners, m.VrfPublicKey, addr); err != nil {
			return fmt.Errorf("member %s: %w", m.Validator, err)
		}
	}
	for _, e := range gs.Entities {
		n := count[e.Id]
		switch e.Status {
		case EntityStatus_ENTITY_STATUS_ATTESTED:
			if n < 1 || n > MaxMembersPerEntity {
				return fmt.Errorf("attested entity %q has %d members, want 1-%d", e.Id, n, MaxMembersPerEntity)
			}
		case EntityStatus_ENTITY_STATUS_REVOKED:
			if n != 0 {
				return fmt.Errorf("revoked entity %q has %d members", e.Id, n)
			}
		}
	}

	frozen := make(map[string]bool, len(gs.Frozen))
	for _, f := range gs.Frozen {
		addr, err := DecodeValidator(f.Validator)
		if err != nil {
			return fmt.Errorf("frozen: %w", err)
		}
		if err := ValidateEntityID(f.FormerEntityId); err != nil {
			return fmt.Errorf("frozen validator %s former_entity_id: %w", f.Validator, err)
		}
		if frozen[string(addr)] {
			return fmt.Errorf("frozen validator %s appears twice", f.Validator)
		}
		frozen[string(addr)] = true
		if seen[string(addr)] {
			return fmt.Errorf("frozen validator %s is also a member", f.Validator)
		}
	}

	departed := make(map[string]bool, len(gs.Departures))
	for _, d := range gs.Departures {
		addr, err := DecodeValidator(d.Validator)
		if err != nil {
			return fmt.Errorf("departure: %w", err)
		}
		if err := ValidateEntityID(d.EntityId); err != nil {
			return fmt.Errorf("departure of %s entity_id: %w", d.Validator, err)
		}
		if departed[string(addr)] {
			return fmt.Errorf("departure of %s appears twice", d.Validator)
		}
		departed[string(addr)] = true
		if err := validateDeparture(d); err != nil {
			return fmt.Errorf("departure of %s: %w", d.Validator, err)
		}
	}

	pending := make(map[string]bool, len(gs.Pending))
	for _, p := range gs.Pending {
		addr, err := DecodeValidator(p)
		if err != nil {
			return fmt.Errorf("pending: %w", err)
		}
		if pending[string(addr)] {
			return fmt.Errorf("pending validator %s appears twice", p)
		}
		pending[string(addr)] = true
	}

	offered := make(map[string]bool, len(gs.VrfKeyOffers))
	for _, o := range gs.VrfKeyOffers {
		addr, err := DecodeValidator(o.Validator)
		if err != nil {
			return fmt.Errorf("vrf key offer: %w", err)
		}
		if offered[string(addr)] {
			return fmt.Errorf("validator %s has two vrf key offers", o.Validator)
		}
		offered[string(addr)] = true
		if err := checkKeyOwner(owners, o.VrfPublicKey, addr); err != nil {
			return fmt.Errorf("vrf key offer of %s: %w", o.Validator, err)
		}
	}
	return nil
}

// checkKeyOwner decodes a VRF public key and checks that the owner index names
// the given validator for it.
func checkKeyOwner(owners map[string]string, key string, validator []byte) error {
	pk, err := DecodeVrfPublicKey(key)
	if err != nil {
		return err
	}
	owner, ok := owners[string(pk)]
	if !ok {
		return fmt.Errorf("vrf key %s has no owner entry", key)
	}
	if owner != string(validator) {
		return fmt.Errorf("vrf key %s is owned by another validator", key)
	}
	return nil
}

// validateDeparture checks the heights and the charge of one departure row.
func validateDeparture(d Departure) error {
	switch {
	case d.JoinedHeight < 0:
		return fmt.Errorf("negative joined_height %d", d.JoinedHeight)
	case d.LeftHeight < d.JoinedHeight:
		return fmt.Errorf("left_height %d is below joined_height %d", d.LeftHeight, d.JoinedHeight)
	case d.ChargedUntil != 0 && !d.Frozen:
		return fmt.Errorf("charged_until %d without a frozen seat", d.ChargedUntil)
	case d.ChargedUntil < 0 || (d.ChargedUntil > 0 && d.ChargedUntil < d.LeftHeight):
		return fmt.Errorf("charged_until %d is below left_height %d", d.ChargedUntil, d.LeftHeight)
	}
	return nil
}
