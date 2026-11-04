package types

import (
	"bytes"
	"fmt"
	"regexp"
	"unicode"
	"unicode/utf8"

	"cosmossdk.io/errors"
	"github.com/cosmos/cosmos-sdk/types/bech32"
)

var entityIDPattern = regexp.MustCompile("^[a-z][a-z0-9-]{2,63}$")

// DecodeAccount returns the bytes of a saicon1… address and rejects any other prefix.
func DecodeAccount(addr string) ([]byte, error) { return decodeBech32(addr, AccountPrefix) }

// DecodeValidator returns the bytes of a saiconvaloper1… address.
func DecodeValidator(addr string) ([]byte, error) { return decodeBech32(addr, ValidatorPrefix) }

func decodeBech32(addr, want string) ([]byte, error) {
	hrp, bz, err := bech32.DecodeAndConvert(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if hrp != want {
		return nil, fmt.Errorf("address %q has prefix %q, want %q", addr, hrp, want)
	}
	if len(bz) == 0 {
		return nil, fmt.Errorf("address %q is empty", addr)
	}
	return bz, nil
}

// ValidateEntityID checks an entity id against ^[a-z][a-z0-9-]{2,63}$.
func ValidateEntityID(id string) error {
	if !entityIDPattern.MatchString(id) {
		return errors.Wrapf(ErrInvalidEntity, "entity_id %q must match %s", id, entityIDPattern)
	}
	return nil
}

// ValidateDisplayName checks a display name: 1-64 bytes of valid UTF-8 with no
// control characters.
func ValidateDisplayName(name string) error {
	if len(name) == 0 || len(name) > MaxDisplayNameBytes {
		return errors.Wrapf(ErrInvalidEntity, "display_name must be 1-%d bytes, got %d", MaxDisplayNameBytes, len(name))
	}
	if !utf8.ValidString(name) {
		return errors.Wrap(ErrInvalidEntity, "display_name is not valid UTF-8")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.Wrap(ErrInvalidEntity, "display_name contains a control character")
		}
	}
	return nil
}

// ValidateAttestationHash checks for exactly 64 lowercase hex characters.
func ValidateAttestationHash(h string) error {
	if len(h) != 64 {
		return errors.Wrapf(ErrInvalidEntity, "attestation_hash must be 64 characters, got %d", len(h))
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return errors.Wrap(ErrInvalidEntity, "attestation_hash must be lowercase hex")
		}
	}
	return nil
}

// ValidateReason checks a revocation reason: 1-256 bytes of valid UTF-8.
func ValidateReason(reason string) error {
	if len(reason) == 0 || len(reason) > MaxReasonBytes {
		return errors.Wrapf(ErrInvalidEntity, "reason must be 1-%d bytes, got %d", MaxReasonBytes, len(reason))
	}
	if !utf8.ValidString(reason) {
		return errors.Wrap(ErrInvalidEntity, "reason is not valid UTF-8")
	}
	return nil
}

// containsBytes reports whether list holds an element equal to b.
func containsBytes(list [][]byte, b []byte) bool {
	for _, x := range list {
		if bytes.Equal(x, b) {
			return true
		}
	}
	return false
}
