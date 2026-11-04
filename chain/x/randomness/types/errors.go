package types

// DONTCOVER

import "cosmossdk.io/errors"

// x/randomness module sentinel errors (codes 1100-1106).
var (
	ErrInvalidSigner     = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrWrongRound        = errors.Register(ModuleName, 1101, "not the current round")
	ErrWindowClosed      = errors.Register(ModuleName, 1102, "the submission window is not open")
	ErrNotDesignated     = errors.Register(ModuleName, 1103, "validator is not the designated submitter of a pool entity")
	ErrAlreadySubmitted  = errors.Register(ModuleName, 1104, "the entity already submitted this round")
	ErrInvalidProof      = errors.Register(ModuleName, 1105, "invalid VRF proof")
	ErrInvalidRandomness = errors.Register(ModuleName, 1106, "invalid randomness value")
)
