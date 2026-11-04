package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// activationGate lives behind a pointer so every copy of the Keeper value —
// x/compliance, x/stakeengine and the app each hold one — sees the gate that
// x/entityregistry installs after construction.
type activationGate struct{ g types.ActivationGate }

type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	// Address capable of executing a MsgUpdateParams message.
	// Typically, this should be the x/gov module account.
	authority []byte

	Schema collections.Schema
	Params collections.Item[types.Params]

	// ValidatorMetadata stores SAiCoN-specific validator state keyed by
	// validator operator address (sdk.ValAddress bytes).
	// See ADR-0003 and chain/docs/validators/README.md §2.
	ValidatorMetadata collections.Map[[]byte, types.ValidatorMetadata]

	bankKeeper    types.BankKeeper
	stakingKeeper types.StakingKeeper

	// gate is consulted before every transition to STATUS_ACTIVE; see
	// SetActivationGate.
	gate *activationGate
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,

	bankKeeper types.BankKeeper,
	stakingKeeper types.StakingKeeper,
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

		bankKeeper:    bankKeeper,
		stakingKeeper: stakingKeeper,
		gate:          &activationGate{},
		Params:        collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		ValidatorMetadata: collections.NewMap(
			sb,
			types.ValidatorMetadataKey,
			"validator_metadata",
			collections.BytesKey,
			codec.CollValue[types.ValidatorMetadata](cdc),
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

// SetActivationGate installs the gate every transition to STATUS_ACTIVE must
// pass. x/entityregistry calls it once, after both keepers are constructed; a
// second call panics. The gate is shared by every copy of the Keeper value.
func (k Keeper) SetActivationGate(g types.ActivationGate) {
	if k.gate.g != nil {
		panic("validatorregistry: activation gate already set")
	}
	k.gate.g = g
}

// HasActivationGate reports whether an activation gate is installed.
func (k Keeper) HasActivationGate() bool {
	return k.gate.g != nil
}
