package stakeengine

import (
	"cosmossdk.io/core/address"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/store"
	"cosmossdk.io/depinject"
	"cosmossdk.io/depinject/appconfig"
	"github.com/cosmos/cosmos-sdk/codec"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	participationkeeper "github.com/saicon-network/saicon/chain/x/participation/keeper"
	"github.com/saicon-network/saicon/chain/x/stakeengine/keeper"
	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
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
	// TransientStoreService backs the staking backstop (S7 spec §6).
	TransientStoreService store.TransientStoreService
	Cdc                   codec.Codec
	AddressCodec          address.Codec

	AuthKeeper types.AuthKeeper
	BankKeeper types.BankKeeper
	// Concrete keeper types, as x/rewards takes them: depinject provides staking
	// as *stakingkeeper.Keeper and the SAiCoN keepers by value, so naming them
	// gives a unique bind without explicit interface bindings.
	StakingKeeper           *stakingkeeper.Keeper
	ParticipationKeeper     participationkeeper.Keeper
	ValidatorregistryKeeper validatorregistrykeeper.Keeper
}

type ModuleOutputs struct {
	depinject.Out

	StakeengineKeeper keeper.Keeper
	Module            appmodule.AppModule
	// Hooks is the staking backstop; x/staking collects every module's hooks.
	Hooks stakingtypes.StakingHooksWrapper
}

func ProvideModule(in ModuleInputs) ModuleOutputs {
	// default to governance authority if not provided
	authority := authtypes.NewModuleAddress(types.GovModuleName)
	if in.Config.Authority != "" {
		authority = authtypes.NewModuleAddressOrBech32Address(in.Config.Authority)
	}
	k := keeper.NewKeeper(
		in.StoreService,
		in.TransientStoreService,
		in.Cdc,
		in.AddressCodec,
		authority,
		in.AuthKeeper,
		in.BankKeeper,
		in.StakingKeeper,
		in.ParticipationKeeper,
		in.ValidatorregistryKeeper,
	)
	return ModuleOutputs{
		StakeengineKeeper: k,
		Module:            NewAppModule(in.Cdc, k),
		Hooks:             stakingtypes.StakingHooksWrapper{StakingHooks: k.Hooks()},
	}
}
