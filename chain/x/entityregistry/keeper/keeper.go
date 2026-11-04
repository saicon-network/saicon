package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

// Keeper holds the entity registry state: attested entities, the validators
// that belong to them, frozen seats and the validators the activation gate held.
type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	authority    []byte

	Schema collections.Schema
	// Params holds the attestor list and the per-entity active-seat limit.
	Params collections.Item[types.Params]
	// Entities maps an entity id to its attestation record.
	Entities collections.Map[string, types.Entity]
	// Members maps a validator operator address to its entity membership.
	Members collections.Map[[]byte, types.Member]
	// MembersByEntity is the reverse index: (entity id, operator address).
	MembersByEntity collections.KeySet[collections.Pair[string, []byte]]
	// Frozen holds the seats of active validators removed from an entity.
	Frozen collections.Map[[]byte, types.FrozenSeat]
	// Pending holds bonded validators whose activation the gate held.
	Pending collections.KeySet[[]byte]
	// VrfKeyOffers maps a validator operator address to the VRF key its operator
	// last offered.
	VrfKeyOffers collections.Map[[]byte, types.VrfKeyOffer]
	// VrfKeyOwners maps a VRF public key (32 bytes) to the one validator that
	// first offered it. Entries are never pruned.
	VrfKeyOwners collections.Map[[]byte, []byte]
	// Departures maps a validator operator address to the last entity it left
	// (spec §6.2); a later departure replaces the earlier one.
	Departures collections.Map[[]byte, types.Departure]

	registryKeeper types.ValidatorregistryKeeper
	stakingKeeper  types.StakingKeeper
}

// NewKeeper builds the keeper. It panics on an invalid authority address, as
// every module's NewKeeper does.
func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,
	registryKeeper types.ValidatorregistryKeeper,
	stakingKeeper types.StakingKeeper,
) Keeper {
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid authority address %s: %s", authority, err))
	}
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		storeService:   storeService,
		cdc:            cdc,
		addressCodec:   addressCodec,
		authority:      authority,
		registryKeeper: registryKeeper,
		stakingKeeper:  stakingKeeper,
		Params:         collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Entities: collections.NewMap(sb, types.EntitiesKey, "entities",
			collections.StringKey, codec.CollValue[types.Entity](cdc)),
		Members: collections.NewMap(sb, types.MembersKey, "members",
			collections.BytesKey, codec.CollValue[types.Member](cdc)),
		MembersByEntity: collections.NewKeySet(sb, types.MembersByEntityKey, "members_by_entity",
			collections.PairKeyCodec(collections.StringKey, collections.BytesKey)),
		Frozen: collections.NewMap(sb, types.FrozenKey, "frozen",
			collections.BytesKey, codec.CollValue[types.FrozenSeat](cdc)),
		Pending: collections.NewKeySet(sb, types.PendingKey, "pending", collections.BytesKey),
		VrfKeyOffers: collections.NewMap(sb, types.VrfKeyOffersKey, "vrf_key_offers",
			collections.BytesKey, codec.CollValue[types.VrfKeyOffer](cdc)),
		VrfKeyOwners: collections.NewMap(sb, types.VrfKeyOwnersKey, "vrf_key_owners",
			collections.BytesKey, collections.BytesValue),
		Departures: collections.NewMap(sb, types.DeparturesKey, "departures",
			collections.BytesKey, codec.CollValue[types.Departure](cdc)),
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
