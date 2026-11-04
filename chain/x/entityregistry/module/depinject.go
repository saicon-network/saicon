package entityregistry

import (
	"cosmossdk.io/core/address"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/store"
	"cosmossdk.io/depinject"
	"cosmossdk.io/depinject/appconfig"
	"github.com/cosmos/cosmos-sdk/codec"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"

	"github.com/saicon-network/saicon/chain/x/entityregistry/keeper"
	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	validatorregistrykeeper "github.com/saicon-network/saicon/chain/x/validatorregistry/keeper"
)

var _ depinject.OnePerModuleType = AppModule{}

// IsOnePerModuleType implements the depinject.OnePerModuleType interface.
func (AppModule) IsOnePerModuleType() {}

func init() {
	appconfig.Register(
		&types.Module{},
		appconfig.Provide(ProvideModule),
		appconfig.Invoke(InvokeSetActivationGate),
	)
}

// ModuleInputs are the module's depinject dependencies.
type ModuleInputs struct {
	depinject.In

	Config       *types.Module
	StoreService store.KVStoreService
	Cdc          codec.Codec
	AddressCodec address.Codec

	// Concrete keeper types, as x/stakeengine takes them: depinject provides
	// staking as *stakingkeeper.Keeper and the SAiCoN keepers by value, so naming
	// them gives a unique bind without explicit interface bindings.
	StakingKeeper           *stakingkeeper.Keeper
	ValidatorregistryKeeper validatorregistrykeeper.Keeper
}

// ModuleOutputs are what the module provides to the app.
type ModuleOutputs struct {
	depinject.Out

	EntityregistryKeeper keeper.Keeper
	Module               appmodule.AppModule
}

// ProvideModule builds the keeper and the module; the authority defaults to gov.
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
	)
	return ModuleOutputs{
		EntityregistryKeeper: k,
		Module:               NewAppModule(in.Cdc, k),
	}
}

// InvokeSetActivationGate installs this module as x/validatorregistry's
// activation gate (ADR-0014 D1). It runs after every provider, so the registry
// keeper exists; the gate pointer it writes is shared by all copies of it.
func InvokeSetActivationGate(vr validatorregistrykeeper.Keeper, k keeper.Keeper) {
	vr.SetActivationGate(k)
}
