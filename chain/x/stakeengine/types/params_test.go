package types_test

import (
	"strings"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

const scn = int64(100_000_000)

func saiconAddr(b byte) string {
	return sdk.MustBech32ifyAddressBytes(types.AccountPrefix, []byte{b, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
}

// The entry floor is a published protocol parameter (white paper, lite paper,
// site). Every other test compares against the constant symbolically and would
// stay green through a silent edit.
func TestEntryFloorIsTwoThousandScn(t *testing.T) {
	require.Equal(t, int64(2_000)*scn, types.EntryFloorNscn)
	require.Equal(t, math.NewInt(2_000*scn), types.EntryFloor())
}

func TestDefaultParamsAreTheSpecValues(t *testing.T) {
	p := types.DefaultParams()
	require.NoError(t, p.Validate())
	require.Equal(t, "0.080000000000000000", p.MaxSupplyFraction.String())
	require.Equal(t, "0.600000000000000000", p.PublicCirculatingFraction.String())
	require.Equal(t, "0.020000000000000000", p.ValidatorCapFraction.String())
	require.True(t, p.ValidatorCapFloor.IsZero(), "the code default is the strict 2% rule")
	require.Equal(t, uint64(30), p.EwmaWindowEpochs)
	require.Equal(t, math.NewInt(100*scn), p.MinQueueEntry)
	require.Equal(t, uint64(10_000), p.MaxQueueEntries)
	require.Equal(t, uint64(50), p.MaxFillsPerBlock)
	require.Empty(t, p.ExcludedAddresses)
}

func TestParamsBounds(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*types.Params)
	}{
		{"max_supply_fraction below 0.05", func(p *types.Params) { p.MaxSupplyFraction = math.LegacyNewDecWithPrec(4, 2) }},
		{"max_supply_fraction above 0.10", func(p *types.Params) { p.MaxSupplyFraction = math.LegacyNewDecWithPrec(11, 2) }},
		{"public_circulating_fraction below 0.30", func(p *types.Params) { p.PublicCirculatingFraction = math.LegacyNewDecWithPrec(29, 2) }},
		{"public_circulating_fraction above 0.80", func(p *types.Params) { p.PublicCirculatingFraction = math.LegacyNewDecWithPrec(81, 2) }},
		{"validator_cap_fraction below 0.01", func(p *types.Params) { p.ValidatorCapFraction = math.LegacyNewDecWithPrec(9, 3) }},
		{"validator_cap_fraction above 0.05", func(p *types.Params) { p.ValidatorCapFraction = math.LegacyNewDecWithPrec(6, 2) }},
		{"validator_cap_floor negative", func(p *types.Params) { p.ValidatorCapFloor = math.NewInt(-1) }},
		{"validator_cap_floor above 2.1M SCN", func(p *types.Params) { p.ValidatorCapFloor = math.NewInt(2_100_000*scn + 1) }},
		{"ewma window 0", func(p *types.Params) { p.EwmaWindowEpochs = 0 }},
		{"ewma window above 365", func(p *types.Params) { p.EwmaWindowEpochs = 366 }},
		{"min queue entry below 1 SCN", func(p *types.Params) { p.MinQueueEntry = math.NewInt(scn - 1) }},
		{"max queue entries 0", func(p *types.Params) { p.MaxQueueEntries = 0 }},
		{"max queue entries above 100,000", func(p *types.Params) { p.MaxQueueEntries = 100_001 }},
		{"max fills 0", func(p *types.Params) { p.MaxFillsPerBlock = 0 }},
		{"max fills above 500", func(p *types.Params) { p.MaxFillsPerBlock = 501 }},
		{"excluded address with the wrong prefix", func(p *types.Params) {
			p.ExcludedAddresses = []string{sdk.MustBech32ifyAddressBytes("cosmos", make([]byte, 20))}
		}},
		{"duplicate excluded address", func(p *types.Params) { p.ExcludedAddresses = []string{saiconAddr(1), saiconAddr(1)} }},
		{"duplicate excluded address in another case", func(p *types.Params) {
			p.ExcludedAddresses = []string{saiconAddr(1), strings.ToUpper(saiconAddr(1))}
		}},
		{"unset decimal", func(p *types.Params) { p.MaxSupplyFraction = math.LegacyDec{} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := types.DefaultParams()
			c.mut(&p)
			require.Error(t, p.Validate())
		})
	}

	t.Run("bounds are inclusive", func(t *testing.T) {
		p := types.DefaultParams()
		p.MaxSupplyFraction = math.LegacyNewDecWithPrec(10, 2)
		p.PublicCirculatingFraction = math.LegacyNewDecWithPrec(30, 2)
		p.ValidatorCapFraction = math.LegacyNewDecWithPrec(5, 2)
		p.ValidatorCapFloor = math.NewInt(2_100_000 * scn)
		p.EwmaWindowEpochs = 365
		p.MaxQueueEntries = 100_000
		p.MaxFillsPerBlock = 500
		p.ExcludedAddresses = []string{saiconAddr(1), saiconAddr(2)}
		require.NoError(t, p.Validate())
	})

	t.Run("more than 100 excluded addresses", func(t *testing.T) {
		p := types.DefaultParams()
		for i := 0; i < 101; i++ {
			p.ExcludedAddresses = append(p.ExcludedAddresses, saiconAddr(byte(i)))
		}
		require.Error(t, p.Validate())
	})
}

// A module account that is already subtracted from public circulating supply
// must not also be an excluded address: it would be subtracted twice.
func TestParamsRejectAnExcludedModuleAccountAlreadySubtracted(t *testing.T) {
	for _, name := range types.NonCirculatingModules {
		t.Run(name, func(t *testing.T) {
			p := types.DefaultParams()
			p.ExcludedAddresses = []string{sdk.MustBech32ifyAddressBytes(types.AccountPrefix, authtypes.NewModuleAddress(name))}
			require.ErrorContains(t, p.Validate(), "already subtracted")
		})
	}

	t.Run("a publicly owned module account is not caught", func(t *testing.T) {
		p := types.DefaultParams()
		p.ExcludedAddresses = []string{sdk.MustBech32ifyAddressBytes(types.AccountPrefix, authtypes.NewModuleAddress("bonded_tokens_pool"))}
		require.NoError(t, p.Validate())
	})
}

func TestS7DefaultsAreTheRegisterValues(t *testing.T) {
	p := types.DefaultParams()
	require.Equal(t, uint32(3), p.MaxValidatorsPerDelegator)
	require.Equal(t, math.NewInt(100*100_000_000), p.MinDelegation)
	require.Equal(t, math.NewInt(100*100_000_000), p.MinQueueEntry)
	require.NoError(t, p.Validate())
}

func TestS7ParamBounds(t *testing.T) {
	scn := int64(100_000_000)
	for _, tc := range []struct {
		name string
		mut  func(*types.Params)
		ok   bool
	}{
		{"slots 1", func(p *types.Params) { p.MaxValidatorsPerDelegator = 1 }, false},
		{"slots 2", func(p *types.Params) { p.MaxValidatorsPerDelegator = 2 }, true},
		{"slots 5", func(p *types.Params) { p.MaxValidatorsPerDelegator = 5 }, true},
		{"slots 6", func(p *types.Params) { p.MaxValidatorsPerDelegator = 6 }, false},
		{"min nil", func(p *types.Params) { p.MinDelegation = math.Int{} }, false},
		{"min below 1 SCN", func(p *types.Params) { p.MinDelegation = math.NewInt(scn - 1) }, false},
		{"min 1 SCN", func(p *types.Params) { p.MinDelegation = math.NewInt(scn) }, true},
		{"min 10,000 SCN", func(p *types.Params) { p.MinDelegation = math.NewInt(10_000 * scn) }, true},
		{"min above 10,000 SCN", func(p *types.Params) { p.MinDelegation = math.NewInt(10_000*scn + 1) }, false},
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
