package types

// DONTCOVER

import (
	"cosmossdk.io/errors"
)

// x/compliance module sentinel errors
var (
	ErrInvalidSigner    = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrInvalidEvent     = errors.Register(ModuleName, 1101, "invalid compliance event type")
	ErrInvalidValidator = errors.Register(ModuleName, 1102, "invalid validator address")

	ErrNotJailed           = errors.Register(ModuleName, 1103, "validator is not jailed")
	ErrUnjailCooldown      = errors.Register(ModuleName, 1104, "unjail cooldown has not elapsed")
	ErrValidatorTombstoned = errors.Register(ModuleName, 1105, "validator is tombstoned (double-sign is irreversible)")
	ErrValidatorNotFound   = errors.Register(ModuleName, 1106, "validator not found in staking")
	ErrInvalidParams       = errors.Register(ModuleName, 1107, "invalid compliance params")

	ErrJailedRequiresUnjail = errors.Register(ModuleName, 1108, "validator is jailed; use MsgUnjail (the cooldown and tombstone gates apply there)")
)
