package entityregistry

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
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
					Short:     "Shows the entity registry parameters",
				},
				{
					RpcMethod: "Entities",
					Use:       "entities",
					Short:     "Lists the attested and revoked entities",
				},
				{
					RpcMethod:      "Entity",
					Use:            "entity [entity-id]",
					Short:          "Shows an entity with its validators and active seats",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "entity_id"}},
				},
				{
					RpcMethod:      "ValidatorEntity",
					Use:            "validator-entity [validator-address]",
					Short:          "Shows a validator's entity, frozen seat and held-activation state",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "validator"}},
				},
				{
					RpcMethod: "Tickets",
					Use:       "tickets",
					Short:     "Lists one lottery ticket per attested entity",
				},
				{
					RpcMethod:      "VrfKey",
					Use:            "vrf-key [validator-address]",
					Short:          "Shows a validator's offered VRF key and the key bound to its entity membership",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "validator"}},
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
					RpcMethod: "OfferVrfKey",
					Skip:      true, // hand-written: the proof of possession is made locally
				},
				{
					RpcMethod: "AttestEntity",
					Use:       "attest [entity-id] [display-name] [attestation-hash] [validator-address...]",
					Short:     "Attest a beneficial-owner entity and its validators (the hash is the hex SHA-256 of the off-chain attestation document); bind offered VRF keys with --vrf-keys '{\"validator\":\"saiconvaloper1…\",\"vrf_public_key\":\"…\"}'",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "entity_id"},
						{ProtoField: "display_name"},
						{ProtoField: "attestation_hash"},
						{ProtoField: "validators", Varargs: true},
					},
				},
				{
					RpcMethod: "RevokeEntity",
					Use:       "revoke [entity-id] [reason]",
					Short:     "Revoke an entity's attestation; its active validators keep a frozen seat",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "entity_id"},
						{ProtoField: "reason"},
					},
				},
			},
		},
	}
}
