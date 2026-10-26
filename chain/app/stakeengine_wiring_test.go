package app

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// The escrow must refuse plain transfers. Anyone could otherwise send it one
// nscn, V-10 would see a balance the queue does not account for, and the chain
// would halt — a one-transaction denial of service.
func TestTheStakeQueueEscrowIsABlockedAddress(t *testing.T) {
	require.True(t, BlockedAddresses()[stakeenginetypes.ModuleName])
}

// The escrow holds coins but must never create, destroy or stake them itself:
// fills delegate from the delegator's own account.
func TestTheStakeQueueEscrowHoldsNoPermissions(t *testing.T) {
	perms, ok := GetMaccPerms()[stakeenginetypes.ModuleName]
	require.True(t, ok, "the escrow needs a module account")
	require.Empty(t, perms)
}

// x/stakeengine's order in the runtime module lists is load-bearing. Its
// InitGenesis computes the first caps from the bonded genesis set, so it needs
// x/staking and x/genutil (gentxs bond the genesis validators) to have run, and
// x/participation's state to exist. Its EndBlocker reads the epoch x/participation
// has just closed and the validator set x/staking has just updated. This reads
// app_config.go, as TestOnlyRewardsMayMint does, so a reordering fails here.
func TestStakeEngineRunsAfterItsDependenciesInTheBlockOrders(t *testing.T) {
	src, err := os.ReadFile("app_config.go")
	require.NoError(t, err)
	// Drop line comments: they name modules too ("x/stakeengine last: ...").
	text := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(src), "")

	order := func(field string) []string {
		m := regexp.MustCompile(field + `:\s*\[\]string\{([^}]*)\}`).FindStringSubmatch(text)
		require.NotNil(t, m, "%s not found in app_config.go", field)
		var names []string
		names = append(names, regexp.MustCompile(`[A-Za-z0-9_.]+`).FindAllString(m[1], -1)...)
		return names
	}
	index := func(names []string, want string) int {
		for i, n := range names {
			if n == want {
				return i
			}
		}
		return -1
	}
	after := func(field string, dependencies ...string) {
		names := order(field)
		se := index(names, "stakeenginemoduletypes.ModuleName")
		require.GreaterOrEqual(t, se, 0, "%s must list x/stakeengine", field)
		for _, dep := range dependencies {
			i := index(names, dep)
			require.GreaterOrEqual(t, i, 0, "%s must list %s", field, dep)
			require.Less(t, i, se, "%s: x/stakeengine must come after %s", field, dep)
		}
	}

	after("InitGenesis", "stakingtypes.ModuleName", "genutiltypes.ModuleName", "participationmoduletypes.ModuleName")
	after("EndBlockers", "stakingtypes.ModuleName", "participationmoduletypes.ModuleName")
}
