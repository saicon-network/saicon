package app

import (
	"os"
	"path"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// The retention profiles live in the entrypoint scripts, not in Go, so the same
// technique the genesis tests use applies here: read the script rather than trust a
// document. §2.10.2 puts three of the five node roles in the tree — validator, full
// and archive — and a profile that exists only in prose is a profile an operator
// cannot select.
var entrypointScripts = []string{
	"../docker/entrypoint-singlenode.sh",
	"../docker/testnet/entrypoint-node.sh",
}

// nodeProfiles are the values NODE_PROFILE must accept. `full` is the middle
// profile: the validator's state window with the archive's block retention, which
// is what an explorer needs — blocks and transactions far more than historical
// state, and state is the part that grows fastest.
var nodeProfiles = []string{"validator", "full", "archive"}

func TestEntrypointsCarryAllThreeNodeProfiles(t *testing.T) {
	for _, script := range entrypointScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			for _, profile := range nodeProfiles {
				arm := regexp.MustCompile(`(?m)^\s*` + profile + `\)`)
				require.True(t, arm.Match(src),
					"%s has no %q arm, so an operator asking for that profile does not get it",
					script, profile)
			}
		})
	}
}

// A typo in a deployment must not quietly hand someone a different node than they
// asked for. The original two-value form was `if archive ... else`, which meant every
// unrecognised value silently became a validator — including `NODE_PROFILE=ful`.
func TestUnknownNodeProfileIsRejected(t *testing.T) {
	fallthroughArm := regexp.MustCompile(`(?s)\*\)(.{0,400}?)(exit 1|exit \$)`)

	for _, script := range entrypointScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			require.True(t, fallthroughArm.Match(src),
				"%s does not reject an unknown NODE_PROFILE. Without a default arm that exits "+
					"non-zero, a misspelled profile is indistinguishable from the default and the "+
					"node runs a retention policy nobody chose", script)
		})
	}
}

// The SDK does not check this for you: a snapshot is taken from a state version, so
// pruning-keep-recent must outlive snapshot-interval x snapshot-keep-recent or the
// versions a snapshot needs are gone before it is taken — and a node that cannot
// serve state-sync leaves a joining operator no route in but a full replay.
func TestPruningWindowOutlivesTheSnapshotsItFeeds(t *testing.T) {
	keepRecent := regexp.MustCompile(`pruning-keep-recent = "(\d+)"`)
	interval := regexp.MustCompile(`snapshot-interval = (\d+)`)
	keepSnaps := regexp.MustCompile(`snapshot-keep-recent = (\d+)`)

	atoi := func(t *testing.T, s string) int {
		t.Helper()
		n, err := strconv.Atoi(s)
		require.NoError(t, err)
		return n
	}

	for _, script := range entrypointScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			iv := interval.FindSubmatch(src)
			ks := keepSnaps.FindSubmatch(src)
			require.NotNil(t, iv, "%s does not set snapshot-interval", script)
			require.NotNil(t, ks, "%s does not set snapshot-keep-recent", script)
			need := atoi(t, string(iv[1])) * atoi(t, string(ks[1]))

			windows := keepRecent.FindAllSubmatch(src, -1)
			require.NotEmpty(t, windows,
				"%s sets no pruning-keep-recent, so every pruning profile takes the SDK default",
				script)

			for _, w := range windows {
				got := atoi(t, string(w[1]))
				require.Greater(t, got, need,
					"%s sets pruning-keep-recent=%d against snapshot-interval x "+
						"snapshot-keep-recent=%d. State-sync snapshots would reference versions "+
						"already pruned", script, got, need)
			}
		})
	}
}
