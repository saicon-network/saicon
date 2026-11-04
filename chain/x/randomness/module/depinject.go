package randomness

import (
	"cosmossdk.io/core/address"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/store"
	"cosmossdk.io/depinject"
	"cosmossdk.io/depinject/appconfig"
	"github.com/cosmos/cosmos-sdk/codec"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	entityregistrykeeper "github.com/saicon-network/saicon/chain/x/entityregistry/keeper"
	participationkeeper "github.com/saicon-network/saicon/chain/x/participation/keeper"
	"github.com/saicon-network/saicon/chain/x/randomness/keeper"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
	stakeenginekeeper "github.com/saicon-network/saicon/chain/x/stakeengine/keeper"
	validatorregistrykeeper "github.com/saicon-network/saicon/chain/x/validatorregistry/keeper"
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

// ModuleInputs are the module's depinject dependencies.
type ModuleInputs struct {
	depinject.In

	Config       *types.Module
	StoreService store.KVStoreService
	Cdc          codec.Codec
	AddressCodec address.Codec

	// Concrete keeper types, as x/stakeengine takes them: the SAiCoN keepers
	// are provided by value, so naming them gives a unique bind without
	// explicit interface bindings.
	EntityregistryKeeper    entityregistrykeeper.Keeper
	ValidatorregistryKeeper validatorregistrykeeper.Keeper
	StakeengineKeeper       stakeenginekeeper.Keeper
	ParticipationKeeper     participationkeeper.Keeper
}

// ModuleOutputs are what the module provides to the app.
type ModuleOutputs struct {
	depinject.Out

	RandomnessKeeper keeper.Keeper
	Module           appmodule.AppModule
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
		in.EntityregistryKeeper,
		in.ValidatorregistryKeeper,
		in.StakeengineKeeper,
		in.ParticipationKeeper,
	)
	return ModuleOutputs{
		RandomnessKeeper: k,
		Module:           NewAppModule(in.Cdc, k),
	}
}
