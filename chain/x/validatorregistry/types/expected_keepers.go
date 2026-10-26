package types

import (
	"context"
	"time"

	"cosmossdk.io/core/address"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// StakingKeeper defines the expected interface for the Staking module.
type StakingKeeper interface {
	ConsensusAddressCodec() address.Codec
	ValidatorByConsAddr(context.Context, sdk.ConsAddress) (stakingtypes.ValidatorI, error)
	// GetUnbondingDelegation is used by the V-3 self-stake lock-up invariant to
	// detect a self-unbonding on a still-locked validator.
	GetUnbondingDelegation(ctx context.Context, delAddr sdk.AccAddress, valAddr sdk.ValAddress) (stakingtypes.UnbondingDelegation, error)
	// GetRedelegationsFromSrcValidator is used by the V-3 invariant to detect a
	// self-redelegation moving self-stake away from a still-locked validator
	// (redelegation creates a Redelegation, not an UnbondingDelegation, so it is
	// a distinct escape vector that GetUnbondingDelegation cannot see).
	GetRedelegationsFromSrcValidator(ctx context.Context, valAddr sdk.ValAddress) ([]stakingtypes.Redelegation, error)
	// GetValidator is used by the V-5 exit lifecycle: when unbonding begins, the
	// staking record already carries UnbondingTime — the instant the cooldown ends —
	// which is frozen into ValidatorMetadata.
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	// UnbondingTime is the fallback cooldown length used only when the staking
	// validator record cannot be read at begin-unbonding.
	UnbondingTime(ctx context.Context) (time.Duration, error)
	// Methods imported from account should be defined here
}

// AuthKeeper defines the expected interface for the Auth module.
type AuthKeeper interface {
	AddressCodec() address.Codec
	GetAccount(context.Context, sdk.AccAddress) sdk.AccountI // only used for simulation
	// Methods imported from account should be defined here
}

// BankKeeper defines the expected interface for the Bank module.
type BankKeeper interface {
	SpendableCoins(context.Context, sdk.AccAddress) sdk.Coins
	// Methods imported from bank should be defined here
}

// ParamSubspace defines the expected Subspace interface for parameters.
type ParamSubspace interface {
	Get(context.Context, []byte, interface{})
	Set(context.Context, []byte, interface{})
}
