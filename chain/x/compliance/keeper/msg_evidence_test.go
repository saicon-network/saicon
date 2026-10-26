package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// authorityStr returns the module authority as a bech32 string.
func authorityStr(t *testing.T, f *fixture) string {
	t.Helper()
	s, err := f.addressCodec.BytesToString(f.keeper.GetAuthority())
	require.NoError(t, err)
	return s
}

func TestSubmitEvidence_QuarantinesActiveValidator(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	addr := sdk.ValAddress([]byte("ev-active-val-20byte"))[:20]
	f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_ACTIVE

	_, err := ms.SubmitEvidence(f.ctx, &types.MsgSubmitEvidence{
		Authority:        authorityStr(t, f),
		ValidatorAddress: addr.String(),
		Event:            types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG,
		Reason:           "kyc lapse",
	})
	require.NoError(t, err)

	// C flag mirrored false
	require.False(t, f.vr.flags[addr.String()])
	// status moved active → quarantined
	require.Equal(t, vrtypes.Status_STATUS_QUARANTINED, f.vr.statuses[addr.String()])
	// source-of-truth state persisted
	st, found, _ := f.keeper.GetCompliance(f.ctx, addr)
	require.True(t, found)
	require.False(t, st.Compliant)
	require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG, st.LastEvent)
}

func TestSubmitEvidence_CriticalJails(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	addr := sdk.ValAddress([]byte("ev-critical-20bytes!"))[:20]
	f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_ACTIVE

	_, err := ms.SubmitEvidence(f.ctx, &types.MsgSubmitEvidence{
		Authority:        authorityStr(t, f),
		ValidatorAddress: addr.String(),
		Event:            types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION,
		Reason:           "double sign",
	})
	require.NoError(t, err)
	require.Equal(t, vrtypes.Status_STATUS_JAILED, f.vr.statuses[addr.String()])
}

func TestSubmitEvidence_BondingStaysBonding(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	addr := sdk.ValAddress([]byte("ev-bonding-20bytes!!"))[:20]
	f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_BONDING

	_, err := ms.SubmitEvidence(f.ctx, &types.MsgSubmitEvidence{
		Authority:        authorityStr(t, f),
		ValidatorAddress: addr.String(),
		Event:            types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_IDENTITY_FAILED,
		Reason:           "no id",
	})
	require.NoError(t, err)
	// C flag set, but status unchanged (bonding→quarantined forbidden by V-1)
	require.False(t, f.vr.flags[addr.String()])
	require.Equal(t, vrtypes.Status_STATUS_BONDING, f.vr.statuses[addr.String()])
}

func TestClearFlag_RestoresQuarantinedToActive(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	auth := authorityStr(t, f)

	addr := sdk.ValAddress([]byte("ev-restore-20bytes!!"))[:20]
	f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_ACTIVE

	// flag → quarantined
	_, err := ms.SubmitEvidence(f.ctx, &types.MsgSubmitEvidence{
		Authority: auth, ValidatorAddress: addr.String(),
		Event:  types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_KYC_EXPIRED,
		Reason: "expired",
	})
	require.NoError(t, err)
	require.Equal(t, vrtypes.Status_STATUS_QUARANTINED, f.vr.statuses[addr.String()])

	// clear → active
	_, err = ms.ClearFlag(f.ctx, &types.MsgClearFlag{
		Authority: auth, ValidatorAddress: addr.String(), Reason: "re-attested",
	})
	require.NoError(t, err)
	require.True(t, f.vr.flags[addr.String()])
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, f.vr.statuses[addr.String()])
}

