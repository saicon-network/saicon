package types

import (
	"context"

	"cosmossdk.io/core/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	validatorregistrytypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// ValidatorregistryKeeper is the subset of the x/validatorregistry keeper that
// x/compliance depends on:
//   - SetComplianceFlag mirrors the C flag into the metadata cache (V-4.1)
//   - GetValidatorStatus reads the current status to decide a transition
//   - ChangeStatus drives the compliance-triggered status transition
//     (active→quarantined / →jailed, quarantined→active on restore)
type ValidatorregistryKeeper interface {
	SetComplianceFlag(ctx context.Context, addr sdk.ValAddress, compliant bool) error
	GetValidatorStatus(ctx context.Context, addr sdk.ValAddress) (validatorregistrytypes.Status, bool, error)
	ChangeStatus(ctx context.Context, addr sdk.ValAddress, newStatus validatorregistrytypes.Status) error
	// GetValidatorMetadata returns the full metadata (Status + LastStatusChangeHeight)
	// used by MsgUnjail for the JAILED check and the block-based cooldown.
	GetValidatorMetadata(ctx context.Context, addr sdk.ValAddress) (validatorregistrytypes.ValidatorMetadata, bool, error)
}

// StakingKeeper defines the expected x/staking surface: MsgUnjail resolves the
// validator's consensus address (via GetConsAddr) to check tombstone status.
type StakingKeeper interface {
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
}

// SlashingKeeper defines the expected x/slashing surface: MsgUnjail refuses a
// tombstoned (double-sign) validator.
type SlashingKeeper interface {
	IsTombstoned(ctx context.Context, consAddr sdk.ConsAddress) bool
}

// ParticipationKeeper is the subset of x/participation that a PUNISHMENT may
// touch (ADR-0010 §4, revised invariant V-8).
//
// Deliberately narrow: exactly one method, and NOT SetEP. V-8 says punishment
// never mutates EP, and the cheapest way to keep that true is to make it
// unreachable — x/compliance cannot write EP because this interface does not
// expose a way to. The dependency runs compliance -> participation only;
// x/participation knows nothing of compliance, so there is no cycle.
type ParticipationKeeper interface {
	// ZeroReputation zeroes the in-flight epoch's participation snapshot (and,
	// once the engine exists, WARP). It never touches EP and never rewrites a
	// closed epoch.
	ZeroReputation(ctx context.Context, addr sdk.ValAddress, epoch uint64) error
	// EpochLengthBlocks lets compliance work out which epoch is in flight, from
	// participation's own param rather than a second copy.
	EpochLengthBlocks(ctx context.Context) (uint64, error)
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
