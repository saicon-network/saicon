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
				{
					RpcMethod:      "Offence",
					Use:            "offence [hash]",
					Short:          "Query one recorded equivocation by its evidence hash (64 uppercase hex)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "evidence_hash"}},
				},
				{
					RpcMethod: "Offences",
					Use:       "offences",
					Short:     "List every recorded equivocation in evidence-hash order",
				},
				{
					RpcMethod:      "Case",
					Use:            "case [entity-id]",
					Short:          "Query an entity's case: its linked offences, whether it is armed, and which are unfired",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "entity_id"}},
				},
				{
					RpcMethod: "ArmedCases",
					Use:       "armed",
					Short:     "List every armed entity with at least one unfired offence",
				},
				{
					RpcMethod:      "Sanction",
					Use:            "sanction [id]",
					Short:          "Query one executed coordinated-attack sanction",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{
					RpcMethod: "Sanctions",
					Use:       "sanctions",
					Short:     "List every executed coordinated-attack sanction",
				},
				{
					RpcMethod: "SanctionPreview",
					Use:       "sanction-preview [entity-id] [fraction] [hash]...",
					Short:     "Preview what a coordinated-attack sanction would do right now (writes nothing)",
					Long: "Runs the same planning function as the firing on the current state and reports whether the " +
						"firing would pass, with the targets, the extra fraction per target, the earliest execution " +
						"time and the unbonding deadline. It does not fire anything. A sanction is fired only by a " +
						"governance proposal: submit one with `saicond tx gov submit-proposal` carrying a " +
						"MsgFireCoordinatedSanction that names these hashes.",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "entity_id"},
						{ProtoField: "fraction"},
						{ProtoField: "evidence_hashes", Varargs: true},
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
				{
					RpcMethod: "FireCoordinatedSanction",
					Skip:      true, // gov proposals only (spec §11)
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
