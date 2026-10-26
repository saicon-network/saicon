package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestActivateOnBond_RegisteredBonding_BecomesActive(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "bond1", types.Status_STATUS_BONDING)

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	md, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.Status_STATUS_ACTIVE, md.Status)
}

func TestActivateOnBond_Unknown_AutoCreatesActive(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("unregistered-val-addr-20byte"))[:20]

	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	require.NoError(t, f.keeper.ActivateOnBond(sdkCtx, addr))

	md, found, err := f.keeper.GetValidatorMetadata(sdkCtx, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.Status_STATUS_ACTIVE, md.Status)
	require.True(t, md.ComplianceFlag, "auto-created validator must be compliant by default")
	require.Equal(t, types.DefaultLockupDurationSeconds, md.LockupDurationSeconds)
	require.False(t, md.BondingStart.IsZero(), "bonding_start must be set")

	// Collect all EventTypeStatusChanged events emitted during ActivateOnBond.
	// The auto-create path emits two: "none"→bonding (birth) and bonding→active (transition).
	var statusChangedEvents []sdk.Event
	for _, e := range sdkCtx.EventManager().Events() {
		if e.Type == types.EventTypeStatusChanged {
			statusChangedEvents = append(statusChangedEvents, e)
		}
	}
	require.GreaterOrEqual(t, len(statusChangedEvents), 2,
		"auto-create path must emit at least two %s events (birth + transition)", types.EventTypeStatusChanged)

	// Assert the birth event: from_status == "none".
	var birthFound bool
	for _, e := range statusChangedEvents {
		attrs := make(map[string]string, len(e.Attributes))
		for _, a := range e.Attributes {
			attrs[a.Key] = a.Value
		}
		if attrs[types.AttributeKeyFromStatus] == "none" {
			birthFound = true
			require.Equal(t, types.Status_STATUS_BONDING.HumanString(), attrs[types.AttributeKeyToStatus],
				"birth event to_status must be bonding")
		}
	}
	require.True(t, birthFound, "expected a %s event with from_status=none (genesis birth event)", types.EventTypeStatusChanged)
}

func TestActivateOnBond_AlreadyActive_Idempotent(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "act1", types.Status_STATUS_ACTIVE)

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))
	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr)) // twice

	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_ACTIVE, md.Status)
}

func TestActivateOnBond_Jailed_StaysJailed(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "jail1", types.Status_STATUS_JAILED)

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_JAILED, md.Status, "bond event must not reactivate a jailed validator")
}

func TestActivateOnBond_Quarantined_StaysQuarantined(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "quar1", types.Status_STATUS_QUARANTINED)

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_QUARANTINED, md.Status)
}

func TestActivateOnBond_Inactive_StaysInactive(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "inact1", types.Status_STATUS_INACTIVE)

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_INACTIVE, md.Status, "bond event must not reactivate an inactive validator")
}

// D1 (BLOCKER), inverted from the assertion it replaces.
//
// This test used to require that a bond event leaves an UNBONDING validator
// alone. That reading — "leaving the bonded set is irreversible" — was the
// blocker: an ordinary downtime jail routes a validator through
// AfterValidatorBeginUnbonding, so the registry says UNBONDING while nothing is
// wrong with the operator's intent, and re-bonding is exactly how the incident
// ends. Refusing to react there left the record UNBONDING permanently, with no
// score, no EP, no rewards and no governed path back.
//
// Reactivation is still not blind: the status is restored fail-closed from
// pre_unbonding_status (see the unbonding tests), so a sanction cannot be washed
// off by unbonding and re-bonding.
func TestActivateOnBond_Unbonding_ResumesService(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "unb1", types.Status_STATUS_UNBONDING)

	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	md.PreUnbondingStatus = types.Status_STATUS_ACTIVE
	md.ComplianceFlag = true
	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, md))

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	md, _, err = f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_ACTIVE, md.Status,
		"a re-bonded validator whose exit was aborted must return to service")
}

func TestHooks_AfterValidatorBonded_Activates(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "hook1", types.Status_STATUS_BONDING)

	// consensus address is unused by our hook; pass nil ConsAddress.
	err := f.keeper.Hooks().AfterValidatorBonded(f.ctx, nil, addr)
	require.NoError(t, err)

	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_ACTIVE, md.Status)
}
