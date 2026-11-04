package randomness

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/saicon-network/saicon/chain/x/randomness/types"
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
					Short:     "Shows the randomness parameters",
				},
				{
					RpcMethod: "Round",
					Use:       "round",
					Short:     "Shows the current round with its pool, submissions and required quorum",
				},
				{
					RpcMethod:      "Draw",
					Use:            "draw [round]",
					Short:          "Shows the record of one closed round",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "round"}},
				},
				{
					RpcMethod: "Draws",
					Use:       "draws",
					Short:     "Lists the stored round records; --reverse gives newest first",
				},
				{
					RpcMethod: "LatestDraw",
					Use:       "latest-draw",
					Short:     "Shows the newest round record and the newest drawn one",
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
					RpcMethod: "SubmitVrf",
					Skip:      true, // hand-written: the proof is made locally from the key file
				},
			},
		},
	}
}
