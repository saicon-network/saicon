package app

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// The staking backstop sleeps in a window that PreBlock opens and x/stakeengine's
// BeginBlocker closes. The window must cover every BeginBlocker that can slash
// (x/slashing, x/evidence), so x/stakeengine is last in both lists (S7 spec §6.4).
// Reads app_config.go, as stakeengine_wiring_test.go does.
func TestTheBackstopWindowCoversSlashing(t *testing.T) {
	src, err := os.ReadFile("app_config.go")
	require.NoError(t, err)
	text := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(src), "")

	order := func(field string) []string {
		m := regexp.MustCompile(field + `:\s*\[\]string\{([^}]*)\}`).FindStringSubmatch(text)
		require.NotNil(t, m, "%s not found in app_config.go", field)
		return regexp.MustCompile(`[A-Za-z0-9_.]+`).FindAllString(m[1], -1)
	}

	pre := order("PreBlockers")
	require.Equal(t, "stakeenginemoduletypes.ModuleName", pre[len(pre)-1],
		"x/stakeengine must be the LAST PreBlocker: it opens the window")
	begin := order("BeginBlockers")
	require.Equal(t, "stakeenginemoduletypes.ModuleName", begin[len(begin)-1],
		"x/stakeengine must be the LAST BeginBlocker: it closes the window slashing runs in")
}

// x/delegationrouter's InitGenesis needs the module it plans against, so it comes
// after x/stakeengine's (S7 spec §4).
func TestRouterInitsAfterStakeEngine(t *testing.T) {
	src, err := os.ReadFile("app_config.go")
	require.NoError(t, err)
	text := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(src), "")
	m := regexp.MustCompile(`InitGenesis:\s*\[\]string\{([^}]*)\}`).FindStringSubmatch(text)
	require.NotNil(t, m, "InitGenesis not found in app_config.go")
	order := regexp.MustCompile(`[A-Za-z0-9_.]+`).FindAllString(m[1], -1)

	idx := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}
	engine, router := idx("stakeenginemoduletypes.ModuleName"), idx("delegationroutermoduletypes.ModuleName")
	require.GreaterOrEqual(t, engine, 0)
	require.GreaterOrEqual(t, router, 0, "x/delegationrouter must be in InitGenesis")
	require.Greater(t, router, engine, "x/delegationrouter InitGenesis must come after x/stakeengine's")
}
