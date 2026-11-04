package types_test

import (
	"bytes"
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

func TestDefaultsAreTheADRValues(t *testing.T) {
	p := types.DefaultParams()
	require.Equal(t, uint32(2), p.MaxActiveSeats)
	require.Empty(t, p.Attestors)
	require.NoError(t, p.Validate())
	require.Equal(t, 8, types.MaxMembersPerEntity)
	require.Equal(t, uint32(1), types.TicketsPerEntity)
}

func TestParamBounds(t *testing.T) {
	a1 := sdk.MustBech32ifyAddressBytes("saicon", bytes.Repeat([]byte{1}, 20))
	a2 := sdk.MustBech32ifyAddressBytes("saicon", bytes.Repeat([]byte{2}, 20))
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = sdk.MustBech32ifyAddressBytes("saicon", bytes.Repeat([]byte{byte(i + 10)}, 20))
	}
	ten := eleven[:10]
	for _, tc := range []struct {
		name string
		mut  func(*types.Params)
		ok   bool
	}{
		{"seats 0", func(p *types.Params) { p.MaxActiveSeats = 0 }, false},
		{"seats 1", func(p *types.Params) { p.MaxActiveSeats = 1 }, true},
		{"seats 3", func(p *types.Params) { p.MaxActiveSeats = 3 }, true},
		{"seats 4", func(p *types.Params) { p.MaxActiveSeats = 4 }, false},
		{"two attestors", func(p *types.Params) { p.Attestors = []string{a1, a2} }, true},
		{"duplicate attestor", func(p *types.Params) { p.Attestors = []string{a1, a1} }, false},
		{"uppercase duplicate", func(p *types.Params) { p.Attestors = []string{a1, strings.ToUpper(a1)} }, false},
		{"wrong prefix", func(p *types.Params) {
			p.Attestors = []string{sdk.MustBech32ifyAddressBytes("cosmos", bytes.Repeat([]byte{1}, 20))}
		}, false},
		{"ten attestors", func(p *types.Params) { p.Attestors = ten }, true},
		{"eleven attestors", func(p *types.Params) { p.Attestors = eleven }, false},
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
