package types

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// NOTE (depinject): every interface declares methods only the intended keeper
// has, so the concrete keepers bind uniquely (see x/stakeengine).

// ValidatorregistryKeeper is the registry surface the entity registry uses.
type ValidatorregistryKeeper interface {
	GetValidatorStatus(ctx context.Context, addr sdk.ValAddress) (vrtypes.Status, bool, error)
	ActivateOnBond(ctx context.Context, valAddr sdk.ValAddress) error
	IterateValidatorMetadata(ctx context.Context, cb func(addr sdk.ValAddress, md vrtypes.ValidatorMetadata) (stop bool)) error
}

// StakingKeeper is what the entity registry reads from x/staking.
type StakingKeeper interface {
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
}
