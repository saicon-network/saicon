package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

type Keeper struct {
	storeService corestore.KVStoreService
	// transientService backs the staking backstop's per-block records and its
	// BeginBlock window (S7 spec §6).
	transientService corestore.TransientStoreService
	cdc              codec.Codec
	addressCodec     address.Codec
	authority        []byte

	Schema collections.Schema
	Params collections.Item[types.Params]
	// CapState is the snapshot every check reads during an epoch.
	CapState collections.Item[types.CapState]
	// Queue holds the entries by id; fill order is computed, not stored.
	Queue    collections.Map[uint64, types.QueueEntry]
	QueueSeq collections.Sequence
	// QueueLen counts the entries, so max_queue_entries is checked without a walk.
	QueueLen collections.Item[uint64]
	// QueueByDelegator counts queue entries per (delegator, validator).
	QueueByDelegator collections.Map[collections.Pair[string, string], uint64]

	authKeeper          types.AuthKeeper
	bankKeeper          types.BankKeeper
	stakingKeeper       types.StakingKeeper
	participationKeeper types.ParticipationKeeper
	registryKeeper      types.ValidatorregistryKeeper
}

func NewKeeper(
	storeService corestore.KVStoreService,
	transientService corestore.TransientStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,
	authKeeper types.AuthKeeper,
	bankKeeper types.BankKeeper,
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
		transientService:    transientService,
		cdc:                 cdc,
		addressCodec:        addressCodec,
		authority:           authority,
		authKeeper:          authKeeper,
		bankKeeper:          bankKeeper,
		stakingKeeper:       stakingKeeper,
		participationKeeper: participationKeeper,
		registryKeeper:      registryKeeper,
		Params:              collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		CapState:            collections.NewItem(sb, types.CapStateKey, "cap_state", codec.CollValue[types.CapState](cdc)),
		Queue: collections.NewMap(sb, types.QueueKey, "queue",
			collections.Uint64Key, codec.CollValue[types.QueueEntry](cdc)),
		QueueSeq: collections.NewSequence(sb, types.QueueSeqKey, "queue_seq"),
		QueueLen: collections.NewItem(sb, types.QueueLenKey, "queue_len", collections.Uint64Value),
		QueueByDelegator: collections.NewMap(sb, types.QueueByDelegatorKey, "queue_by_delegator",
			collections.PairKeyCodec(collections.StringKey, collections.StringKey), collections.Uint64Value),
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

// EscrowAddress is the module account that holds queued stake.
func (k Keeper) EscrowAddress() sdk.AccAddress { return authtypes.NewModuleAddress(types.ModuleName) }
