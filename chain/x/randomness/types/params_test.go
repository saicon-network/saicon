package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

func TestDefaultParamsAreTheSpecValues(t *testing.T) {
	p := types.DefaultParams()
	require.Equal(t, uint32(7), p.RotationEpochs)
	require.Equal(t, uint64(100), p.WindowBlocks)
	require.NoError(t, p.Validate())
}

func TestParamBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*types.Params)
		ok   bool
	}{
		{"rotation 0", func(p *types.Params) { p.RotationEpochs = 0 }, false},
		{"rotation 1", func(p *types.Params) { p.RotationEpochs = 1 }, true},
		{"rotation 28", func(p *types.Params) { p.RotationEpochs = 28 }, true},
		{"rotation 29", func(p *types.Params) { p.RotationEpochs = 29 }, false},
		{"window 9", func(p *types.Params) { p.WindowBlocks = 9 }, false},
		{"window 10", func(p *types.Params) { p.WindowBlocks = 10 }, true},
		{"window 200000", func(p *types.Params) { p.WindowBlocks = 200000 }, true},
		{"window 200001", func(p *types.Params) { p.WindowBlocks = 200001 }, false},
	} {
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

func TestConstants(t *testing.T) {
	require.Equal(t, "randomness", types.ModuleName)
	require.Equal(t, uint32(22), types.ShadowLotterySeats)
	require.Equal(t, uint64(52), types.MaxDrawHistory)
	require.Equal(t, uint32(2), uint32(types.QuorumNumerator))
	require.Equal(t, uint32(3), uint32(types.QuorumDenominator))
	require.Equal(t, "saicon/randomness/alpha/v1", types.AlphaDomain)
	require.Equal(t, "saicon/randomness/seed/v1", types.SeedDomain)
	require.Equal(t, "saicon/randomness/draw/v1", types.DrawDomain)
	require.Equal(t, "saicon/randomness/initial-input/v1", types.InitialInputDomain)
}
