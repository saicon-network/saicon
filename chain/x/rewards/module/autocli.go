package rewards

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
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
					Short:     "Shows the parameters of the module",
				},
				{
					RpcMethod: "RewardSnapshot",
					Use:       "snapshot [validator-address] [epoch-id]",
					Short:     "Query the reward decision (minted amount or denial reason) for a validator at an epoch",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "validator_address"},
						{ProtoField: "epoch_id"},
					},
				},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service:              types.Msg_serviceDesc.ServiceName,
			EnhanceCustomCommand: true, // only required if you want to use the custom command
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateParams",
					Skip:      true, // skipped because authority gated
				},
			},
		},
	}
}
