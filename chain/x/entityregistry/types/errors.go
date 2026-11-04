package types

// DONTCOVER

import "cosmossdk.io/errors"

// x/entityregistry module sentinel errors (codes 1100-1110).
var (
	ErrInvalidSigner            = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrNotAttestor              = errors.Register(ModuleName, 1101, "signer is neither a listed attestor nor the gov authority")
	ErrInvalidEntity            = errors.Register(ModuleName, 1102, "invalid entity attestation")
	ErrEntityNotFound           = errors.Register(ModuleName, 1103, "entity not found")
	ErrEntityRevoked            = errors.Register(ModuleName, 1104, "entity attestation already revoked")
	ErrMemberOfAnotherEntity    = errors.Register(ModuleName, 1105, "validator belongs to another attested entity")
	ErrTooManyMembers           = errors.Register(ModuleName, 1106, "entity lists more validators than allowed")
	ErrInvalidVrfKey            = errors.Register(ModuleName, 1107, "invalid VRF public key")
	ErrVrfKeyInUse              = errors.Register(ModuleName, 1108, "VRF public key belongs to another validator")
	ErrVrfKeyNotOffered         = errors.Register(ModuleName, 1109, "VRF key was not offered by the validator's operator")
	ErrInvalidProofOfPossession = errors.Register(ModuleName, 1110, "invalid VRF proof of possession")
)
