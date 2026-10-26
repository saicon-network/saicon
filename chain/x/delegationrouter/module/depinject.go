package delegationrouter

import (
	"cosmossdk.io/core/address"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/store"
	"cosmossdk.io/depinject"
	"cosmossdk.io/depinject/appconfig"
	"github.com/cosmos/cosmos-sdk/codec"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/keeper"
	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
	participationkeeper "github.com/saicon-network/saicon/chain/x/participation/keeper"
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
	StakeengineKeeper       stakeenginekeeper.Keeper
	ParticipationKeeper     participationkeeper.Keeper
	ValidatorregistryKeeper validatorregistrykeeper.Keeper
}

type ModuleOutputs struct {
	depinject.Out

	DelegationrouterKeeper keeper.Keeper
	Module                 appmodule.AppModule
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
		in.StakeengineKeeper,
		in.StakingKeeper,
		in.ParticipationKeeper,
		in.ValidatorregistryKeeper,
	)
	return ModuleOutputs{
		DelegationrouterKeeper: k,
		Module:                 NewAppModule(in.Cdc, k),
	}
}
