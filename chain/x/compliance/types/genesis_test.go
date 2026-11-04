package types_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func hashOf(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return types.FormatEvidenceHash(sum[:])
}

func valcons(t *testing.T, seed string) string {
	t.Helper()
	sum := sha256.Sum256([]byte("cons/" + seed))
	s, err := bech32.ConvertAndEncode("saiconvalcons", sum[:20])
	require.NoError(t, err)
	return s
}

func valoper(t *testing.T, seed string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(seed))
	s, err := bech32.ConvertAndEncode("saiconvaloper", sum[:20])
	require.NoError(t, err)
	return s
}

// validGenesis is a populated, valid state: entity "acme" holds two linked
// duplicate-vote offences (validators 1 and 2), one light-client-attack offence
// of validator 3 has no entity, and sanction 1 fires the first of acme's.
func validGenesis(t *testing.T) *types.GenesisState {
	t.Helper()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	offence := func(seed string, kind types.MisbehaviorKind, entity string, infraction int64) types.Offence {
		return types.Offence{
			EvidenceHash:       hashOf(seed),
			Validator:          valoper(t, seed),
			ConsensusAddress:   valcons(t, seed),
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
	return &types.GenesisState{
		Params:   types.DefaultParams(),
		Offences: []types.Offence{a, b, c},
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
			Authority: "saicon1gov",
			Reason:    "coordinated double-sign",
			Height:    500,
			Time:      at.Add(10 * 24 * time.Hour),
		}},
		NextSanctionId:            2,
		RemovedConsensusAddresses: []string{valcons(t, "v4")},
	}
}

func TestGenesisState_Validate(t *testing.T) {
	require.NoError(t, types.DefaultGenesis().Validate())
	require.Equal(t, uint64(1), types.DefaultGenesis().NextSanctionId)
	require.NoError(t, validGenesis(t).Validate())

	t.Run("zero params are refused", func(t *testing.T) {
		require.Error(t, (&types.GenesisState{}).Validate())
	})

	t.Run("next_sanction_id 0 reads as 1", func(t *testing.T) {
		gs := types.DefaultGenesis()
		gs.NextSanctionId = 0
		require.NoError(t, gs.Validate())

		// With a sanction of id 1 present, 0 reads as 1 and is not above it.
		gs = validGenesis(t)
		gs.NextSanctionId = 0
		require.Error(t, gs.Validate())
	})
}

