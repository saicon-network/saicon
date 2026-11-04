package types

import (
	"cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
)

// VrfKeyBinding is one entry of MsgAttestEntity.vrf_keys: the offered VRF key
// the attestation binds to a listed member.
type VrfKeyBinding struct {
	Validator sdk.ValAddress
	PublicKey []byte
}

// Attestation is what MsgAttestEntity asks the keeper to record.
type Attestation struct {
	EntityID        string
	DisplayName     string
	AttestationHash string
	Validators      []sdk.ValAddress
	// VrfKeys binds offered VRF keys to listed members; a staying member not
	// named keeps its key.
	VrfKeys []VrfKeyBinding
}

// Validate checks the field formats, the member count, member uniqueness and
// the VRF key bindings (each names a listed member once and holds a valid key).
// That a key was offered is the keeper's check.
func (a Attestation) Validate() error {
	if err := ValidateEntityID(a.EntityID); err != nil {
		return err
	}
	if err := ValidateDisplayName(a.DisplayName); err != nil {
		return err
	}
	if err := ValidateAttestationHash(a.AttestationHash); err != nil {
		return err
	}
	if len(a.Validators) == 0 {
		return errors.Wrap(ErrInvalidEntity, "an entity needs at least one validator")
	}
	if len(a.Validators) > MaxMembersPerEntity {
		return errors.Wrapf(ErrTooManyMembers, "%d validators, at most %d", len(a.Validators), MaxMembersPerEntity)
	}
	seen := make([][]byte, 0, len(a.Validators))
	for _, v := range a.Validators {
		if len(v) == 0 {
			return errors.Wrap(ErrInvalidEntity, "validator address is empty")
		}
		if containsBytes(seen, v) {
			return errors.Wrapf(ErrInvalidEntity, "validator %s listed twice", v)
		}
		seen = append(seen, v)
	}
	bound := make([][]byte, 0, len(a.VrfKeys))
	for _, b := range a.VrfKeys {
		if !containsBytes(seen, b.Validator) {
			return errors.Wrapf(ErrInvalidEntity, "vrf_keys names %s, which is not in validators", b.Validator)
		}
		if containsBytes(bound, b.Validator) {
			return errors.Wrapf(ErrInvalidEntity, "vrf_keys names %s twice", b.Validator)
		}
		bound = append(bound, b.Validator)
		if err := vrf.ValidatePublicKey(b.PublicKey); err != nil {
			return errors.Wrapf(ErrInvalidVrfKey, "validator %s: %s", b.Validator, err)
		}
	}
	return nil
}
