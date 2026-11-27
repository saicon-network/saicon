package types

import (
	"context"
	"cosmossdk.io/math"

	"cosmossdk.io/core/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	compliancetypes "github.com/saicon-network/saicon/chain/x/compliance/types"
	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
	validatorregistrytypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// NOTE (depinject): every expected-keeper interface below MUST declare at least
// one concrete method. An empty interface is satisfied by every value in the
// depinject container, which makes depinject report "Multiple implementations
// found for interface ..." and panic at app wiring. Each method here is one
// that only the matching keeper implements, giving depinject a unique bind.

// StakingKeeper is the subset of x/staking x/rewards needs to iterate the
// active (bonded) validator set during emission.
type StakingKeeper interface {
	IterateBondedValidatorsByPower(ctx context.Context, fn func(index int64, validator stakingtypes.ValidatorI) (stop bool)) error
	// TotalBondedTokens is the stake-linked term of the annual security budget
	// (ADR-0011 D1): the budget tracks what the network is actually securing.
	TotalBondedTokens(ctx context.Context) (math.Int, error)
	// PowerReduction converts tokens to CometBFT power, which the
	// voting-power preview reports beside its own figure as stock_power.
	PowerReduction(ctx context.Context) math.Int
	// Validator reads one validator at payout time: x/distribution needs its
	// commission rate, and a missing validator or one with zero tokens cannot
	// be allocated to (ADR-0011 F3, S10 R3).
	Validator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.ValidatorI, error)
}

// BankKeeper is the subset of x/bank x/rewards needs to mint per-epoch rewards
// to the module account and hand each validator's share to x/distribution.
type BankKeeper interface {
	MintCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	// GetSupply is read BEFORE every mint so the 21M cap is a precondition of
	// creating SCN rather than something an invariant notices afterwards
	// (ADR-0011 D4).
	GetSupply(ctx context.Context, denom string) sdk.Coin
	// SendCoinsFromModuleToModule moves a validator's epoch reward into the
	// x/distribution module account, which must hold the coins that
	// AllocateTokensToValidator books as outstanding rewards (S10 R1). It is
	// the module's only send: with no account-facing send in this interface,
	// an operator-account payout cannot come back as a fallback (R3).
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
	// GetBalance reads the fee collector so gas revenue can OFFSET issuance
	// (ADR-0011 D2). Read-only: x/distribution owns those coins and sweeps them
	// every block; taking them here would pay the same fees twice.
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

// ParticipationKeeper is the subset of x/participation x/rewards needs to read a
// validator's accumulated Empirical Points (EP). EP>0 is part of the emission
// gate (invariant V-2: EP=0 -> reward=0).
type ParticipationKeeper interface {
	GetEP(ctx context.Context, addr sdk.ValAddress) (participationtypes.EmpiricalPointsState, bool, error)
	// EpochLengthBlocks is x/participation's epoch length (Params.EpochLengthBlocks).
	// x/rewards reads it so its EndBlock fires on the SAME epoch boundary as
	// participation's, avoiding drift from a duplicated param (WI-016).
	EpochLengthBlocks(ctx context.Context) (uint64, error)
	// GetWarp and WarpParams feed the voting-power preview (ADR-0008 §1,
	// S10 R10). Nothing in emission reads them.
	GetWarp(ctx context.Context, addr sdk.ValAddress) (participationtypes.WarpState, bool, error)
	WarpParams(ctx context.Context) (participationtypes.WarpParams, error)
}

// ValidatorregistryKeeper is the subset of x/validatorregistry x/rewards needs
// to read a validator's status (only STATUS_ACTIVE is eligible for emission).
type ValidatorregistryKeeper interface {
	GetValidatorStatus(ctx context.Context, addr sdk.ValAddress) (validatorregistrytypes.Status, bool, error)
}

// ComplianceKeeper is the subset of x/compliance x/rewards needs to read a
// validator's C flag. A missing record is treated as compliant (validators only
// get a record once flagged).
type ComplianceKeeper interface {
	GetCompliance(ctx context.Context, addr sdk.ValAddress) (compliancetypes.ComplianceState, bool, error)
}

// DistributionKeeper is the subset of x/distribution the payout leg needs
// (ADR-0011 F3, S10 R1). AllocateTokensToValidator only books: the validator's
// commission goes to its accumulated commission, the rest to its delegators'
// current rewards, and its outstanding rewards rise by the whole amount. The
// coins must already sit in the x/distribution module account.
type DistributionKeeper interface {
	AllocateTokensToValidator(ctx context.Context, val stakingtypes.ValidatorI, tokens sdk.DecCoins) error
}

// StakeengineKeeper is the subset of x/stakeengine the voting-power preview
// needs: the per-validator cap bonded stake is clipped at (ADR-0008 §1).
// Read-only, and nothing in emission reads it.
type StakeengineKeeper interface {
	ValidatorCap(ctx context.Context) (math.Int, error)
}

// AuthKeeper defines the expected interface for the Auth module.
type AuthKeeper interface {
	AddressCodec() address.Codec
	// GetModuleAddress resolves the fee collector, whose balance is the gas
	// revenue the emission offset is measured against.
	GetModuleAddress(name string) sdk.AccAddress
	GetAccount(context.Context, sdk.AccAddress) sdk.AccountI // only used for simulation
}
