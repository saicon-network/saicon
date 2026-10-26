package validatorregistry

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
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
					RpcMethod:      "Validator",
					Use:            "validator [validator-address]",
					Short:          "Query SAiCoN metadata for a single validator",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "validator_address"}},
				},
				{
					RpcMethod: "Validators",
					Use:       "list-validators",
					Short:     "List validators, optionally filtered by --status-filter (e.g. active)",
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
				{
					RpcMethod: "RegisterValidator",
					Use:       "register-validator",
					Short:     "Register the signer as an SAiCoN validator (enters bonding)",
				},
			},
		},
	}
}
