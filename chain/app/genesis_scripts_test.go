package app

import (
	"os"
	"path"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestBothGenesisPathsCoverTheEvidenceWindow(t *testing.T) {
	for _, script := range genesisScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			for _, key := range []string{"max_age_num_blocks", "max_age_duration"} {
				require.Contains(t, string(src), key,
					"%s does not set consensus.params.evidence.%s, so it keeps CometBFT's stock "+
						"100,000 blocks / 48h. Expiry needs BOTH limits passed (evidence/pool.go "+
						"isExpired uses &&), so the longer governs: 5.8 days against a 21-day "+
						"unbonding period leaves fifteen days in which a proven double-signer "+
						"keeps their stake", script, key)
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
