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

	// Offences holds every equivocation x/evidence accepted, keyed by the 32
	// evidence hash bytes (spec §10).
	Offences collections.Map[[]byte, types.Offence]
	// OffencesByEntity indexes the duplicate-vote offences linked to an entity
	// by (entity_id, hash).
	OffencesByEntity collections.KeySet[collections.Pair[string, []byte]]
	// Sanctions holds every executed coordinated-attack sanction by id.
	Sanctions collections.Map[uint64, types.Sanction]
	// SanctionSeq is the sanction id sequence; Next returns the next id, the
	// first being 1.
	SanctionSeq collections.Sequence
	// RemovedConsensus holds every tombstoned consensus address whose validator
	// x/staking removed (the AfterValidatorRemoved hook). A tombstoned key never
	// gets a new offence, so each of its offences predates the removal, and any
	// validator x/staking holds on that key or operator now is another incarnation.
	RemovedConsensus collections.KeySet[[]byte]

	validatorregistryKeeper types.ValidatorregistryKeeper
	stakingKeeper           types.StakingKeeper
	slashingKeeper          types.SlashingKeeper
	participationKeeper     types.ParticipationKeeper
	evidence                types.EvidenceReader
	entityKeeper            types.EntityregistryKeeper
	window                  types.SlashWindowKeeper
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
	evidence types.EvidenceReader,
	entities types.EntityregistryKeeper,
	window types.SlashWindowKeeper,
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
		evidence:                evidence,
		entityKeeper:            entities,
		window:                  window,
		Params:                  collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		ComplianceState: collections.NewMap(
			sb,
			types.ComplianceStateKey,
			"compliance_state",
			collections.BytesKey,
			codec.CollValue[types.ComplianceState](cdc),
		),
		Offences: collections.NewMap(sb, types.OffencesKey, "offences",
			collections.BytesKey, codec.CollValue[types.Offence](cdc)),
		OffencesByEntity: collections.NewKeySet(sb, types.OffencesByEntityKey, "offences_by_entity",
			collections.PairKeyCodec(collections.StringKey, collections.BytesKey)),
		Sanctions: collections.NewMap(sb, types.SanctionsKey, "sanctions",
			collections.Uint64Key, codec.CollValue[types.Sanction](cdc)),
		SanctionSeq: collections.NewSequence(sb, types.SanctionSeqKey, "sanction_seq"),
		RemovedConsensus: collections.NewKeySet(sb, types.RemovedConsensusKey, "removed_consensus",
			collections.BytesKey),
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
