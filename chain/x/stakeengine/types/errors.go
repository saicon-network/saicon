package types

// DONTCOVER

import "cosmossdk.io/errors"

// x/stakeengine module sentinel errors
var (
	ErrInvalidSigner       = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrGlobalCapReached    = errors.Register(ModuleName, 1101, "global bonded cap reached")
	ErrValidatorCapReached = errors.Register(ModuleName, 1102, "validator cap reached")
	ErrBelowEntryFloor     = errors.Register(ModuleName, 1103, "self-bond below the entry floor")
	ErrSelfBondBelowFloor  = errors.Register(ModuleName, 1104, "remaining self-bond would fall below the entry floor")
	ErrQueueFull           = errors.Register(ModuleName, 1105, "stake queue is full")
	ErrQueueEntryTooSmall  = errors.Register(ModuleName, 1106, "queue entry below the minimum")
	ErrQueueEntryNotFound  = errors.Register(ModuleName, 1107, "queue entry not found")
	ErrNotQueueOwner       = errors.Register(ModuleName, 1108, "queue entry belongs to another delegator")
	ErrCapsNotComputed     = errors.Register(ModuleName, 1109, "stake caps have not been computed")
	ErrInvalidDenom        = errors.Register(ModuleName, 1110, "invalid denom")
	ErrAuthzTooDeep        = errors.Register(ModuleName, 1111, "authz.MsgExec nested too deep to verify stake caps")
	ErrTooManyValidators   = errors.Register(ModuleName, 1112, "delegator already holds the maximum number of validators")
	ErrBelowMinDelegation  = errors.Register(ModuleName, 1113, "delegation below the minimum")
)
