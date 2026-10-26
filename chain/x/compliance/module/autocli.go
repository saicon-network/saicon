package compliance

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
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
					RpcMethod:      "Compliance",
					Use:            "compliance [validator-address]",
					Short:          "Query a validator's compliance state (C flag, last event, reason)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "validator_address"}},
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
					RpcMethod: "SubmitEvidence",
					Use:       "submit-evidence [validator-address] [event] [reason]",
					Short:     "Flag a validator as non-compliant (authority-gated; via gov)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "validator_address"},
						{ProtoField: "event"},
						{ProtoField: "reason"},
					},
				},
				{
					RpcMethod: "ClearFlag",
					Use:       "clear-flag [validator-address] [reason]",
					Short:     "Restore a validator to compliant (authority-gated; via gov)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "validator_address"},
						{ProtoField: "reason"},
					},
				},
				{
					RpcMethod: "Unjail",
					Use:       "unjail [validator-address] [reason]",
					Short:     "Governed unjail of a JAILED validator (cooldown + tombstone gated; via gov)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "validator_address"},
						{ProtoField: "reason"},
					},
				},
			},
		},
	}
}
