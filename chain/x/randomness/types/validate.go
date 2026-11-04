package types

import (
	"encoding/hex"
	"fmt"

	"cosmossdk.io/errors"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
)

// Hex lengths of the byte fields (lowercase hex strings, spec §11.2).
const (
	// InputHexLen is the length of a round input and of a seed: 32 bytes.
	InputHexLen = 64
	// KeyHexLen is the length of a VRF public key.
	KeyHexLen = 2 * vrf.PublicKeySize
	// OutputHexLen is the length of a VRF output.
	OutputHexLen = 2 * vrf.OutputSize
	// ProofHexLen is the length of a VRF proof.
	ProofHexLen = 2 * vrf.ProofSize
)

// DecodeHex decodes a string written as exactly 2n lowercase hex characters.
// Errors wrap ErrInvalidRandomness.
func DecodeHex(s string, n int) ([]byte, error) {
	if len(s) != 2*n {
		return nil, errors.Wrapf(ErrInvalidRandomness, "want %d lowercase hex characters, got %d", 2*n, len(s))
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil, errors.Wrap(ErrInvalidRandomness, "must be lowercase hex")
		}
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, errors.Wrap(ErrInvalidRandomness, err.Error())
	}
	return b, nil
}

// ValidateRound checks one round's own consistency (spec §14): a live status,
// a number of at least 1, a well-formed input and, once scheduled, the height
// order key_cutoff_height ≤ window_open_height < rotation_height. An
// unscheduled round has no window and is SCHEDULED.
func ValidateRound(r Round) error {
	if r.Status != RoundStatus_ROUND_STATUS_SCHEDULED && r.Status != RoundStatus_ROUND_STATUS_OPEN {
		return fmt.Errorf("round %d has status %s", r.Number, r.Status)
	}
	if r.Number < 1 {
		return fmt.Errorf("round number must be at least 1, got %d", r.Number)
	}
	if _, err := DecodeHex(r.Input, InputHexLen/2); err != nil {
		return fmt.Errorf("round %d input: %w", r.Number, err)
	}
	if r.KeyCutoffHeight < 0 {
		return fmt.Errorf("round %d has negative key_cutoff_height %d", r.Number, r.KeyCutoffHeight)
	}
	if r.RotationHeight < 0 || r.WindowOpenHeight < 0 {
		return fmt.Errorf("round %d has a negative height", r.Number)
	}
	if r.RotationHeight == 0 {
		if r.WindowOpenHeight != 0 {
			return fmt.Errorf("round %d is unscheduled but has window_open_height %d", r.Number, r.WindowOpenHeight)
		}
		if r.Status != RoundStatus_ROUND_STATUS_SCHEDULED {
			return fmt.Errorf("round %d is %s without a rotation height", r.Number, r.Status)
		}
		return nil
	}
	if r.KeyCutoffHeight > r.WindowOpenHeight || r.WindowOpenHeight >= r.RotationHeight {
		return fmt.Errorf("round %d heights must satisfy key_cutoff %d <= window_open %d < rotation %d",
			r.Number, r.KeyCutoffHeight, r.WindowOpenHeight, r.RotationHeight)
	}
	return nil
}
