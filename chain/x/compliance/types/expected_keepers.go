package types

import (
	"context"

	"cosmossdk.io/core/address"
	"cosmossdk.io/math"
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

// NOTE (depinject): every interface below declares at least one method that
// only the intended keeper has, and ModuleInputs names the concrete keepers
// (the house rule of x/stakeengine), so the bindings are unique.

// StakingKeeper defines the expected x/staking surface: MsgUnjail resolves the
// validator's consensus address (via GetConsAddr) to check tombstone status,
// and the coordinated-attack rule resolves a double-signer by consensus
// address and slashes through the same entry point x/evidence uses.
type StakingKeeper interface {
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	// GetValidatorByConsAddr resolves the validator behind a consensus address.
	GetValidatorByConsAddr(ctx context.Context, consAddr sdk.ConsAddress) (stakingtypes.Validator, error)
	// SlashWithInfractionReason is x/staking's slash, exactly as x/evidence reaches it.
	SlashWithInfractionReason(ctx context.Context, consAddr sdk.ConsAddress, infractionHeight, power int64,
		slashFactor math.LegacyDec, infraction stakingtypes.Infraction) (math.Int, error)
	// ConsensusAddressCodec is the codec that spells a consensus address in an Equivocation.
	ConsensusAddressCodec() address.Codec
	// TokensFromConsensusPower converts a consensus power to tokens.
	TokensFromConsensusPower(ctx context.Context, power int64) math.Int
}

// SlashingKeeper defines the expected x/slashing surface: MsgUnjail refuses a
// tombstoned (double-sign) validator, and the coordinated-attack fraction may
// not sit below the double-sign fraction in force.
type SlashingKeeper interface {
	IsTombstoned(ctx context.Context, consAddr sdk.ConsAddress) bool
	// SlashFractionDoubleSign is x/slashing's current slash_fraction_double_sign.
	SlashFractionDoubleSign(ctx context.Context) (math.LegacyDec, error)
}

// ParticipationKeeper is the subset of x/participation that a PUNISHMENT may
// touch (ADR-0010 §4, revised invariant V-8).
//
// Deliberately narrow: exactly two methods, and NOT SetEP. V-8 says punishment
// never mutates EP, and the cheapest way to keep that true is to make it
// unreachable — x/compliance cannot write EP because this interface does not
// expose a way to. The dependency runs compliance -> participation only;
// x/participation knows nothing of compliance, so there is no cycle.
type ParticipationKeeper interface {
	// ZeroReputation zeroes WARP and the participation snapshot of the given
	// epoch, if there is one. It never touches EP. The caller passes the
	// in-flight epoch, ceil(height / EpochLengthBlocks), so a closed epoch is
	// never rewritten.
	ZeroReputation(ctx context.Context, addr sdk.ValAddress, epoch uint64) error
	// EpochLengthBlocks lets compliance work out which epoch is in flight, from
	// participation's own param rather than a second copy.
	EpochLengthBlocks(ctx context.Context) (uint64, error)
}

// EvidenceReader reads x/evidence's store of accepted equivocations. A hash is
// there only if x/evidence slashed, jailed and tombstoned for it (spec §6.1).
type EvidenceReader interface {
	HasEquivocation(ctx context.Context, hash []byte) (bool, error)
}

// EntityregistryKeeper is the subset of x/entityregistry that the
// coordinated-attack rule reads: which entity a validator belonged to when it
// voted at a height (spec §6.2), and whether an entity exists.
type EntityregistryKeeper interface {
	EntityAt(ctx context.Context, val sdk.ValAddress, height int64) (string, bool, error)
	HasEntity(ctx context.Context, entityID string) (bool, error)
}

// SlashWindowKeeper is x/stakeengine's slash window: while it is open the
// staking hooks of x/stakeengine sleep, as they do in BeginBlock (spec §8.4).
type SlashWindowKeeper interface {
	OpenBeginBlockWindow(ctx context.Context) error
	CloseBeginBlockWindow(ctx context.Context) error
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
