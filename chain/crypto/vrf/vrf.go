// Package vrf wraps the ECVRF-EDWARDS25519-SHA512-ELL2 suite of RFC 9381 as
// implemented by curve25519-voi (draft-11-and-later semantics, which RFC 9381
// published). The pre-11 functions of the library are never used.
package vrf

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"github.com/oasisprotocol/curve25519-voi/curve"
	"github.com/oasisprotocol/curve25519-voi/primitives/ed25519"
	"github.com/oasisprotocol/curve25519-voi/primitives/ed25519/extra/ecvrf"
)

// Suite names the VRF ciphersuite of RFC 9381 used by the chain.
const Suite = "ECVRF-EDWARDS25519-SHA512-ELL2"

const (
	// PublicKeySize is the size of an encoded public key in bytes.
	PublicKeySize = ed25519.PublicKeySize
	// SeedSize is the size of a private key seed in bytes.
	SeedSize = ed25519.SeedSize
	// ProofSize is the size of a proof in bytes.
	ProofSize = ecvrf.ProofSize
	// OutputSize is the size of a VRF output in bytes.
	OutputSize = ecvrf.OutputSize
)

// VerifyGas is the gas charged for one proof verification.
const VerifyGas uint64 = 2000

var (
	// ErrInvalidPublicKey is returned for a key that is not 32 bytes, is not a
	// canonical encoding of a curve point, or is a point of small order.
	ErrInvalidPublicKey = errors.New("vrf: invalid public key")
	// ErrInvalidProof is returned for a proof that is malformed or does not
	// verify for the given key and input.
	ErrInvalidProof = errors.New("vrf: invalid proof")
)

// PrivateKey is a VRF private key. The zero value is not usable.
type PrivateKey struct {
	sk ed25519.PrivateKey
}

// NewKeyFromSeed derives the private key of a 32-byte seed.
func NewKeyFromSeed(seed []byte) (PrivateKey, error) {
	if len(seed) != SeedSize {
		return PrivateKey{}, fmt.Errorf("vrf: seed is %d bytes, want %d", len(seed), SeedSize)
	}
	return PrivateKey{sk: ed25519.NewKeyFromSeed(seed)}, nil
}

// GenerateKey creates a key from a seed read from r; a nil r means
// crypto/rand.
func GenerateKey(r io.Reader) (PrivateKey, error) {
	if r == nil {
		r = rand.Reader
	}
	seed := make([]byte, SeedSize)
	if _, err := io.ReadFull(r, seed); err != nil {
		return PrivateKey{}, fmt.Errorf("vrf: reading a seed: %w", err)
	}
	return NewKeyFromSeed(seed)
}

// Seed returns a copy of the 32-byte seed the key was derived from.
func (k PrivateKey) Seed() []byte {
	return append([]byte(nil), k.sk[:SeedSize]...)
}

// PublicKey returns a copy of the 32-byte public key.
func (k PrivateKey) PublicKey() []byte {
	return append([]byte(nil), k.sk[SeedSize:]...)
}

// Prove returns the 80-byte proof for alpha. It is deterministic: the nonce
// is the one RFC 9381 derives from the key and the input.
func (k PrivateKey) Prove(alpha []byte) []byte {
	return ecvrf.Prove(k.sk, alpha)
}

// ValidatePublicKey is RFC 9381 ECVRF_validate_key plus decoding: 32 bytes, a
// canonical encoding of a curve point, not of small order.
func ValidatePublicKey(pk []byte) error {
	if len(pk) != PublicKeySize {
		return fmt.Errorf("%w: %d bytes, want %d", ErrInvalidPublicKey, len(pk), PublicKeySize)
	}
	var c curve.CompressedEdwardsY
	if _, err := c.SetBytes(pk); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPublicKey, err)
	}
	if !c.IsCanonicalVartime() {
		return fmt.Errorf("%w: non-canonical encoding", ErrInvalidPublicKey)
	}
	var p curve.EdwardsPoint
	if _, err := p.SetCompressedY(&c); err != nil {
		return fmt.Errorf("%w: not a curve point", ErrInvalidPublicKey)
	}
	if p.IsSmallOrder() {
		return fmt.Errorf("%w: small-order point", ErrInvalidPublicKey)
	}
	return nil
}

// Verify checks a proof for alpha under pk and returns the 64-byte output. It
// never panics: the library panics if hash-to-curve fails, and this function
// runs inside EndBlock (V-14), where a panic would halt the chain.
func Verify(pk, proof, alpha []byte) (out []byte, err error) {
	if err := ValidatePublicKey(pk); err != nil {
		return nil, err
	}
	if len(proof) != ProofSize {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrInvalidProof, len(proof), ProofSize)
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%w: %v", ErrInvalidProof, r)
		}
	}()
	ok, beta := ecvrfVerify(ed25519.PublicKey(pk), proof, alpha)
	if !ok {
		return nil, ErrInvalidProof
	}
	return beta, nil
}

// ecvrfVerify is the library call, a variable only so a test can make it panic.
var ecvrfVerify = ecvrf.Verify
