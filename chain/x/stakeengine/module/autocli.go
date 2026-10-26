package stakeengine

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
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
					Short:     "Shows the parameters and the 2,000 SCN entry floor",
				},
				{
					RpcMethod: "Caps",
					Use:       "caps",
					Short:     "Shows this epoch's global cap, validator cap, public circulating supply and the headroom left",
				},
				{
					RpcMethod:      "ValidatorCap",
					Use:            "validator-cap [validator-address]",
					Short:          "Shows a validator's tokens, the per-validator cap and its room under it",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "validator_address"}},
				},
				{
					RpcMethod: "Queue",
					Use:       "queue",
					Short:     "Lists the stake queue in the order it is filled",
				},
				{
					RpcMethod:      "QueueEntry",
					Use:            "queue-entry [id]",
					Short:          "Shows one queue entry",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{
					RpcMethod:      "DelegatorQueue",
					Use:            "delegator-queue [delegator-address]",
					Short:          "Lists one delegator's queue entries",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "delegator_address"}},
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
					RpcMethod: "EnqueueDelegation",
					Use:       "enqueue [validator-address] [amount]",
					Short:     "Queue stake for a validator while the global cap is full; it is delegated when room opens",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "validator"},
						{ProtoField: "amount"},
					},
				},
				{
					RpcMethod:      "CancelQueuedDelegation",
					Use:            "cancel [id]",
					Short:          "Leave the queue; what has not been delegated yet is returned",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
			},
		},
	}
}
