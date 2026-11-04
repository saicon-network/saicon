package types

import (
	"encoding/hex"
	"strings"
)

// evidenceHashLen is the length of an evidence hash in hex characters: SHA-256.
const evidenceHashLen = 64

// FormatEvidenceHash writes an evidence hash as 64 uppercase hex characters,
// the form x/evidence itself emits (spec §5.3).
func FormatEvidenceHash(b []byte) string {
	return strings.ToUpper(hex.EncodeToString(b))
}

// ParseEvidenceHash reads exactly 64 characters of [0-9A-F] into the 32 hash
// bytes. Lowercase is refused so that one hash has one spelling.
func ParseEvidenceHash(s string) ([]byte, error) {
	if len(s) != evidenceHashLen {
		return nil, ErrInvalidEvidenceHash.Wrapf("%d characters, want %d", len(s), evidenceHashLen)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') {
			return nil, ErrInvalidEvidenceHash.Wrapf("character %d is not uppercase hex", i)
		}
	}
	// Only validated characters reach here, so the decode cannot fail.
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, ErrInvalidEvidenceHash.Wrap(err.Error())
	}
	return b, nil
}
