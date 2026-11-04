package keeper_test

import (
	"context"
	"errors"
	"testing"
	"time"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// stubGate is a scripted types.ActivationGate that records every address it is
// asked about.
type stubGate struct {
	err   error
	calls []string
}

func (g *stubGate) MayActivate(_ context.Context, v sdk.ValAddress) error {
	g.calls = append(g.calls, v.String())
	return g.err
}

var errHeld = errorsmod.Wrap(types.ErrActivationHeld, "not attested")

// gatedFixture returns a fresh fixture whose keeper asks gate before any
// transition to ACTIVE.
func gatedFixture(t *testing.T, gate *stubGate) *fixture {
	t.Helper()
	f := initFixture(t)
	f.keeper.SetActivationGate(gate)
	return f
}

func statusOf(t *testing.T, f *fixture, addr sdk.ValAddress) types.ValidatorMetadata {
	t.Helper()
	md, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)
	return md
}

func TestNoGateActivatesAsBefore(t *testing.T) {
	f := initFixture(t)
	require.False(t, f.keeper.HasActivationGate())
	addr := sdk.ValAddress([]byte("ungated-validator-addr-20b"))[:20]

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	require.Equal(t, types.Status_STATUS_ACTIVE, statusOf(t, f, addr).Status)
}

func TestHeldAutoCreateStaysBonding(t *testing.T) {
	gate := &stubGate{err: errHeld}
	f := gatedFixture(t, gate)
	blockTime := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	f.ctx = sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)
	addr := sdk.ValAddress([]byte("held-validator-addr-20byte"))[:20]

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	md := statusOf(t, f, addr)
	require.Equal(t, types.Status_STATUS_BONDING, md.Status)
	require.True(t, md.BondingStart.Equal(blockTime))
	require.Equal(t, types.DefaultLockupDurationSeconds, md.LockupDurationSeconds)
	require.Equal(t, []string{addr.String()}, gate.calls)
}

func TestHeldBondingStaysBonding(t *testing.T) {
	gate := &stubGate{err: errHeld}
	f := gatedFixture(t, gate)
	addr := seedValidator(t, f, "heldbond", types.Status_STATUS_BONDING)

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	require.Equal(t, types.Status_STATUS_BONDING, statusOf(t, f, addr).Status)
	require.Len(t, gate.calls, 1)
}

func TestHeldResumeStaysUnbondingWithItsPreStatus(t *testing.T) {
	gate := &stubGate{err: errHeld}
	f := gatedFixture(t, gate)
	addr := seedValidator(t, f, "heldresume", types.Status_STATUS_UNBONDING)
	completeAt := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	md := statusOf(t, f, addr)
	md.PreUnbondingStatus = types.Status_STATUS_ACTIVE
	md.ComplianceFlag = true
	md.UnbondingCompleteTime = completeAt
	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, md))

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	got := statusOf(t, f, addr)
	require.Equal(t, types.Status_STATUS_UNBONDING, got.Status)
	require.Equal(t, types.Status_STATUS_ACTIVE, got.PreUnbondingStatus)
	require.True(t, got.UnbondingCompleteTime.Equal(completeAt))
	require.Len(t, gate.calls, 1)
}

func TestResumeToQuarantinedIsNotGated(t *testing.T) {
	gate := &stubGate{err: errHeld}
	f := gatedFixture(t, gate)
	addr := seedValidator(t, f, "resumeq", types.Status_STATUS_UNBONDING)
	md := statusOf(t, f, addr)
	md.PreUnbondingStatus = types.Status_STATUS_QUARANTINED
	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, md))

	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	require.Equal(t, types.Status_STATUS_QUARANTINED, statusOf(t, f, addr).Status)
	require.Empty(t, gate.calls)
}

func TestChangeStatusReturnsTheRefusal(t *testing.T) {
	gate := &stubGate{err: errHeld}
	f := gatedFixture(t, gate)
	addr := seedValidator(t, f, "clearflag", types.Status_STATUS_QUARANTINED)

	err := f.keeper.ChangeStatus(f.ctx, addr, types.Status_STATUS_ACTIVE)

	require.True(t, errors.Is(err, types.ErrActivationHeld))
	require.Equal(t, types.Status_STATUS_QUARANTINED, statusOf(t, f, addr).Status)
}

