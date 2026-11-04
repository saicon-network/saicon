package app

import (
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	compliancetypes "github.com/saicon-network/saicon/chain/x/compliance/types"
)

// The two genesis paths are shell scripts, so a decision recorded in Go or in a
// document can drift from what a chain actually starts with. Reading the scripts
// themselves is the technique that already caught a genesis funding 4,762x the
// supply ceiling — see x/rewards/keeper/cap_test.go,
// TestShippedGenesisLeavesHeadroomUnderTheCap.
var genesisScripts = []string{
	"../docker/entrypoint-singlenode.sh",
	"../docker/testnet/gen-genesis.sh",
}

// unbondingSecondsRegister is the Register v1.0 unbonding period, 21 days.
const unbondingSecondsRegister = "1814400"

func TestBothGenesisPathsPinTheUnbondingTime(t *testing.T) {
	assign := regexp.MustCompile(`\.app_state\.staking\.params\.unbonding_time\s*=`)

	for _, script := range genesisScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			require.True(t, assign.Match(src),
				"%s never assigns staking.params.unbonding_time, so the 21-day cooldown is "+
					"whatever the SDK ships that day. V-5 and four script comments describe it "+
					"as a decision of ours", script)

			require.Contains(t, string(src), unbondingSecondsRegister,
				"%s must pin the unbonding time to %ss (21 days, Register v1.0)",
				script, unbondingSecondsRegister)
		})
	}
}

// evidenceWindowScripts are the scripts that size something by the evidence
// window: the two genesis paths write it into consensus.params.evidence, and all
// three set min-retain-blocks to it.
var evidenceWindowScripts = append([]string{"../docker/testnet/entrypoint-node.sh"}, genesisScripts...)

// The evidence window is the unbonding period, no shorter and no longer (owner
// 2026-10-29): the v0.10.0 upgrade sets it by evidenceWindow, and a chain made
// by a genesis script must start where an upgraded one ends. Each script must
// compute EVIDENCE_BLOCKS as UNBONDING_SECONDS / evidenceMaxBlockTime with the
// Register's 21 days, and the genesis paths must write it and the duration
// UNBONDING_SECONDS into the evidence params. Expiry needs BOTH limits passed
// (evidence/pool.go isExpired uses &&), so the window is max(blocks x block
// time, duration): too few blocks and a short duration leave a double-signer
// unpunishable while still bonded; too many blocks or a longer duration let old
// evidence reach a validator re-created on the key.
func TestTheScriptsFollowTheEvidenceWindowRule(t *testing.T) {
	unbondingRe := regexp.MustCompile(`(?m)^\s*UNBONDING_SECONDS=(\d+)\b`)
	blocksRe := regexp.MustCompile(`(?m)^\s*EVIDENCE_BLOCKS=(.*?)\s*(#.*)?$`)
	ruleRe := regexp.MustCompile(`^\$\(\( UNBONDING_SECONDS / (\d+) \)\)$`)

	u, err := time.ParseDuration(unbondingSecondsRegister + "s")
	require.NoError(t, err)
	wantBlocks, wantAge := evidenceWindow(u)

	for _, script := range evidenceWindowScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			units := unbondingRe.FindAllSubmatch(src, -1)
			require.NotEmpty(t, units, "%s never sets UNBONDING_SECONDS", script)
			for _, m := range units {
				require.Equal(t, unbondingSecondsRegister, string(m[1]),
					"%s sets UNBONDING_SECONDS to %s, the Register's unbonding is %ss", script, m[1], unbondingSecondsRegister)
			}

			assigns := blocksRe.FindAllSubmatch(src, -1)
			require.NotEmpty(t, assigns, "%s never sets EVIDENCE_BLOCKS", script)
			for _, m := range assigns {
				rule := ruleRe.FindSubmatch(m[1])
				require.NotNil(t, rule, "%s sets EVIDENCE_BLOCKS=%s; it must be $(( UNBONDING_SECONDS / %d ))",
					script, m[1], int64(evidenceMaxBlockTime/time.Second))
				divisor, err := strconv.ParseInt(string(rule[1]), 10, 64)
				require.NoError(t, err)
				require.Equal(t, int64(evidenceMaxBlockTime/time.Second), divisor,
					"%s divides the unbonding period by %ds, the upgrade by %s (app/upgrades.go evidenceMaxBlockTime)",
					script, divisor, evidenceMaxBlockTime)
				require.Equal(t, wantBlocks, int64(u/time.Second)/divisor, "%s", script)
			}
		})
	}

	for _, script := range genesisScripts {
		t.Run(path.Base(script)+"/genesis", func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)
			for _, needle := range []string{
				`--arg n "${EVIDENCE_BLOCKS}"`,
				`.consensus.params.evidence.max_age_num_blocks = $n`,
				`--arg d "${UNBONDING_SECONDS}000000000"`,
				`.consensus.params.evidence.max_age_duration = $d`,
			} {
				require.Contains(t, string(src), needle,
					"%s must write the evidence window as the upgrade does (%d blocks, %s): %s missing",
					script, wantBlocks, wantAge, needle)
			}
		})
	}
}

// validatorCapFloorBy is the floor each genesis script sets, in nscn. The
// single-node devnet runs one validator holding 1,000,000 SCN, whose strict 2%
// cap would be 20,000 SCN: it would accept no delegation at all, so it sets
// 2,000,000 SCN. The S7 3-validator devnet sets 400,000 SCN. The code default
// is 0 — the strict rule — so the floor exists only where a genesis script sets
// it (spec §5.0).
var validatorCapFloorBy = map[string]string{
	"entrypoint-singlenode.sh": "200000000000000",
	"gen-genesis.sh":           "40000000000000",
}

