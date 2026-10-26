package keeper_test

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func TestGenesis(t *testing.T) {
	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
	}

	f := initFixture(t)
	err := f.keeper.InitGenesis(f.ctx, genesisState)
	require.NoError(t, err)
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.EqualExportedValues(t, genesisState.Params, got.Params)
}

// D2, the other half. x/validatorregistry's compliance_flag is only a MIRROR;
// ComplianceState here is the source of truth, and it was not in genesis either.
//
// Losing it across an export/import is worse than losing the mirror, because the
// reward gate reads a missing state as innocent
// (`compliant := !found || state.Compliant`): a validator sanctioned with C=0
// became reward-eligible again after a routine coordinated restart, and the whole
// evidence trail behind the sanction went with it.
func TestGenesis_ComplianceStatesSurviveTheRoundTrip(t *testing.T) {
	f := initFixture(t)

	flaggedAt := time.Date(2026, 6, 15, 8, 0, 0, 0, time.UTC)
	sanctioned := sdk.ValAddress([]byte("sanctioned-validator"))[:20]
	clean := sdk.ValAddress([]byte("clean-validator-addr"))[:20]

	require.NoError(t, f.keeper.ComplianceState.Set(f.ctx, sanctioned.Bytes(), types.ComplianceState{
		ValidatorAddress: sdk.AccAddress(sanctioned).String(),
		Compliant:        false,
		LastEvent:        types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION,
		LastEventTime:    flaggedAt,
		Reason:           "double-sign at height 4211",
	}))
	require.NoError(t, f.keeper.ComplianceState.Set(f.ctx, clean.Bytes(), types.ComplianceState{
		ValidatorAddress: sdk.AccAddress(clean).String(),
		Compliant:        true,
		LastEventTime:    flaggedAt,
	}))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, exported.ComplianceStates, 2)

	fresh := initFixture(t)
	require.NoError(t, fresh.keeper.InitGenesis(fresh.ctx, *exported))

	got, found, err := fresh.keeper.GetCompliance(fresh.ctx, sanctioned)
	require.NoError(t, err)
	require.True(t, found, "the sanction must survive the restart")
	require.False(t, got.Compliant, "a C=0 validator must not come back reward-eligible")
	require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION, got.LastEvent)
	require.Equal(t, "double-sign at height 4211", got.Reason, "the evidence trail must survive too")
	require.Equal(t, flaggedAt, got.LastEventTime.UTC())

	ok, found, err := fresh.keeper.GetCompliance(fresh.ctx, clean)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, ok.Compliant)
}
