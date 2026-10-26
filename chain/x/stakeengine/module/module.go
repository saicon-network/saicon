package stakeengine

import (
	"context"
	"encoding/json"
	"fmt"

	"cosmossdk.io/core/appmodule"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"google.golang.org/grpc"

	"github.com/saicon-network/saicon/chain/x/stakeengine/keeper"
	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

var (
	_ module.AppModuleBasic = (*AppModule)(nil)
	_ module.AppModule      = (*AppModule)(nil) //nolint:staticcheck // the legacy interface is what module.Manager still takes in SDK v0.53
	_ module.HasGenesis     = (*AppModule)(nil)

	_ appmodule.AppModule       = (*AppModule)(nil)
	_ appmodule.HasEndBlocker   = (*AppModule)(nil)
	_ appmodule.HasPreBlocker   = (*AppModule)(nil)
	_ appmodule.HasBeginBlocker = (*AppModule)(nil)
)

// AppModule is the stake engine: the entry floor, the global and per-validator
// caps, and the stake queue (ADR-0012).
type AppModule struct {
	cdc    codec.Codec
	keeper keeper.Keeper
}

func NewAppModule(cdc codec.Codec, keeper keeper.Keeper) AppModule {
	return AppModule{cdc: cdc, keeper: keeper}
}

// IsAppModule implements the appmodule.AppModule interface.
func (AppModule) IsAppModule() {}

// Name returns the name of the module as a string.
func (AppModule) Name() string { return types.ModuleName }

// RegisterLegacyAminoCodec registers the amino codec
func (AppModule) RegisterLegacyAminoCodec(*codec.LegacyAmino) {}

// RegisterGRPCGatewayRoutes registers the gRPC Gateway routes for the module.
func (AppModule) RegisterGRPCGatewayRoutes(clientCtx client.Context, mux *runtime.ServeMux) {
	if err := types.RegisterQueryHandlerClient(clientCtx.CmdContext, mux, types.NewQueryClient(clientCtx)); err != nil {
		panic(err)
	}
}

// RegisterInterfaces registers a module's interface types and their concrete implementations as proto.Message.
func (AppModule) RegisterInterfaces(registrar codectypes.InterfaceRegistry) {
	types.RegisterInterfaces(registrar)
}

// RegisterServices registers the module's Msg and Query services.
func (am AppModule) RegisterServices(registrar grpc.ServiceRegistrar) error {
	types.RegisterMsgServer(registrar, keeper.NewMsgServerImpl(am.keeper))
	types.RegisterQueryServer(registrar, keeper.NewQueryServerImpl(am.keeper))
	return nil
}

// DefaultGenesis returns a default GenesisState for the module, marshalled to json.RawMessage.
func (am AppModule) DefaultGenesis(codec.JSONCodec) json.RawMessage {
	return am.cdc.MustMarshalJSON(types.DefaultGenesis())
}

// ValidateGenesis used to validate the GenesisState, given in its json.RawMessage form.
func (am AppModule) ValidateGenesis(_ codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var gs types.GenesisState
	if err := am.cdc.UnmarshalJSON(bz, &gs); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	return gs.Validate()
}

// InitGenesis performs the module's genesis initialization.
func (am AppModule) InitGenesis(ctx sdk.Context, _ codec.JSONCodec, bz json.RawMessage) {
	var gs types.GenesisState
	if err := am.cdc.UnmarshalJSON(bz, &gs); err != nil {
		panic(fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err))
	}
	if err := am.keeper.InitGenesis(ctx, gs); err != nil {
		panic(fmt.Errorf("failed to initialize %s genesis state: %w", types.ModuleName, err))
	}
}

// ExportGenesis returns the module's exported genesis state as raw JSON bytes.
func (am AppModule) ExportGenesis(ctx sdk.Context, _ codec.JSONCodec) json.RawMessage {
	gs, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to export %s genesis state: %w", types.ModuleName, err))
	}
	bz, err := am.cdc.MarshalJSON(gs)
	if err != nil {
		panic(fmt.Errorf("failed to marshal %s genesis state: %w", types.ModuleName, err))
	}
	return bz
}

// ConsensusVersion is a sequence number for state-breaking change of the module.
func (AppModule) ConsensusVersion() uint64 { return 1 }

// EndBlock recomputes the caps on an epoch boundary, fills the queue, and
// asserts V-10. The logic lives in Keeper.EndBlock so it is unit-tested there.
func (am AppModule) EndBlock(ctx context.Context) error {
	return am.keeper.EndBlock(ctx)
}

// PreBlock opens the BeginBlock window in which the staking backstop sleeps
// (S7 spec §6.4). BeginBlock, ordered last, closes it.
func (am AppModule) PreBlock(ctx context.Context) (appmodule.ResponsePreBlock, error) {
	return &sdk.ResponsePreBlock{}, am.keeper.OpenBeginBlockWindow(ctx)
}

// BeginBlock closes the window PreBlock opened.
func (am AppModule) BeginBlock(ctx context.Context) error {
	return am.keeper.CloseBeginBlockWindow(ctx)
}
