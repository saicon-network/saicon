package keeper_test

import (
	"crypto/sha256"
	"sort"
	"testing"
	"time"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func TestGenesis(t *testing.T) {
	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
	}

	f := initFixture(t)
	err := f.keeper.InitGenesis(f.ctx, genesisState)
	require.NoError(t, err)
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.EqualExportedValues(t, genesisState.Params, got.Params)
}

// D2, the other half. x/validatorregistry's compliance_flag is only a MIRROR;
// ComplianceState here is the source of truth, and it was not in genesis either.
//
// Losing it across an export/import is worse than losing the mirror, because the
// reward gate reads a missing state as innocent
// (`compliant := !found || state.Compliant`): a validator sanctioned with C=0
// became reward-eligible again after a routine coordinated restart, and the whole
// evidence trail behind the sanction went with it.
func TestGenesis_ComplianceStatesSurviveTheRoundTrip(t *testing.T) {
	f := initFixture(t)

	flaggedAt := time.Date(2026, 6, 15, 8, 0, 0, 0, time.UTC)
	sanctioned := sdk.ValAddress([]byte("sanctioned-validator"))[:20]
	clean := sdk.ValAddress([]byte("clean-validator-addr"))[:20]

	require.NoError(t, f.keeper.ComplianceState.Set(f.ctx, sanctioned.Bytes(), types.ComplianceState{
		ValidatorAddress: sdk.AccAddress(sanctioned).String(),
		Compliant:        false,
		LastEvent:        types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION,
		LastEventTime:    flaggedAt,
		Reason:           "double-sign at height 4211",
	}))
	require.NoError(t, f.keeper.ComplianceState.Set(f.ctx, clean.Bytes(), types.ComplianceState{
		ValidatorAddress: sdk.AccAddress(clean).String(),
		Compliant:        true,
		LastEventTime:    flaggedAt,
	}))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, exported.ComplianceStates, 2)

	fresh := initFixture(t)
	require.NoError(t, fresh.keeper.InitGenesis(fresh.ctx, *exported))

	got, found, err := fresh.keeper.GetCompliance(fresh.ctx, sanctioned)
	require.NoError(t, err)
	require.True(t, found, "the sanction must survive the restart")
	require.False(t, got.Compliant, "a C=0 validator must not come back reward-eligible")
	require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION, got.LastEvent)
	require.Equal(t, "double-sign at height 4211", got.Reason, "the evidence trail must survive too")
	require.Equal(t, flaggedAt, got.LastEventTime.UTC())

	ok, found, err := fresh.keeper.GetCompliance(fresh.ctx, clean)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, ok.Compliant)
}

func evidenceHashOf(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return types.FormatEvidenceHash(sum[:])
}

