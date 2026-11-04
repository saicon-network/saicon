// Package keeper holds the x/randomness state machine: rounds on the
// participation epoch grid, the pool snapshot, the chained seed and the shadow
// draw. Nothing here writes outside the module, and no EndBlock path returns an
// error (an EndBlock error would halt the chain).
package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

// Keeper holds the current round, its pool snapshot and submissions, and the
// bounded history of closed rounds.
type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	authority    []byte

	Schema collections.Schema
	// Params holds the cadence: rotation_epochs and window_blocks.
	Params collections.Item[types.Params]
	// CurrentRound is the one live round; always present after InitGenesis.
	CurrentRound collections.Item[types.Round]
	// Pool is the snapshot taken when the window opens, by entity id. It is
	// empty unless the round is OPEN.
	Pool collections.Map[string, types.PoolEntry]
	// Designated indexes the keyed pool entries: validator operator bytes →
	// entity id.
	Designated collections.Map[[]byte, string]
	// Submissions holds this round's verified submissions, by entity id.
	Submissions collections.Map[string, types.Submission]
	// draws is the history of closed rounds. It is unexported on purpose: the
	// draw is shadow-only, and no keeper method returns a draw or a winner
	// (spec §10.3). The gRPC query service, events and genesis export read it
	// from inside this package.
	draws collections.Map[uint64, types.Draw]

	er types.EntityregistryKeeper
	vr types.ValidatorregistryKeeper
	se types.StakeengineKeeper
	pk types.ParticipationKeeper
}

// NewKeeper builds the keeper. It panics on an invalid authority address, as
// every module's NewKeeper does.
func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,
	er types.EntityregistryKeeper,
	vr types.ValidatorregistryKeeper,
	se types.StakeengineKeeper,
	pk types.ParticipationKeeper,
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
		er:           er,
		vr:           vr,
		se:           se,
		pk:           pk,
		Params:       collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		CurrentRound: collections.NewItem(sb, types.CurrentRoundKey, "current_round", codec.CollValue[types.Round](cdc)),
		Pool: collections.NewMap(sb, types.PoolKey, "pool",
			collections.StringKey, codec.CollValue[types.PoolEntry](cdc)),
		Designated: collections.NewMap(sb, types.DesignatedKey, "designated",
			collections.BytesKey, collections.StringValue),
		Submissions: collections.NewMap(sb, types.SubmissionsKey, "submissions",
			collections.StringKey, codec.CollValue[types.Submission](cdc)),
		draws: collections.NewMap(sb, types.DrawsKey, "draws",
			collections.Uint64Key, codec.CollValue[types.Draw](cdc)),
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
