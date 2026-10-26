package delegationrouter

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
)

// AutoCLIOptions implements the autocli.HasAutoCLIConfig interface.
func (am AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "Params",
					Use:       "params",
					Short:     "Shows the router parameters",
				},
				{
					RpcMethod: "Route",
					Use:       "route [delegator-address] [validator-address] [amount]",
					Short:     "Previews how a routed delegation would be spread, without sending it (amount in nscn)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "delegator"},
						{ProtoField: "validator"},
						{ProtoField: "amount"},
					},
				},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service:              types.Msg_serviceDesc.ServiceName,
			EnhanceCustomCommand: true,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateParams",
					Skip:      true, // skipped because authority gated
				},
				{
					RpcMethod: "DelegateRouted",
					Use:       "delegate [validator-address] [amount]",
					Short:     "Delegate to a validator; what exceeds its cap is spread over other active validators or queued. Amount is a coin, e.g. 250000000000000nscn",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "validator"},
						{ProtoField: "amount"},
					},
				},
			},
		},
	}
}