// TestClearFlag_RejectsJailedValidator locks the boundary between the two
// restore paths. MsgUnjail is the only way out of JAILED — it is what enforces
// the cooldown and the tombstone gate — so MsgClearFlag must not double as a
// way around them.
//
// Regression guard for the governance bypass: both messages share the same
// gov-authority gate, and the V-1 table permits JAILED -> ACTIVE generically,
// so a proposal calling ClearFlag on a JAILED validator used to restore C=1 and
// flip JAILED -> ACTIVE with neither gate evaluated — reinstating even a
// tombstoned double-signer that MsgUnjail bars permanently.
func TestClearFlag_RejectsJailedValidator(t *testing.T) {
	addr := sdk.ValAddress([]byte("cf-jailed-20bytes!!!"))[:20]

	setup := func(t *testing.T, jailHeight, nowHeight, cooldown int64, tombstoned bool) (*fixture, types.MsgServer) {
		f := initFixture(t)
		f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_JAILED
		f.vr.heights[addr.String()] = jailHeight
		f.slashing.tombstoned = tombstoned
		require.NoError(t, f.keeper.Params.Set(f.ctx, types.NewParams(cooldown)))
		f.ctx = sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(nowHeight)
		return f, keeper.NewMsgServerImpl(f.keeper)
	}

	clear := func(f *fixture, ms types.MsgServer) error {
		_, err := ms.ClearFlag(f.ctx, &types.MsgClearFlag{
			Authority:        authorityStr(t, f),
			ValidatorAddress: addr.String(),
			Reason:           "bypass attempt",
		})
		return err
	}

	// A tombstoned (double-signing) validator is permanently barred from
	// returning — MsgUnjail refuses it, so ClearFlag must refuse it too.
	t.Run("tombstoned", func(t *testing.T) {
		f, ms := setup(t, 100, 150, 50, true)
		require.ErrorIs(t, clear(f, ms), types.ErrJailedRequiresUnjail)
		require.Equal(t, vrtypes.Status_STATUS_JAILED, f.vr.statuses[addr.String()])
		require.False(t, f.vr.flags[addr.String()])
	})

	// Cooldown still running: MsgUnjail returns ErrUnjailCooldown here, so
	// ClearFlag must not act as an early exit.
	t.Run("cooldown_not_elapsed", func(t *testing.T) {
		f, ms := setup(t, 100, 149, 50, false)
		require.ErrorIs(t, clear(f, ms), types.ErrJailedRequiresUnjail)
		require.Equal(t, vrtypes.Status_STATUS_JAILED, f.vr.statuses[addr.String()])
		require.False(t, f.vr.flags[addr.String()])
	})

	// Even a fully-eligible jailed validator must go through MsgUnjail, keeping
	// it the single audited entry point (it is what emits validator_unjailed).
	t.Run("cooldown_elapsed_not_tombstoned", func(t *testing.T) {
		f, ms := setup(t, 100, 150, 50, false)
		require.ErrorIs(t, clear(f, ms), types.ErrJailedRequiresUnjail)
		require.Equal(t, vrtypes.Status_STATUS_JAILED, f.vr.statuses[addr.String()])
		require.False(t, f.vr.flags[addr.String()])
	})
}

func TestSubmitEvidence_RejectsNonAuthority(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	addr := sdk.ValAddress([]byte("ev-noauth-20bytes!!!"))[:20]
	notAuth, _ := f.addressCodec.BytesToString(sdk.AccAddress([]byte("random-acct-20bytess"))[:20])

	_, err := ms.SubmitEvidence(f.ctx, &types.MsgSubmitEvidence{
		Authority: notAuth, ValidatorAddress: addr.String(),
		Event:  types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG,
		Reason: "x",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, types.ErrInvalidSigner)
}

func TestSubmitEvidence_RejectsRestoredEvent(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	addr := sdk.ValAddress([]byte("ev-badevent-20byte!!"))[:20]
	_, err := ms.SubmitEvidence(f.ctx, &types.MsgSubmitEvidence{
		Authority: authorityStr(t, f), ValidatorAddress: addr.String(),
		Event:  types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_RESTORED,
		Reason: "x",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, types.ErrInvalidEvent)
}