func TestBothGenesisPathsSetTheValidatorCapFloor(t *testing.T) {
	assign := regexp.MustCompile(`\.app_state\.stakeengine\.params\.validator_cap_floor\s*=`)

	for _, script := range genesisScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			require.True(t, assign.Match(src),
				"%s never sets stakeengine.params.validator_cap_floor, so the devnet starts on "+
					"the strict 2%% rule and its one validator accepts no delegation", script)
			want, ok := validatorCapFloorBy[path.Base(script)]
			require.True(t, ok, "no expected floor listed for %s", script)
			require.Contains(t, string(src), want,
				"%s must set the floor to %s nscn", script, want)
		})
	}
}

// TestBothGenesisPathsAttestTheirValidators pins the S8 devnet decision: the
// activation gate admits a validator to ACTIVE only as a member of an attested
// entity (spec §7.4, §18), so each genesis script must write the registry state
// for its genesis validators.
func TestBothGenesisPathsAttestTheirValidators(t *testing.T) {
	for _, script := range genesisScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)
			for _, needle := range []string{
				".app_state.entityregistry.params.attestors",
				".app_state.entityregistry.entities",
				".app_state.entityregistry.members",
				".app_state.stakeengine.params.entity_cap_floor",
			} {
				require.Contains(t, string(src), needle,
					"%s must set %s: without an entity for each genesis validator the activation gate "+
						"holds every one of them at BONDING and nothing earns (spec §7.4)", script, needle)
			}
		})
	}
}

// entityCapFloorAssignment is the exact jq assignment each genesis script makes
// to stakeengine.params.entity_cap_floor (spec §18). The single-node script
// copies validator_cap_floor, so the two stay equal even when
// STAKEENGINE_CAP_FLOOR overrides the validator floor (the entity floor bound
// is [validator_cap_floor, 5,250,000 SCN]); the 3-validator script sets the
// entity floor to 400,000 SCN as a literal, equal to the validator floor it
// sets.
var entityCapFloorAssignment = map[string]string{
	"entrypoint-singlenode.sh": `.app_state.stakeengine.params.entity_cap_floor = .app_state.stakeengine.params.validator_cap_floor`,
	"gen-genesis.sh":           `.app_state.stakeengine.params.entity_cap_floor = "40000000000000"`,
}

func TestBothGenesisPathsPinTheEntityCapFloor(t *testing.T) {
	for _, script := range genesisScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			want, ok := entityCapFloorAssignment[path.Base(script)]
			require.True(t, ok, "no expected entity_cap_floor assignment listed for %s", script)
			require.Contains(t, string(src), want,
				"%s must set the entity cap floor as `%s`: below the validator floor the genesis "+
					"is refused, and an unset floor leaves the strict 5%% rule on a 1-3 validator devnet",
				script, want)
		})
	}
}

// TestBothGenesisPathsBindVrfKeys: every genesis validator gets a VRF key bound
// in genesis, with its owner entry, and the initial input is drawn after the
// keys (W2 spec §17, K18).
func TestBothGenesisPathsBindVrfKeys(t *testing.T) {
	needles := []struct{ needle, why string }{
		{"vrf keygen", "a genesis validator needs a VRF key made at genesis time (spec §17)"},
		{"vrf_public_key", "the member row must carry the validator's VRF key (spec §17)"},
		{".app_state.entityregistry.vrf_key_owners", "the lifetime key-to-validator index must list each genesis key (spec §17)"},
		{".app_state.randomness.initial_input", "round 1's input must be set in genesis, not left to the module default (spec §17, K18)"},
		{"/dev/urandom", "the initial input must be random, not a constant (spec §17, K18)"},
	}

	for _, script := range genesisScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			raw, err := os.ReadFile(script)
			require.NoError(t, err)
			src := string(raw)

			for _, n := range needles {
				require.Contains(t, src, n.needle, "%s lacks %q: %s", script, n.needle, n.why)
			}
			require.Greater(t, strings.Index(src, "/dev/urandom"), strings.Index(src, "vrf keygen"),
				"%s draws the initial input before the VRF keys exist, so a key could have been "+
					"chosen against it (spec §17, K18)", script)
		})
	}
}

// TestTheImageCarriesTheSubmitLoop: the validator's submitter loop ships in the
// image at the path the operator runs it from (W2 spec §16.1).
func TestTheImageCarriesTheSubmitLoop(t *testing.T) {
	src, err := os.ReadFile("../Dockerfile")
	require.NoError(t, err)
	require.Contains(t, string(src),
		"COPY ./scripts/vrf-submit-loop.sh /usr/local/bin/vrf-submit-loop.sh",
		"the image must carry the VRF submitter loop (spec §16.1)")
}

// The coordinated-attack fraction may not sit below the double-sign fraction in
// force (spec C20), and its code floor is that fraction as the chain starts:
// the floor, the binary's default genesis and both genesis scripts say the same
// number. Moving one without the others fails here.
func TestCoordinatedFloorIsTheDoubleSignFraction(t *testing.T) {
	floor := compliancetypes.CoordinatedFractionFloor()

	require.True(t, DoubleSignSlashFraction().Equal(floor),
		"the binary's default double-sign fraction %s must equal the coordinated-attack floor %s",
		DoubleSignSlashFraction(), floor)

	for _, script := range genesisScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			m := regexp.MustCompile(`slash_fraction_double_sign = "([0-9.]+)"`).FindSubmatch(src)
			require.NotNil(t, m, "%s does not set slash_fraction_double_sign", script)
			inScript, err := math.LegacyNewDecFromStr(string(m[1]))
			require.NoError(t, err)
			require.True(t, inScript.Equal(floor),
				"%s sets slash_fraction_double_sign to %s, the coordinated-attack floor is %s",
				script, inScript, floor)
		})
	}
}