func TestIllegalEdgeFailsBeforeTheGate(t *testing.T) {
	gate := &stubGate{err: errHeld}
	f := gatedFixture(t, gate)
	addr := seedValidator(t, f, "illegal", types.Status_STATUS_INACTIVE)

	err := f.keeper.ChangeStatus(f.ctx, addr, types.Status_STATUS_ACTIVE)

	require.True(t, errors.Is(err, types.ErrInvalidTransition))
	require.Empty(t, gate.calls)
}

func TestOtherTargetsAreNotGated(t *testing.T) {
	gate := &stubGate{err: errors.New("every call fails")}
	f := gatedFixture(t, gate)
	addr := seedValidator(t, f, "othertarget", types.Status_STATUS_ACTIVE)

	require.NoError(t, f.keeper.ChangeStatus(f.ctx, addr, types.Status_STATUS_QUARANTINED))

	require.Equal(t, types.Status_STATUS_QUARANTINED, statusOf(t, f, addr).Status)
	require.Empty(t, gate.calls)
}

// Review Focus 1: the staking hook propagates errors out of EndBlock, so no gate
// failure — not even one that is not a hold — may leave AfterValidatorBonded.
func TestAGateErrorNeverLeavesTheHook(t *testing.T) {
	gate := &stubGate{err: errors.New("store failure")}
	f := gatedFixture(t, gate)
	addr := seedValidator(t, f, "hookfail", types.Status_STATUS_BONDING)

	err := f.keeper.Hooks().AfterValidatorBonded(f.ctx, sdk.ConsAddress("cons-addr-hook-failure"), addr)

	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_BONDING, statusOf(t, f, addr).Status)
	require.Len(t, gate.calls, 1)
}

func TestTheGateIsSharedByKeeperCopies(t *testing.T) {
	f := initFixture(t)
	k2 := f.keeper // a value copy, as depinject hands out
	require.False(t, k2.HasActivationGate())

	f.keeper.SetActivationGate(&stubGate{})

	require.True(t, k2.HasActivationGate())
	require.PanicsWithValue(t, "validatorregistry: activation gate already set", func() {
		k2.SetActivationGate(&stubGate{})
	})
}

func TestAHeldBondingValidatorLeavingTheBondedSetIsANoOp(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "heldleaves", types.Status_STATUS_BONDING)
	before := statusOf(t, f, addr)

	require.NoError(t, f.keeper.BeginUnbonding(f.ctx, addr))

	require.Equal(t, before, statusOf(t, f, addr))
}

// Spec §7.1: a resume the gate holds leaves pre_unbonding_status untouched, so a
// later promotion resumes correctly. A held validator can leave the bonded set
// again while it is UNBONDING; that second begin-unbonding must not overwrite
// the recorded ACTIVE with UNBONDING, or the resume lands QUARANTINED.
func TestAHeldResumeThatLeavesAgainKeepsItsPreStatus(t *testing.T) {
	gate := &stubGate{}
	f := v5Fixture(t, stubStakingKeeper{validatorUnbondingTime: v5Completion})
	f.keeper.SetActivationGate(gate)
	addr := seedValidator(t, f, "heldtwice", types.Status_STATUS_ACTIVE)

	// Jailed for downtime: it leaves the bonded set.
	require.NoError(t, f.keeper.BeginUnbonding(f.ctx, addr))
	require.Equal(t, types.Status_STATUS_ACTIVE, statusOf(t, f, addr).PreUnbondingStatus)

	// Its seat is taken meanwhile: the re-bond is held.
	gate.err = errHeld
	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))
	require.Equal(t, types.Status_STATUS_UNBONDING, statusOf(t, f, addr).Status)

	// It drops out of the bonded set again while held.
	require.NoError(t, f.keeper.BeginUnbonding(f.ctx, addr))
	md := statusOf(t, f, addr)
	require.Equal(t, types.Status_STATUS_UNBONDING, md.Status)
	require.Equal(t, types.Status_STATUS_ACTIVE, md.PreUnbondingStatus, "the second exit keeps what the first recorded")

	// The seat frees and it re-bonds: it resumes ACTIVE, not QUARANTINED.
	gate.err = nil
	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))
	require.Equal(t, types.Status_STATUS_ACTIVE, statusOf(t, f, addr).Status)
}
