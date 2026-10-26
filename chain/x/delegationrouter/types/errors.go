package types

// DONTCOVER

import "cosmossdk.io/errors"

// x/delegationrouter module sentinel errors. A delegation below the minimum
// returns x/stakeengine's ErrBelowMinDelegation, not a copy of it.
var (
	ErrInvalidSigner      = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrPrimaryNotEligible = errors.Register(ModuleName, 1101, "the chosen validator cannot take routed stake")
	ErrNoRoute            = errors.Register(ModuleName, 1102, "no route places the whole amount")
	ErrInvalidDenom       = errors.Register(ModuleName, 1103, "invalid denom")
)
