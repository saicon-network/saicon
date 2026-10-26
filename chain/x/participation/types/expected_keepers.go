package types

import (
	"context"

	"cosmossdk.io/core/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	validatorregistrytypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// ValidatorregistryKeeper is the subset of the x/validatorregistry keeper that
// x/participation depends on. GetValidatorStatus lets a later WI (status-gated
// PS/EP compute) read a validator's current status before computing PS/EP.
// WI-012's epoch tick does not consult validator status yet — status-gated
// compute is deferred.
//
// NOTE: this interface MUST declare at least one concrete method. An empty
// interface is satisfied by every value in the depinject container, which
// makes depinject report "Multiple implementations found for interface
// types.ValidatorregistryKeeper" and panic at app wiring. A method that only
// the validatorregistry keeper implements gives depinject a unique match.
type ValidatorregistryKeeper interface {
	GetValidatorStatus(ctx context.Context, addr sdk.ValAddress) (validatorregistrytypes.Status, bool, error)
}

// StakingKeeper is the subset of the x/staking keeper x/participation needs to
// iterate the active (bonded) validator set during the epoch tick (WI-012).
type StakingKeeper interface {
	IterateBondedValidatorsByPower(ctx context.Context, fn func(index int64, validator stakingtypes.ValidatorI) (stop bool)) error
	GetValidatorByConsAddr(ctx context.Context, consAddr sdk.ConsAddress) (stakingtypes.Validator, error)
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
