package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// seedValidator stores a metadata record in the given status and returns the addr.
func seedValidator(t *testing.T, f *fixture, tag string, status types.Status) sdk.ValAddress {
	t.Helper()
	raw := []byte("validator-" + tag + "-padding-bytes!!")
	addr := sdk.ValAddress(raw)[:20]
	addrStr, err := f.addressCodec.BytesToString(addr)
	require.NoError(t, err)
	md := sampleValidatorMetadata(addrStr)
	md.Status = status
	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, md))
	return addr
}

func TestChangeStatus_AllAllowedTransitions(t *testing.T) {
	transitions := []struct {
		from types.Status
		to   types.Status
	}{
		{types.Status_STATUS_BONDING, types.Status_STATUS_ACTIVE},
		{types.Status_STATUS_BONDING, types.Status_STATUS_INACTIVE},
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_QUARANTINED},
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_JAILED},
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_UNBONDING},
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_ACTIVE},
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_JAILED},
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_UNBONDING},
		{types.Status_STATUS_JAILED, types.Status_STATUS_ACTIVE},
		{types.Status_STATUS_JAILED, types.Status_STATUS_UNBONDING},
		{types.Status_STATUS_UNBONDING, types.Status_STATUS_INACTIVE},
	}

	for i, tr := range transitions {
		f := initFixture(t)
		addr := seedValidator(t, f, string(rune('a'+i)), tr.from)

		require.NoError(t, f.keeper.ChangeStatus(f.ctx, addr, tr.to),
			"%s -> %s should succeed", tr.from.HumanString(), tr.to.HumanString())

		got, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, tr.to, got.Status, "status should be persisted as new value")
	}
}

func TestChangeStatus_ForbiddenTransitions(t *testing.T) {
	forbidden := []struct {
		from types.Status
		to   types.Status
	}{
		{types.Status_STATUS_INACTIVE, types.Status_STATUS_BONDING},
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_QUARANTINED}, // self-loop
		// UNBONDING -> ACTIVE/QUARANTINED became legal with the aborted-exit
		// recovery (D1); jailing from UNBONDING did not — x/compliance jails a live
		// validator, never one already leaving the set.
		{types.Status_STATUS_UNBONDING, types.Status_STATUS_JAILED},
		{types.Status_STATUS_BONDING, types.Status_STATUS_QUARANTINED},
		{types.Status_STATUS_BONDING, types.Status_STATUS_JAILED},
	}

	for i, tr := range forbidden {
		f := initFixture(t)
		addr := seedValidator(t, f, "f"+string(rune('a'+i)), tr.from)

		err := f.keeper.ChangeStatus(f.ctx, addr, tr.to)
		require.Error(t, err, "%s -> %s must be rejected", tr.from.HumanString(), tr.to.HumanString())
		require.ErrorIs(t, err, types.ErrInvalidTransition)

		// status must be unchanged
		got, found, gerr := f.keeper.GetValidatorMetadata(f.ctx, addr)
		require.NoError(t, gerr)
		require.True(t, found)
		require.Equal(t, tr.from, got.Status, "status must remain unchanged after rejected transition")
	}
}

func TestChangeStatus_NotFound(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("ghost-validator-20b!"))[:20]

	err := f.keeper.ChangeStatus(f.ctx, addr, types.Status_STATUS_ACTIVE)
	require.Error(t, err)
	require.ErrorIs(t, err, types.ErrInvalidTransition)
}

func TestChangeStatus_UpdatesHeight(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "height", types.Status_STATUS_BONDING)

	sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(4242)
	require.NoError(t, f.keeper.ChangeStatus(sdkCtx, addr, types.Status_STATUS_ACTIVE))

	got, found, err := f.keeper.GetValidatorMetadata(sdkCtx, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(4242), got.LastStatusChangeHeight)
}

func TestChangeStatus_EmitsEvent(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "event", types.Status_STATUS_ACTIVE)

	sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(77)
	require.NoError(t, f.keeper.ChangeStatus(sdkCtx, addr, types.Status_STATUS_UNBONDING))

	events := sdkCtx.EventManager().Events()
	var found bool
	for _, e := range events {
		if e.Type != types.EventTypeStatusChanged {
			continue
		}
		found = true
		attrs := map[string]string{}
		for _, a := range e.Attributes {
			attrs[a.Key] = a.Value
		}
		require.Equal(t, addr.String(), attrs[types.AttributeKeyValidator])
		require.Equal(t, "active", attrs[types.AttributeKeyFromStatus])
		require.Equal(t, "unbonding", attrs[types.AttributeKeyToStatus])
		require.Equal(t, "77", attrs[types.AttributeKeyHeight])
	}
	require.True(t, found, "expected a %s event", types.EventTypeStatusChanged)
}
