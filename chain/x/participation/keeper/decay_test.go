package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/participation/keeper"
)

func TestDecayEP(t *testing.T) {
	half := math.LegacyMustNewDecFromStr("0.5")
	zero := math.LegacyZeroDec()

	cases := []struct {
		name   string
		ep     int64
		idle   int64
		decay  math.LegacyDec
		expect int64
	}{
		{"decay off keeps EP", 8, 5, zero, 8},
		{"no idle keeps EP", 8, 0, half, 8},
		{"one idle epoch halves", 8, 1, half, 4},
		{"gap of three", 8, 3, half, 1},  // 8 * 0.125 = 1.0
		{"floors at one", 2, 3, half, 1}, // 2 * 0.125 = 0.25 -> trunc 0 -> floor 1
		{"ep one stays one", 1, 4, half, 1},
		{"grace zero stays zero", 0, 4, half, 0},
		{"negative idle no-op", 8, -2, half, 8},
		{"large idle floors", 1000, 200, half, 1},                                   // 1000 * 0.5^200 -> ~0 -> floor 1
		{"near-1 decay one epoch", 100, 1, math.LegacyMustNewDecFromStr("0.99"), 1}, // floor(100*0.01)=1
		{"ep two halves to floor", 2, 1, half, 1},                                   // floor(2*0.5)=1
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keeper.DecayEPForTest(math.NewInt(tc.ep), tc.idle, tc.decay)
			require.Equal(t, tc.expect, got.Int64())
		})
	}
}