// populatedGenesis: entity "acme" has two linked duplicate-vote offences, one
// light-client-attack offence has no entity, and sanction 1 fires the first.
func populatedGenesis() types.GenesisState {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	offence := func(seed string, kind types.MisbehaviorKind, entity string, infraction int64) types.Offence {
		return types.Offence{
			EvidenceHash:       evidenceHashOf(seed),
			Validator:          sdk.ValAddress(sha256.New().Sum([]byte(seed))[:20]).String(),
			ConsensusAddress:   "cons-" + seed,
			Kind:               kind,
			InfractionHeight:   infraction,
			InfractionTime:     at,
			Power:              100,
			DoubleSignFraction: math.LegacyNewDecWithPrec(10, 2),
			EntityId:           entity,
			RecordedHeight:     infraction + 2,
			RecordedTime:       at.Add(4 * time.Second),
		}
	}
	a := offence("v1", types.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE, "acme", 100)
	b := offence("v2", types.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE, "acme", 102)
	c := offence("v3", types.MisbehaviorKind_MISBEHAVIOR_KIND_LIGHT_CLIENT_ATTACK, "", 90)
	a.SanctionId = 1
	// The export is in hash-byte order, which is the order a valid genesis lists them in.
	offences := []types.Offence{a, b, c}
	sort.Slice(offences, func(i, j int) bool { return offences[i].EvidenceHash < offences[j].EvidenceHash })
	return types.GenesisState{
		Params:   types.DefaultParams(),
		Offences: offences,
		Sanctions: []types.Sanction{{
			Id:       1,
			EntityId: "acme",
			Fraction: math.LegacyNewDecWithPrec(30, 2),
			Targets: []types.SanctionTarget{{
				EvidenceHash:    a.EvidenceHash,
				Validator:       a.Validator,
				ExtraFraction:   math.LegacyNewDecWithPrec(20, 2),
				ValidatorBurned: math.NewInt(2000),
				Outcome:         types.TargetOutcome_TARGET_OUTCOME_SLASHED,
				BaseTokens:      math.NewInt(10000),
				Classification:  types.TargetClassification_TARGET_CLASSIFICATION_CLASSIFIED,
			}},
			Authority: "gov",
			Reason:    "coordinated double-sign",
			Height:    500,
			Time:      at.Add(10 * 24 * time.Hour),
		}},
		NextSanctionId: 2,
		// In address-byte order, as the export lists them.
		RemovedConsensusAddresses: []string{
			sdk.ConsAddress([]byte("removed-consensus-01")).String(),
			sdk.ConsAddress([]byte("removed-consensus-02")).String(),
		},
	}
}

func TestGenesis_OffencesAndSanctionsSurviveTheRoundTrip(t *testing.T) {
	f := initFixture(t)
	gs := populatedGenesis()
	require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))

	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	want, err := gs.Marshal()
	require.NoError(t, err)
	have, err := got.Marshal()
	require.NoError(t, err)
	require.Equal(t, want, have, "export must reproduce the imported state")

	// The by-entity index is rebuilt from the offences: exactly the two linked
	// duplicate-vote pairs, not the light-client-attack offence.
	var pairs []collections.Pair[string, []byte]
	iter, err := f.keeper.OffencesByEntity.Iterate(f.ctx, nil)
	require.NoError(t, err)
	defer func() { _ = iter.Close() }()
	for ; iter.Valid(); iter.Next() {
		k, err := iter.Key()
		require.NoError(t, err)
		pairs = append(pairs, k)
	}
	require.Len(t, pairs, 2)
	hashes := map[string]bool{}
	for _, p := range pairs {
		require.Equal(t, "acme", p.K1())
		hashes[types.FormatEvidenceHash(p.K2())] = true
	}
	for _, o := range gs.Offences {
		require.Equal(t, o.EntityId == "acme", hashes[o.EvidenceHash], o.EvidenceHash)
	}

	seq, err := f.keeper.SanctionSeq.Peek(f.ctx)
	require.NoError(t, err)
	require.Equal(t, gs.NextSanctionId, seq)

	s, err := f.keeper.Sanctions.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "acme", s.EntityId)

	for _, raw := range []string{"removed-consensus-01", "removed-consensus-02"} {
		has, err := f.keeper.RemovedConsensus.Has(f.ctx, []byte(raw))
		require.NoError(t, err)
		require.True(t, has, "the removed consensus key %s is restored", raw)
	}
}

func TestGenesisRefusesAMalformedRemovedConsensusAddress(t *testing.T) {
	f := initFixture(t)
	gs := types.GenesisState{Params: types.DefaultParams(), RemovedConsensusAddresses: []string{"not-an-address"}}
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, gs), "removed_consensus_addresses[0]")
}

func TestGenesisZeroNextSanctionIDStartsAtOne(t *testing.T) {
	f := initFixture(t)
	gs := types.GenesisState{Params: types.DefaultParams(), NextSanctionId: 0}
	require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))

	seq, err := f.keeper.SanctionSeq.Peek(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), seq)

	first, err := f.keeper.SanctionSeq.Next(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), first)
}
