package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	// Address capable of executing a MsgUpdateParams message.
	// Typically, this should be the x/gov module account.
	authority []byte

	Schema collections.Schema
	Params collections.Item[types.Params]

	// ComplianceState stores the source-of-truth compliance record per validator,
	// keyed by operator address bytes. See chain/docs/validators/README.md §4.5.
	ComplianceState collections.Map[[]byte, types.ComplianceState]

	validatorregistryKeeper types.ValidatorregistryKeeper
	stakingKeeper           types.StakingKeeper
	slashingKeeper          types.SlashingKeeper
	participationKeeper     types.ParticipationKeeper
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,

	validatorregistryKeeper types.ValidatorregistryKeeper,
	stakingKeeper types.StakingKeeper,
	slashingKeeper types.SlashingKeeper,
	participationKeeper types.ParticipationKeeper,
) Keeper {
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid authority address %s: %s", authority, err))
	}

	sb := collections.NewSchemaBuilder(storeService)

	k := Keeper{
		storeService: storeService,
		cdc:          cdc,
		addressCodec: addressCodec,
		authority:    authority,

		validatorregistryKeeper: validatorregistryKeeper,
		stakingKeeper:           stakingKeeper,
		slashingKeeper:          slashingKeeper,
		participationKeeper:     participationKeeper,
		Params:                  collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		ComplianceState: collections.NewMap(
			sb,
			types.ComplianceStateKey,
			"compliance_state",
			collections.BytesKey,
			codec.CollValue[types.ComplianceState](cdc),
		),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	return k
}

// GetAuthority returns the module's authority.
func (k Keeper) GetAuthority() []byte {
	return k.authority
}