func TestGenesisValidateRefusesEachBrokenRule(t *testing.T) {
	cases := []struct {
		name   string
		want   string
		break_ func(gs *types.GenesisState)
	}{
		{"bad params", "appeal_window_seconds", func(gs *types.GenesisState) { gs.Params.AppealWindowSeconds = 5 }},
		{"bad hash", "invalid evidence hash", func(gs *types.GenesisState) { gs.Offences[1].EvidenceHash = "abc" }},
		{"lowercase hash", "invalid evidence hash", func(gs *types.GenesisState) {
			gs.Offences[1].EvidenceHash = "9f" + gs.Offences[1].EvidenceHash[2:]
		}},
		{"duplicate hash", "appears twice", func(gs *types.GenesisState) { gs.Offences[1].EvidenceHash = gs.Offences[0].EvidenceHash }},
		{"kind unspecified", "has kind", func(gs *types.GenesisState) {
			gs.Offences[1].Kind = types.MisbehaviorKind_MISBEHAVIOR_KIND_UNSPECIFIED
		}},
		{"kind out of range", "has kind", func(gs *types.GenesisState) { gs.Offences[1].Kind = 9 }},
		{"infraction height 0", "heights must be >= 1", func(gs *types.GenesisState) { gs.Offences[1].InfractionHeight = 0 }},
		{"recorded height 0", "heights must be >= 1", func(gs *types.GenesisState) { gs.Offences[1].RecordedHeight = 0 }},
		{"light client attack with an entity", "never linked to an entity", func(gs *types.GenesisState) { gs.Offences[2].EntityId = "acme" }},
		{"recorded before infraction", "before its infraction", func(gs *types.GenesisState) { gs.Offences[1].RecordedHeight = 101 }},
		{"double sign fraction 1.5", "double_sign_fraction", func(gs *types.GenesisState) {
			gs.Offences[1].DoubleSignFraction = math.LegacyMustNewDecFromStr("1.5")
		}},
		{"double sign fraction negative", "double_sign_fraction", func(gs *types.GenesisState) {
			gs.Offences[1].DoubleSignFraction = math.LegacyMustNewDecFromStr("-0.1")
		}},
		{"double sign fraction nil", "double_sign_fraction", func(gs *types.GenesisState) { gs.Offences[1].DoubleSignFraction = math.LegacyDec{} }},
		{"cosmosvaloper validator", "has prefix", func(gs *types.GenesisState) {
			sum := sha256.Sum256([]byte("v2"))
			s, err := bech32.ConvertAndEncode("cosmosvaloper", sum[:20])
			require.NoError(t, err)
			gs.Offences[1].Validator = s
		}},
		{"offence power 0", "power", func(gs *types.GenesisState) { gs.Offences[1].Power = 0 }},
		{"offence consensus address empty", "consensus_address", func(gs *types.GenesisState) { gs.Offences[1].ConsensusAddress = "" }},
		{"offence consensus address not bech32", "consensus_address", func(gs *types.GenesisState) {
			gs.Offences[1].ConsensusAddress = "saiconvalcons1v2"
		}},
		{"offence consensus address with the operator prefix", "has prefix", func(gs *types.GenesisState) {
			gs.Offences[1].ConsensusAddress = gs.Offences[1].Validator
		}},
		{"target classification unspecified", "classification", func(gs *types.GenesisState) {
			gs.Sanctions[0].Targets[0].Classification = types.TargetClassification_TARGET_CLASSIFICATION_UNSPECIFIED
		}},
		{"target classification out of range", "classification", func(gs *types.GenesisState) {
			gs.Sanctions[0].Targets[0].Classification = 9
		}},
		{"a slashed target skipped for its key", "is not slashable", func(gs *types.GenesisState) {
			gs.Sanctions[0].Targets[0].Classification = types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_KEY_REUSED
		}},
		{"removed consensus address malformed", "removed_consensus_addresses", func(gs *types.GenesisState) {
			gs.RemovedConsensusAddresses = []string{"nope"}
		}},
		{"removed consensus address with the operator prefix", "has prefix", func(gs *types.GenesisState) {
			gs.RemovedConsensusAddresses = []string{gs.Offences[0].Validator}
		}},
		{"removed consensus address twice", "appears twice", func(gs *types.GenesisState) {
			gs.RemovedConsensusAddresses = append(gs.RemovedConsensusAddresses, gs.RemovedConsensusAddresses[0])
		}},
		{"target extra fraction nil", "extra_fraction", func(gs *types.GenesisState) { gs.Sanctions[0].Targets[0].ExtraFraction = math.LegacyDec{} }},
		{"target extra fraction negative", "extra_fraction", func(gs *types.GenesisState) {
			gs.Sanctions[0].Targets[0].ExtraFraction = math.LegacyMustNewDecFromStr("-0.1")
		}},
		{"target validator burned nil", "validator_burned", func(gs *types.GenesisState) { gs.Sanctions[0].Targets[0].ValidatorBurned = math.Int{} }},
		{"target validator burned negative", "validator_burned", func(gs *types.GenesisState) { gs.Sanctions[0].Targets[0].ValidatorBurned = math.NewInt(-1) }},
		{"target base tokens nil", "base_tokens", func(gs *types.GenesisState) { gs.Sanctions[0].Targets[0].BaseTokens = math.Int{} }},
		{"target base tokens negative", "base_tokens", func(gs *types.GenesisState) { gs.Sanctions[0].Targets[0].BaseTokens = math.NewInt(-1) }},
		{"target outcome unspecified", "outcome", func(gs *types.GenesisState) {
			gs.Sanctions[0].Targets[0].Outcome = types.TargetOutcome_TARGET_OUTCOME_UNSPECIFIED
		}},
		{"target outcome out of range", "outcome", func(gs *types.GenesisState) { gs.Sanctions[0].Targets[0].Outcome = 9 }},
		{"target validator is not its offence's", "is not the validator of its offence", func(gs *types.GenesisState) {
			gs.Sanctions[0].Targets[0].Validator = gs.Offences[1].Validator
		}},
		{"bad entity id", "entity_id", func(gs *types.GenesisState) { gs.Offences[1].EntityId = "A" }},
		{"sanction id 0", "sanction id 0", func(gs *types.GenesisState) { gs.Sanctions[0].Id = 0 }},
		{"duplicate sanction id", "appears twice", func(gs *types.GenesisState) {
			gs.Sanctions = append(gs.Sanctions, gs.Sanctions[0])
		}},
		{"sanction id at next_sanction_id", "not below next_sanction_id", func(gs *types.GenesisState) { gs.NextSanctionId = 1 }},
		{"sanction fraction 0.05", "out of bounds", func(gs *types.GenesisState) {
			gs.Sanctions[0].Fraction = math.LegacyNewDecWithPrec(5, 2)
		}},
		{"zero targets", "has no targets", func(gs *types.GenesisState) {
			gs.Sanctions[0].Targets = nil
			gs.Offences[0].SanctionId = 0
		}},
		{"target hash unknown", "not a recorded offence", func(gs *types.GenesisState) { gs.Sanctions[0].Targets[0].EvidenceHash = hashOf("nobody") }},
		{"target hash twice in one sanction", "is listed by sanctions", func(gs *types.GenesisState) {
			gs.Sanctions[0].Targets = append(gs.Sanctions[0].Targets, gs.Sanctions[0].Targets[0])
		}},
		{"target offence carries another sanction id", "carries sanction id 7", func(gs *types.GenesisState) { gs.Offences[0].SanctionId = 7 }},
		{"target offence of another entity", "is linked to", func(gs *types.GenesisState) { gs.Sanctions[0].EntityId = "other" }},
		{"offence sanction id not listed by that sanction", "does not list it", func(gs *types.GenesisState) { gs.Offences[1].SanctionId = 1 }},
		{"offence sanction id of no sanction", "does not list it", func(gs *types.GenesisState) { gs.Offences[1].SanctionId = 5 }},
		{"two sanctions list one offence", "is listed by sanctions", func(gs *types.GenesisState) {
			second := gs.Sanctions[0]
			second.Id = 2
			second.Targets = append([]types.SanctionTarget(nil), second.Targets...)
			gs.Sanctions = append(gs.Sanctions, second)
			gs.NextSanctionId = 3
		}},
		{"entity armed by one validator only", "is not armed", func(gs *types.GenesisState) {
			// Both of acme's offences are validator 1's.
			gs.Offences[1].Validator = gs.Offences[0].Validator
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := validGenesis(t)
			tc.break_(gs)
			err := gs.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}
