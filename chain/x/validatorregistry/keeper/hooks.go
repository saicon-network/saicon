package keeper

import (
	"context"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// Hooks wraps the validatorregistry Keeper to implement stakingtypes.StakingHooks.
// The only hook that does work is AfterValidatorBonded, which calls ActivateOnBond;
// all other hooks are no-ops.
type Hooks struct {
	k Keeper
}

var _ stakingtypes.StakingHooks = Hooks{}

// Hooks returns the staking hooks for the validatorregistry module.
func (k Keeper) Hooks() Hooks {
	return Hooks{k: k}
}

// AfterValidatorBonded activates the validator in the registry when it bonds —
// and, since the V-5 follow-up, resumes service for one whose exit was aborted.
//
// The error is LOGGED, NOT RETURNED. Unlike AfterValidatorRemoved, whose error
// staking merely logs, an error returned from this hook PROPAGATES: staking's
// bondValidator returns it up through ApplyAndReturnValidatorSetUpdates into
// BlockValidatorUpdates and out of the EndBlocker, halting the network. That was
// tolerable while this hook only ran on a validator's first bond; it now also
// runs on every recovery from a downtime jail — the most common validator event
// on any Cosmos chain — so a transient store error would take the chain down
// during precisely the incident it is meant to help the operator recover from.
//
// The trade-off is explicit: a failure here leaves the registry status stale
// (the validator bonded and signing while the registry still says UNBONDING or
// BONDING) rather than halting consensus. That is the same bargain staking
// itself strikes for AfterValidatorRemoved, and the failure is loud in the logs.
// A stale status costs that validator its rewards until an operator intervenes;
// a halt costs the whole network its liveness.
func (h Hooks) AfterValidatorBonded(ctx context.Context, _ sdk.ConsAddress, valAddr sdk.ValAddress) error {
	if err := h.k.ActivateOnBond(ctx, valAddr); err != nil {
		sdk.UnwrapSDKContext(ctx).Logger().Error(
			"validatorregistry: activation on bond failed; registry status is now stale for this validator",
			"validator", valAddr.String(), "err", err,
		)
	}
	return nil
}

// The remaining StakingHooks methods are no-ops for the validatorregistry module.

// AfterValidatorCreated fires when x/staking creates a validator — including a
// re-creation at an address a previous incarnation used, since removal deleted the
// staking keys. V-5 follow-up: discard any stale registry record so re-onboarding
// is a genuinely fresh registration instead of a permanently-INACTIVE ghost.
func (h Hooks) AfterValidatorCreated(ctx context.Context, valAddr sdk.ValAddress) error {
	return h.k.ResetOnValidatorCreated(ctx, valAddr)
}

func (h Hooks) BeforeValidatorModified(_ context.Context, _ sdk.ValAddress) error {
	return nil
}

// AfterValidatorRemoved fires when x/staking deletes a fully-unbonded validator
// (it guards on zero tokens, so the unbonding period has matured). V-5: this is
// the single trigger that moves a validator to STATUS_INACTIVE.
//
// staking only LOGS an error returned here, it does not propagate it — a failure
// would leave the status stale, which UnstakeCooldownInvariant is there to catch.
func (h Hooks) AfterValidatorRemoved(ctx context.Context, _ sdk.ConsAddress, valAddr sdk.ValAddress) error {
	return h.k.CompleteExit(ctx, valAddr)
}

// AfterValidatorBeginUnbonding fires on every bonded→unbonding transition,
// including a jail. V-5: record the exit timing (always) and move ACTIVE or
// QUARANTINED to STATUS_UNBONDING, leaving a JAILED validator jailed.
func (h Hooks) AfterValidatorBeginUnbonding(ctx context.Context, _ sdk.ConsAddress, valAddr sdk.ValAddress) error {
	return h.k.BeginUnbonding(ctx, valAddr)
}

func (h Hooks) BeforeValidatorSlashed(_ context.Context, _ sdk.ValAddress, _ math.LegacyDec) error {
	return nil
}

func (h Hooks) BeforeDelegationCreated(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) BeforeDelegationSharesModified(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) BeforeDelegationRemoved(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterDelegationModified(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterUnbondingInitiated(_ context.Context, _ uint64) error {
	return nil
}
