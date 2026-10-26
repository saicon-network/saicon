package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
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

	// RewardSnapshot stores the per-epoch emission record per validator, keyed
	// by (epoch_id, operator address bytes). See keeper/emission.go.
	RewardSnapshot collections.Map[collections.Pair[uint64, []byte], types.RewardSnapshot]

	// Remainder holds base units left over when an epoch pool did not divide
	// evenly among its recipients. It is carried into the NEXT epoch's pool and
	// never paid to anyone directly — the Register's rule, so rounding dust
	// cannot accrue to a validator owner (ADR-0011 D5).
	Remainder collections.Item[math.Int]

	// ObservedGasFees is the gas revenue seen so far in the current epoch. It
	// reduces how much must be minted (ADR-0011 D2) and is cleared at each epoch
	// boundary. Not a balance — x/distribution holds the actual coins.
	ObservedGasFees collections.Item[math.Int]

	authKeeper              types.AuthKeeper
	bankKeeper              types.BankKeeper
	stakingKeeper           types.StakingKeeper
	participationKeeper     types.ParticipationKeeper
	validatorregistryKeeper types.ValidatorregistryKeeper
	complianceKeeper        types.ComplianceKeeper
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,

	authKeeper types.AuthKeeper,
	bankKeeper types.BankKeeper,
	stakingKeeper types.StakingKeeper,
	participationKeeper types.ParticipationKeeper,
	validatorregistryKeeper types.ValidatorregistryKeeper,
	complianceKeeper types.ComplianceKeeper,
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

		authKeeper:              authKeeper,
		bankKeeper:              bankKeeper,
		stakingKeeper:           stakingKeeper,
		participationKeeper:     participationKeeper,
		validatorregistryKeeper: validatorregistryKeeper,
		complianceKeeper:        complianceKeeper,
		Params:                  collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		RewardSnapshot: collections.NewMap(
			sb,
			types.RewardSnapshotKey,
			"reward_snapshot",
			collections.PairKeyCodec(collections.Uint64Key, collections.BytesKey),
			codec.CollValue[types.RewardSnapshot](cdc),
		),
		Remainder: collections.NewItem(
			sb,
			types.RemainderKey,
			"remainder",
			sdk.IntValue,
		),
		ObservedGasFees: collections.NewItem(
			sb,
			types.ObservedGasFeesKey,
			"observed_gas_fees",
			sdk.IntValue,
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

// EpochLength delegates to x/participation's EpochLengthBlocks so the rewards
// EndBlock (WI-016) fires on the same epoch boundary participation uses. Keeping
// the participation keeper unexported and exposing this thin accessor keeps the
// module layer from reaching into keeper internals.
func (k Keeper) EpochLength(ctx context.Context) (uint64, error) {
	return k.participationKeeper.EpochLengthBlocks(ctx)
}
