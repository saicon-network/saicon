package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestTheGateHoldsAnUnattestedValidator(t *testing.T) {
	f := newFixture(t)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_BONDING
	err := f.k.MayActivate(f.ctx, val(1))
	require.ErrorIs(t, err, vrtypes.ErrActivationHeld)
	require.Contains(t, err.Error(), "not attested by an entity")
	has, err := f.k.Pending.Has(f.ctx, val(1))
	require.NoError(t, err)
	require.True(t, has)
	requireEvent(t, f.ctx, types.EventTypeActivationHeld, "validator", val(1).String())
}

func TestTheThirdSeatIsHeld(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2), val(3)))
	require.NoError(t, err)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_BONDING
	err = f.k.MayActivate(f.ctx, val(3))
	require.ErrorIs(t, err, vrtypes.ErrActivationHeld)
	require.Contains(t, err.Error(), "entity acme already holds 2 active seats; the limit is 2")
	// a quarantined member holds no seat
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_QUARANTINED
	require.NoError(t, f.k.MayActivate(f.ctx, val(3)))
	has, _ := f.k.Pending.Has(f.ctx, val(3))
	require.False(t, has, "an allowed activation leaves pending")
}

func TestAnActiveValidatorDoesNotCountItself(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2)))
	require.NoError(t, err)
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_ACTIVE
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_ACTIVE
	require.NoError(t, f.k.MayActivate(f.ctx, val(2)))
}

func TestAnAllowedActivationClearsPendingAndStaysQuiet(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1)))
	require.NoError(t, err)
	require.NoError(t, f.k.Pending.Set(f.ctx, val(1)))
	events := len(f.ctx.EventManager().Events())
	require.NoError(t, f.k.MayActivate(f.ctx, val(1)))
	has, err := f.k.Pending.Has(f.ctx, val(1))
	require.NoError(t, err)
	require.False(t, has)
	require.Len(t, f.ctx.EventManager().Events(), events, "allowing emits nothing")
}

// The verdict is computed read-only: a read error before it is neither a hold
// nor a write. The staking bond hook is not branch-cached, so a stray write on
// that path would persist.
func TestAGateErrorBeforeTheVerdictWritesAndEmitsNothing(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Attest(f.ctx, f.attestor, att("acme", val(1), val(2)))
	require.NoError(t, err)
	f.reg.status[val(2).String()] = vrtypes.Status_STATUS_ACTIVE
	before, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	events := len(f.ctx.EventManager().Events())

	f.reg.failStatus = true
	err = f.k.MayActivate(f.ctx, val(1))
	require.Error(t, err)
	require.NotErrorIs(t, err, vrtypes.ErrActivationHeld)

	after, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Len(t, f.ctx.EventManager().Events(), events)
}
