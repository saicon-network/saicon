package participation

import (
	"context"
	"encoding/json"
	"fmt"

	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/comet"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"google.golang.org/grpc"

	"github.com/saicon-network/saicon/chain/x/participation/keeper"
	"github.com/saicon-network/saicon/chain/x/participation/types"
)

var (
	_ module.AppModuleBasic = (*AppModule)(nil)
	_ module.AppModule      = (*AppModule)(nil) //nolint:staticcheck // the legacy interface is what module.Manager still takes in SDK v0.53
	_ module.HasGenesis     = (*AppModule)(nil)

	_ appmodule.AppModule       = (*AppModule)(nil)
	_ appmodule.HasBeginBlocker = (*AppModule)(nil)
	_ appmodule.HasEndBlocker   = (*AppModule)(nil)
)

// AppModule implements the AppModule interface that defines the inter-dependent methods that modules need to implement
type AppModule struct {
	cdc        codec.Codec
	keeper     keeper.Keeper
	authKeeper types.AuthKeeper
	bankKeeper types.BankKeeper
}

func NewAppModule(
	cdc codec.Codec,
	keeper keeper.Keeper,
	authKeeper types.AuthKeeper,
	bankKeeper types.BankKeeper,
) AppModule {
	return AppModule{
		cdc:        cdc,
		keeper:     keeper,
		authKeeper: authKeeper,
		bankKeeper: bankKeeper,
	}
}

// IsAppModule implements the appmodule.AppModule interface.
func (AppModule) IsAppModule() {}

// Name returns the name of the module as a string.
func (AppModule) Name() string {
	return types.ModuleName
}

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

// RegisterServices registers a gRPC query service to respond to the module-specific gRPC queries
func (am AppModule) RegisterServices(registrar grpc.ServiceRegistrar) error {
	types.RegisterMsgServer(registrar, keeper.NewMsgServerImpl(am.keeper))
	types.RegisterQueryServer(registrar, keeper.NewQueryServerImpl(am.keeper))

	return nil
}

// DefaultGenesis returns a default GenesisState for the module, marshalled to json.RawMessage.
// The default GenesisState need to be defined by the module developer and is primarily used for testing.
func (am AppModule) DefaultGenesis(codec.JSONCodec) json.RawMessage {
	return am.cdc.MustMarshalJSON(types.DefaultGenesis())
}

// ValidateGenesis used to validate the GenesisState, given in its json.RawMessage form.
func (am AppModule) ValidateGenesis(_ codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var genState types.GenesisState
	if err := am.cdc.UnmarshalJSON(bz, &genState); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}

	return genState.Validate()
}

// InitGenesis performs the module's genesis initialization. It returns no validator updates.
func (am AppModule) InitGenesis(ctx sdk.Context, _ codec.JSONCodec, gs json.RawMessage) {
	var genState types.GenesisState
	// Initialize global index to index in genesis state
	if err := am.cdc.UnmarshalJSON(gs, &genState); err != nil {
		panic(fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err))
	}

	if err := am.keeper.InitGenesis(ctx, genState); err != nil {
		panic(fmt.Errorf("failed to initialize %s genesis state: %w", types.ModuleName, err))
	}
}

// ExportGenesis returns the module's exported genesis state as raw JSON bytes.
func (am AppModule) ExportGenesis(ctx sdk.Context, _ codec.JSONCodec) json.RawMessage {
	genState, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to export %s genesis state: %w", types.ModuleName, err))
	}

	bz, err := am.cdc.MarshalJSON(genState)
	if err != nil {
		panic(fmt.Errorf("failed to marshal %s genesis state: %w", types.ModuleName, err))
	}

	return bz
}

// ConsensusVersion is a sequence number for state-breaking changes. It stays at
// 1 through v0.1 development: the chain is pre-launch (genesis is reset on every
// iteration, no live state to migrate), so store additions (e.g. M1-B epoch
// counters) do not yet require a migration. ConsensusVersion bumps + migration
// handlers are introduced with upgrade readiness (roadmap M3-D).
func (AppModule) ConsensusVersion() uint64 { return 1 }

// BeginBlock tallies the previous block's precommit votes into per-validator
// epoch counters by forwarding them to keeper.RecordEpochVotes. It reads the
// last-commit info from CometInfo (injected by the runtime). Errors from
// RecordEpochVotes are logged and swallowed so that an accounting glitch can
// never halt block production.
func (am AppModule) BeginBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	ci := sdkCtx.CometInfo()
	if ci == nil {
		return nil
	}
	votes := ci.GetLastCommit().Votes()
	n := votes.Len()
	if n == 0 {
		// genesis / first block: no previous commit to tally.
		return nil
	}
	vs := make([]comet.VoteInfo, 0, n)
	for i := 0; i < n; i++ {
		vs = append(vs, votes.Get(i))
	}
	if err := am.keeper.RecordEpochVotes(ctx, vs); err != nil {
		// Never halt the chain on accounting; log and continue.
		sdkCtx.Logger().Error("participation: RecordEpochVotes failed (swallowed)",
			"height", sdkCtx.BlockHeight(), "err", err)
	}
	return nil
}

// EndBlock contains the logic that is automatically triggered at the end of each block.
//
// It implements the block-based epoch tick (Decision D1 — no time-based
// x/epochs): when the current height is an exact multiple of
// Params.EpochLengthBlocks, it computes the per-validator Participation Score
// snapshot and accumulates Empirical Points for the just-closed epoch.
//
// Error handling (v0.1): a compute error is LOGGED and SWALLOWED (return nil)
// rather than returned, because returning an error from EndBlock halts the
// chain. The hard fail-loud invariant (V-2) is not wired yet, so a transient
// participation glitch must not be able to stop block production. This will be
// revisited when V-2 lands.
func (am AppModule) EndBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	h := sdkCtx.BlockHeight()

	p, err := am.keeper.Params.Get(ctx)
	if err != nil {
		sdkCtx.Logger().Error("participation: failed to read params in EndBlock", "height", h, "err", err)
		return nil
	}

	if p.EpochLengthBlocks == 0 || h <= 1 || h%int64(p.EpochLengthBlocks) != 0 {
		return nil
	}

	epochID := uint64(h) / p.EpochLengthBlocks
	if err := am.keeper.ComputeEpoch(ctx, epochID); err != nil {
		sdkCtx.Logger().Error("participation: ComputeEpoch failed (swallowed to avoid halt)",
			"height", h, "epoch_id", epochID, "err", err)
		return nil
	}

	// V-7 fail-loud backstop: no PS snapshot may exist for a non-active validator.
	// A breach is a correctness violation (not a transient error) and halts.
	if msg, broken := am.keeper.PSActiveOnlyInvariant(ctx, epochID); broken {
		panic(msg)
	}

	sdkCtx.Logger().Info("participation: epoch computed", "height", h, "epoch_id", epochID)
	return nil
}
