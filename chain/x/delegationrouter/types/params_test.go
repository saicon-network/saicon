package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
)

func TestDefaultParamsAreTheSpecValues(t *testing.T) {
	p := types.DefaultParams()
	require.NoError(t, p.Validate())
	require.Equal(t, "0.750000000000000000", p.MinRouteScore.String())
	require.Equal(t, uint32(2), p.MaxRouteLegs)
}

func TestParamsBounds(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*types.Params)
		ok   bool
	}{
		{"score nil", func(p *types.Params) { p.MinRouteScore = math.LegacyDec{} }, false},
		{"score below 0.50", func(p *types.Params) { p.MinRouteScore = math.LegacyNewDecWithPrec(49, 2) }, false},
		{"score at 0.50", func(p *types.Params) { p.MinRouteScore = math.LegacyNewDecWithPrec(50, 2) }, true},
		{"score at 1.00", func(p *types.Params) { p.MinRouteScore = math.LegacyOneDec() }, true},
		{"score above 1.00", func(p *types.Params) { p.MinRouteScore = math.LegacyNewDecWithPrec(101, 2) }, false},
		{"legs 0", func(p *types.Params) { p.MaxRouteLegs = 0 }, false},
		{"legs 1", func(p *types.Params) { p.MaxRouteLegs = 1 }, true},
		{"legs 4", func(p *types.Params) { p.MaxRouteLegs = 4 }, true},
		{"legs 5", func(p *types.Params) { p.MaxRouteLegs = 5 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := types.DefaultParams()
			tc.mut(&p)
			if tc.ok {
				require.NoError(t, p.Validate())
			} else {
				require.Error(t, p.Validate())
			}
		})
	}
}
