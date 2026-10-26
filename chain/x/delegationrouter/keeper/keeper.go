package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
)

// Keeper holds the router's parameters. It owns no other state: the route is
// computed from x/stakeengine, x/staking, x/participation and x/validatorregistry.
type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	authority    []byte

	Schema collections.Schema
	Params collections.Item[types.Params]

	stakeEngine         types.StakeEngineKeeper
	stakingKeeper       types.StakingKeeper
	participationKeeper types.ParticipationKeeper
	registryKeeper      types.ValidatorregistryKeeper
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,
	stakeEngine types.StakeEngineKeeper,
	stakingKeeper types.StakingKeeper,
	participationKeeper types.ParticipationKeeper,
	registryKeeper types.ValidatorregistryKeeper,
) Keeper {
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid authority address %s: %s", authority, err))
	}
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		storeService:        storeService,
		cdc:                 cdc,
		addressCodec:        addressCodec,
		authority:           authority,
		stakeEngine:         stakeEngine,
		stakingKeeper:       stakingKeeper,
		participationKeeper: participationKeeper,
		registryKeeper:      registryKeeper,
		Params:              collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// GetAuthority returns the module's authority.
func (k Keeper) GetAuthority() []byte { return k.authority }
