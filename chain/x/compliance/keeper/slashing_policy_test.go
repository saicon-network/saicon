package keeper_test

import (
	"os"
	"path"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Register's slashing policy (ADR-0010 §1-2) lives in the genesis templates,
// not in Go — x/slashing owns the params. This test pins the two values there,
// because the compliance hook's correctness DEPENDS on one of them.
//
// Specifically: BeforeValidatorSlashed treats any positive burn as a double-sign.
// That is exact only while downtime carries a ZERO stake slash, since x/staking
// skips the hook entirely on a zero burn. If someone restores a positive downtime
// fraction, every downtime slash starts jailing validators as double-signers and
// tombstoning them permanently. This test is the tripwire for that.
func TestShippedGenesisCarriesTheRegisterSlashingPolicy(t *testing.T) {
	for _, script := range []string{
		"../../../docker/entrypoint-singlenode.sh",
		"../../../docker/testnet/gen-genesis.sh",
	} {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			downtime := regexp.MustCompile(`slash_fraction_downtime = "([0-9.]+)"`).FindSubmatch(src)
			require.NotNil(t, downtime, "the template must set slash_fraction_downtime")
			require.Equal(t, "0.000000000000000000", string(downtime[1]),
				"downtime must carry NO stake slash: the compliance hook infers a double-sign "+
					"from any positive burn, which is only exact while this is zero")

			doubleSign := regexp.MustCompile(`slash_fraction_double_sign = "([0-9.]+)"`).FindSubmatch(src)
			require.NotNil(t, doubleSign, "the template must set slash_fraction_double_sign")
			require.Equal(t, "0.100000000000000000", string(doubleSign[1]),
				"the Register doubles the SDK's 5% to 10%")
		})
	}
}
