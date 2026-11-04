package types

import (
	entityregistrytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

// ParseEvidenceHashes reads the evidence hashes of a sanction: 1 to
// MaxEvidenceHashesPerSanction of them, each 64 uppercase hex characters, none
// repeated. The count is ErrInvalidSanction; a malformed or repeated hash is
// ErrInvalidEvidenceHash. Both ValidateBasic and the sanction plan use it, so a
// preview that never saw ValidateBasic cannot name one offence twice.
func ParseEvidenceHashes(hashes []string) ([][]byte, error) {
	if len(hashes) == 0 || len(hashes) > MaxEvidenceHashesPerSanction {
		return nil, ErrInvalidSanction.Wrapf("%d evidence hashes, want 1 to %d", len(hashes), MaxEvidenceHashesPerSanction)
	}
	out := make([][]byte, 0, len(hashes))
	seen := make(map[string]bool, len(hashes))
	for i, h := range hashes {
		b, err := ParseEvidenceHash(h)
		if err != nil {
			return nil, ErrInvalidEvidenceHash.Wrapf("hash %d: %s", i, err)
		}
		if seen[string(b)] {
			return nil, ErrInvalidEvidenceHash.Wrapf("hash %d (%s) is named twice", i, h)
		}
		seen[string(b)] = true
		out = append(out, b)
	}
	return out, nil
}

// ValidateBasic is the stateless check of spec §7.2: an authority, a well-formed
// entity id, 1 to 16 distinct uppercase-hex hashes, a fraction inside the code
// bounds [0.10, 1.00] and a reason of at most 256 bytes. The stateful checks
// (the governed fraction, the armed entity, the appeal window) are the plan's.
func (m *MsgFireCoordinatedSanction) ValidateBasic() error {
	if m.Authority == "" {
		return ErrInvalidSanction.Wrap("authority is empty")
	}
	if err := entityregistrytypes.ValidateEntityID(m.EntityId); err != nil {
		return ErrInvalidSanction.Wrap(err.Error())
	}
	if _, err := ParseEvidenceHashes(m.EvidenceHashes); err != nil {
		return err
	}
	if err := ValidateCoordinatedFraction(m.Fraction); err != nil {
		return err
	}
	if len(m.Reason) > MaxSanctionReasonBytes {
		return ErrInvalidSanction.Wrapf("reason is %d bytes, at most %d", len(m.Reason), MaxSanctionReasonBytes)
	}
	return nil
}
