package types

import (
	"context"
	"time"

	"cosmossdk.io/core/address"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
	validatorregistrytypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// NOTE (depinject): every interface declares methods only the intended keeper
// has, so depinject finds exactly one implementation (see x/rewards).

type AuthKeeper interface {
	AddressCodec() address.Codec
	GetModuleAddress(name string) sdk.AccAddress
}

type BankKeeper interface {
	GetSupply(ctx context.Context, denom string) sdk.Coin
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
}

type StakingKeeper interface {
	TotalBondedTokens(ctx context.Context) (math.Int, error)
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	GetDelegation(ctx context.Context, delAddr sdk.AccAddress, valAddr sdk.ValAddress) (stakingtypes.Delegation, error)
	GetDelegatorDelegations(ctx context.Context, delegator sdk.AccAddress, maxRetrieve uint16) ([]stakingtypes.Delegation, error)
	GetUnbondingDelegations(ctx context.Context, delegator sdk.AccAddress, maxRetrieve uint16) ([]stakingtypes.UnbondingDelegation, error)
	Delegate(ctx context.Context, delAddr sdk.AccAddress, bondAmt math.Int, tokenSrc stakingtypes.BondStatus,
		validator stakingtypes.Validator, subtractAccount bool) (math.LegacyDec, error)
}

type ParticipationKeeper interface {
	EpochLengthBlocks(ctx context.Context) (uint64, error)
	GetSnapshot(ctx context.Context, epoch uint64, addr sdk.ValAddress) (participationtypes.ParticipationScoreSnapshot, bool, error)
	GetEP(ctx context.Context, addr sdk.ValAddress) (participationtypes.EmpiricalPointsState, bool, error)
}

type ValidatorregistryKeeper interface {
	GetValidatorStatus(ctx context.Context, addr sdk.ValAddress) (validatorregistrytypes.Status, bool, error)
	SelfStakeLockup(ctx context.Context, valAddr sdk.ValAddress) (locked bool, unlockAt time.Time, err error)
}
