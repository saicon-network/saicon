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

	ErrInvalidFraction      = errors.Register(ModuleName, 1109, "coordinated-attack fraction out of bounds")
	ErrFractionMismatch     = errors.Register(ModuleName, 1110, "fraction does not equal the governed coordinated_attack_fraction")
	ErrEntityNotFound       = errors.Register(ModuleName, 1111, "entity not found")
	ErrNotArmed             = errors.Register(ModuleName, 1112, "entity is not armed")
	ErrUnknownOffence       = errors.Register(ModuleName, 1113, "unknown offence")
	ErrOffenceNotLinked     = errors.Register(ModuleName, 1114, "offence is not linked to the entity")
	ErrAlreadySanctioned    = errors.Register(ModuleName, 1115, "offence already sanctioned")
	ErrAppealWindowOpen     = errors.Register(ModuleName, 1116, "appeal window still open")
	ErrNotTombstoned        = errors.Register(ModuleName, 1117, "validator is not tombstoned")
	ErrInvalidEvidenceHash  = errors.Register(ModuleName, 1118, "invalid evidence hash")
	ErrCoordinatedInvariant = errors.Register(ModuleName, 1119, "coordinated-attack invariant V-15 broken")
	ErrInvalidSanction      = errors.Register(ModuleName, 1120, "invalid sanction message")
)
