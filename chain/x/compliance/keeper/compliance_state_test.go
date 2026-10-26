package keeper_test

import (
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func TestSetCompliance_PersistsAndMirrors(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("compliance-val-20by!"))[:20]

	err := f.keeper.SetCompliance(f.ctx, addr, false, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG, "admin flag")
	require.NoError(t, err)

	// source-of-truth state persisted
	state, found, err := f.keeper.GetCompliance(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, state.Compliant)
	require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG, state.LastEvent)
	require.Equal(t, "admin flag", state.Reason)

	// C flag mirrored into the registry (V-4.1)
	require.Contains(t, f.vr.flags, addr.String())
	require.False(t, f.vr.flags[addr.String()])
}

func TestSetCompliance_RestoreFlipsFlag(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("compliance-restore!!"))[:20]

	require.NoError(t, f.keeper.SetCompliance(f.ctx, addr, false, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_KYC_EXPIRED, "kyc expired"))
	require.False(t, f.vr.flags[addr.String()])

	require.NoError(t, f.keeper.SetCompliance(f.ctx, addr, true, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_RESTORED, "re-attested"))
	require.True(t, f.vr.flags[addr.String()])

	state, _, _ := f.keeper.GetCompliance(f.ctx, addr)
	require.True(t, state.Compliant)
	require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_RESTORED, state.LastEvent)
}

func TestSetCompliance_MirrorErrorPropagates(t *testing.T) {
	f := initFixture(t)
	f.vr.err = errors.New("validator not found")
	addr := sdk.ValAddress([]byte("compliance-noreg-20b"))[:20]

	err := f.keeper.SetCompliance(f.ctx, addr, false, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG, "x")
	require.Error(t, err)
}

func TestGetCompliance_NotFound(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("compliance-absent20b"))[:20]

	_, found, err := f.keeper.GetCompliance(f.ctx, addr)
	require.NoError(t, err)
	require.False(t, found)
}

// TestSetCompliance_EmitsFlaggedEvent: SetCompliance must emit exactly one
// types.EventTypeComplianceFlagged event when a validator flips to
// non-compliant, with attributes identifying the validator, event, and reason.
func TestSetCompliance_EmitsFlaggedEvent(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("compliance-flag-20by"))[:20]

	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	require.NoError(t, f.keeper.SetCompliance(sdkCtx, addr, false, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG, "demo flag"))

	events := sdkCtx.EventManager().Events()
	var matches []sdk.Event
	for _, e := range events {
		if e.Type == types.EventTypeComplianceFlagged {
			matches = append(matches, e)
		}
	}
	require.Len(t, matches, 1, "expected exactly one %s event", types.EventTypeComplianceFlagged)

	attrs := map[string]string{}
	for _, a := range matches[0].Attributes {
		attrs[a.Key] = a.Value
	}
	require.Equal(t, addr.String(), attrs[types.AttributeKeyValidator])
	require.Equal(t, "COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG", attrs[types.AttributeKeyEvent])
	require.Equal(t, "demo flag", attrs[types.AttributeKeyReason])
}

// TestSetCompliance_EmitsClearedEvent: SetCompliance must emit exactly one
// types.EventTypeComplianceCleared event when a validator is restored to
// compliant, with attributes identifying the validator, event, and reason.
func TestSetCompliance_EmitsClearedEvent(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("compliance-clear-20b"))[:20]

	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	require.NoError(t, f.keeper.SetCompliance(sdkCtx, addr, true, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_RESTORED, "restored ok"))

	events := sdkCtx.EventManager().Events()
	var matches []sdk.Event
	for _, e := range events {
		if e.Type == types.EventTypeComplianceCleared {
			matches = append(matches, e)
		}
	}
	require.Len(t, matches, 1, "expected exactly one %s event", types.EventTypeComplianceCleared)

	attrs := map[string]string{}
	for _, a := range matches[0].Attributes {
		attrs[a.Key] = a.Value
	}
	require.Equal(t, addr.String(), attrs[types.AttributeKeyValidator])
	require.Equal(t, "COMPLIANCE_EVENT_TYPE_RESTORED", attrs[types.AttributeKeyEvent])
	require.Equal(t, "restored ok", attrs[types.AttributeKeyReason])
}
