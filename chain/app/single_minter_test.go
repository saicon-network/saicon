package app_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// B12 / ADR-0011 D3 — THE SINGLE MINT PATH.
//
// x/rewards mints the epoch pool and clamps it to the headroom below the 21M
// ceiling. That guarantee is only worth anything if nothing else can create
// SCN. x/mint used to be wired in and to run first in BeginBlock on SDK
// defaults (7-20% inflation), so the cap guarded one of two minters; zeroing it
// in the genesis template helped only chains created from that template.
//
// The module is now removed from the app entirely. This test reads the wiring
// so the removal cannot be quietly undone: a second Minter permission, or
// x/mint reappearing in the module list, fails here rather than in production
// where it would silently break the supply ceiling.
func TestOnlyRewardsMayMint(t *testing.T) {
	src, err := os.ReadFile("app_config.go")
	require.NoError(t, err)

	// Matches Minter wherever it appears in the permission list, not only alone —
	// x/ibctransfer holds Minter alongside Burner, and an over-narrow pattern here
	// would report a clean wiring while a second minter sat one comma away.
	minters := regexp.MustCompile(`\{Account: ([A-Za-z0-9_.]+), Permissions: \[\]string\{[^}]*authtypes\.Minter[^}]*\}`).
		FindAllSubmatch(src, -1)

	got := make([]string, 0, len(minters))
	for _, m := range minters {
		got = append(got, string(m[1]))
	}

	// x/ibctransfer must keep its Minter: it mints IBC voucher denoms, never nscn.
	require.ElementsMatch(t,
		[]string{"rewardsmoduletypes.ModuleName", "ibctransfertypes.ModuleName"},
		got,
		"exactly two modules may hold Minter, and only x/rewards may create nscn")
}

// x/mint must not be in the app at all — not as a module, not in a block order,
// not as a keeper. Zeroing its inflation in genesis was the reversible stopgap;
// this is the disposition B12 asks for.
func TestMintModuleIsNotWiredIn(t *testing.T) {
	for _, f := range []string{"app_config.go", "app.go"} {
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		require.NotContains(t, string(src), "minttypes.",
			"%s still references x/mint; it is a second minter the 21M cap does not guard", f)
		// The import path, not just the qualified identifiers. A blank import of
		// x/mint carries no keeper and no module-list entry, so the two checks
		// below both pass with it present — which is exactly how one survived
		// B12 in app_config.go for two weeks, reading to anyone skimming the
		// file as though the module were still wired in.
		require.NotContains(t, string(src), "cosmos-sdk/x/mint",
			"%s still imports x/mint; even a blank import claims a wiring that no longer exists", f)
		require.NotContains(t, string(src), "mintkeeper.",
			"%s still holds a mint keeper", f)
	}
}
