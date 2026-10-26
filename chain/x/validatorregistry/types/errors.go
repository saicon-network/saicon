package types

// DONTCOVER

import (
	"cosmossdk.io/errors"
)

// x/validatorregistry module sentinel errors
var (
	ErrInvalidSigner              = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrInvalidTransition          = errors.Register(ModuleName, 1101, "invalid validator status transition")
	ErrValidatorAlreadyRegistered = errors.Register(ModuleName, 1102, "validator already registered")
	ErrValidatorNotFound          = errors.Register(ModuleName, 1103, "validator not found")
	ErrLockupActive               = errors.Register(ModuleName, 1104, "validator self-stake is locked")
	ErrLockupTooShort             = errors.Register(ModuleName, 1105, "requested self-stake lock-up is shorter than the chain minimum")
	ErrLockupTooLong              = errors.Register(ModuleName, 1106, "requested self-stake lock-up is longer than the chain maximum")
)
