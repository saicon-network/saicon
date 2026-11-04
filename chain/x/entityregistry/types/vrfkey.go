package types

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"errors"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
)

// PopDomain is the domain prefix of the proof-of-possession input (spec §5.3).
const PopDomain = "saicon/entityregistry/vrf-pop/v1"

// proofOfPossessionHexLen is the length of a proof of possession in hex characters.
const proofOfPossessionHexLen = 2 * vrf.ProofSize

// PopAlpha is the input a proof of possession is made over (spec §5.3):
// PopDomain ‖ len16(chainID) ‖ chainID ‖ len16(validator) ‖ validator ‖ pk.
func PopAlpha(chainID string, validator sdk.ValAddress, pk []byte) []byte {
	b := make([]byte, 0, len(PopDomain)+4+len(chainID)+len(validator)+len(pk))
	b = append(b, PopDomain...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(chainID)))
	b = append(b, chainID...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(validator)))
	b = append(b, validator...)
	return append(b, pk...)
}

// DecodeVrfPublicKey decodes a VRF public key written as exactly 64 lowercase
// hex characters and checks it with vrf.ValidatePublicKey. Errors wrap
// ErrInvalidVrfKey. Keys are compared by the returned canonical bytes.
func DecodeVrfPublicKey(s string) ([]byte, error) {
	pk, err := decodeLowerHex(s, 2*vrf.PublicKeySize)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidVrfKey, "vrf_public_key: %s", err)
	}
	if err := vrf.ValidatePublicKey(pk); err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidVrfKey, "%s", err)
	}
	return pk, nil
}

// DecodeProofOfPossession decodes a proof of possession written as exactly 160
// lowercase hex characters. Errors wrap ErrInvalidProofOfPossession. The proof
// itself is verified by the message handler.
func DecodeProofOfPossession(s string) ([]byte, error) {
	pop, err := decodeLowerHex(s, proofOfPossessionHexLen)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidProofOfPossession, "proof_of_possession: %s", err)
	}
	return pop, nil
}

// decodeLowerHex decodes exactly n lowercase hex characters.
func decodeLowerHex(s string, n int) ([]byte, error) {
	if len(s) != n {
		return nil, fmt.Errorf("want %d hex characters, got %d", n, len(s))
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil, errors.New("must be lowercase hex")
		}
	}
	return hex.DecodeString(s)
}
