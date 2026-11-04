package compliance

import (
	"context"

	"cosmossdk.io/core/address"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/store"
	"cosmossdk.io/depinject"
	"cosmossdk.io/depinject/appconfig"
	evidencekeeper "cosmossdk.io/x/evidence/keeper"
	"github.com/cosmos/cosmos-sdk/codec"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	slashingkeeper "github.com/cosmos/cosmos-sdk/x/slashing/keeper"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
	entityregistrykeeper "github.com/saicon-network/saicon/chain/x/entityregistry/keeper"
	stakeenginekeeper "github.com/saicon-network/saicon/chain/x/stakeengine/keeper"
)

var _ depinject.OnePerModuleType = AppModule{}

// IsOnePerModuleType implements the depinject.OnePerModuleType interface.
func (AppModule) IsOnePerModuleType() {}

func init() {
	appconfig.Register(
		&types.Module{},
		appconfig.Provide(ProvideModule),
	)
}

type ModuleInputs struct {
	depinject.In

	Config       *types.Module
	StoreService store.KVStoreService
	Cdc          codec.Codec
	AddressCodec address.Codec

	AuthKeeper              types.AuthKeeper
	BankKeeper              types.BankKeeper
	ValidatorregistryKeeper types.ValidatorregistryKeeper
	ParticipationKeeper     types.ParticipationKeeper
	// Concrete keeper types, as x/stakeengine takes them: depinject provides
	// staking as *stakingkeeper.Keeper and the other keepers by value, so naming
	// them gives a unique bind without explicit interface bindings.
	StakingKeeper        *stakingkeeper.Keeper
	SlashingKeeper       slashingkeeper.Keeper
	EvidenceKeeper       evidencekeeper.Keeper
	EntityregistryKeeper entityregistrykeeper.Keeper
	StakeengineKeeper    stakeenginekeeper.Keeper
}

type ModuleOutputs struct {
	depinject.Out

	ComplianceKeeper keeper.Keeper
	Module           appmodule.AppModule
	Hooks            stakingtypes.StakingHooksWrapper
}

func ProvideModule(in ModuleInputs) ModuleOutputs {
	// default to governance authority if not provided
	authority := authtypes.NewModuleAddress(types.GovModuleName)
	if in.Config.Authority != "" {
		authority = authtypes.NewModuleAddressOrBech32Address(in.Config.Authority)
	}
	k := keeper.NewKeeper(
		in.StoreService,
		in.Cdc,
		in.AddressCodec,
		authority,
		in.ValidatorregistryKeeper,
		in.StakingKeeper,
		in.SlashingKeeper,
		in.ParticipationKeeper,
		evidenceStore{in.EvidenceKeeper},
		in.EntityregistryKeeper,
		in.StakeengineKeeper,
	)
	m := NewAppModule(in.Cdc, k, in.AuthKeeper, in.BankKeeper)

	return ModuleOutputs{
		ComplianceKeeper: k,
		Module:           m,
		Hooks:            stakingtypes.StakingHooksWrapper{StakingHooks: k.Hooks()},
	}
}

// evidenceStore reads x/evidence's store of accepted equivocations: the hash is
// there only if x/evidence slashed, jailed and tombstoned for it.
type evidenceStore struct{ k evidencekeeper.Keeper }

// HasEquivocation reports whether x/evidence stored an equivocation under hash.
func (s evidenceStore) HasEquivocation(ctx context.Context, hash []byte) (bool, error) {
	return s.k.Evidences.Has(ctx, hash)
}
