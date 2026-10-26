package compliance

import (
	"cosmossdk.io/core/address"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/store"
	"cosmossdk.io/depinject"
	"cosmossdk.io/depinject/appconfig"
	"github.com/cosmos/cosmos-sdk/codec"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
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
	StakingKeeper           types.StakingKeeper
	SlashingKeeper          types.SlashingKeeper
	ParticipationKeeper     types.ParticipationKeeper
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
	)
	m := NewAppModule(in.Cdc, k, in.AuthKeeper, in.BankKeeper)

	return ModuleOutputs{
		ComplianceKeeper: k,
		Module:           m,
		Hooks:            stakingtypes.StakingHooksWrapper{StakingHooks: k.Hooks()},
	}
}
