package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/saicon-network/saicon/chain/x/participation/types"
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

	// ParticipationScore stores the per-epoch participation snapshot per
	// validator, keyed by (epoch_id, operator address bytes).
	// See chain/docs/validators/README.md §5.
	ParticipationScore collections.Map[collections.Pair[uint64, []byte], types.ParticipationScoreSnapshot]
	// EmpiricalPoints stores accumulated EP per validator, keyed by operator
	// address bytes. EP gates rewards (invariant V-2).
	EmpiricalPoints collections.Map[[]byte, types.EmpiricalPointsState]

	// EpochBlocksSigned / EpochBlocksMissed are running per-validator precommit
	// tallies for the current epoch (reset each epoch boundary). M1-B.
	EpochBlocksSigned collections.Map[[]byte, uint64]
	EpochBlocksMissed collections.Map[[]byte, uint64]

	validatorregistryKeeper types.ValidatorregistryKeeper
	stakingKeeper           types.StakingKeeper
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,

	validatorregistryKeeper types.ValidatorregistryKeeper,
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

		validatorregistryKeeper: validatorregistryKeeper,
		stakingKeeper:           stakingKeeper,
		Params:                  collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		ParticipationScore: collections.NewMap(
			sb,
			types.ParticipationScoreKey,
			"participation_score",
			collections.PairKeyCodec(collections.Uint64Key, collections.BytesKey),
			codec.CollValue[types.ParticipationScoreSnapshot](cdc),
		),
		EmpiricalPoints: collections.NewMap(
			sb,
			types.EmpiricalPointsKey,
			"empirical_points",
			collections.BytesKey,
			codec.CollValue[types.EmpiricalPointsState](cdc),
		),
		EpochBlocksSigned: collections.NewMap(sb, types.EpochBlocksSignedKey, "epoch_blocks_signed", collections.BytesKey, collections.Uint64Value),
		EpochBlocksMissed: collections.NewMap(sb, types.EpochBlocksMissedKey, "epoch_blocks_missed", collections.BytesKey, collections.Uint64Value),
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
