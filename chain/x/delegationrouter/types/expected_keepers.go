package types

import (
	"context"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
	validatorregistrytypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// NOTE (depinject): every interface declares methods only the intended keeper
// has, so the concrete keepers bind uniquely (see x/stakeengine).

// StakeEngineKeeper is the cap, slot and queue state the router plans against.
type StakeEngineKeeper interface {
	Headroom(ctx context.Context) (math.Int, error)
	ValidatorCap(ctx context.Context) (math.Int, error)
	ValidatorTokens(ctx context.Context, v sdk.ValAddress) (math.Int, error)
	DelegatorSlots(ctx context.Context, delegator sdk.AccAddress) ([]string, error)
	MaxValidatorsPerDelegator(ctx context.Context) (uint32, error)
	MinDelegation(ctx context.Context) (math.Int, error)
	LatestEpoch(ctx context.Context) (uint64, error)
	EnqueueRouted(ctx context.Context, delegator sdk.AccAddress, validator sdk.ValAddress, amount math.Int) (uint64, error)
}

type StakingKeeper interface {
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	GetBondedValidatorsByPower(ctx context.Context) ([]stakingtypes.Validator, error)
	Delegate(ctx context.Context, delAddr sdk.AccAddress, bondAmt math.Int, tokenSrc stakingtypes.BondStatus,
		validator stakingtypes.Validator, subtractAccount bool) (math.LegacyDec, error)
}

type ParticipationKeeper interface {
	GetSnapshot(ctx context.Context, epoch uint64, addr sdk.ValAddress) (participationtypes.ParticipationScoreSnapshot, bool, error)
}

type ValidatorregistryKeeper interface {
	GetValidatorStatus(ctx context.Context, addr sdk.ValAddress) (validatorregistrytypes.Status, bool, error)
}
