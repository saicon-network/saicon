package keeper_test

import (
	"errors"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// hookTestValAddr returns a deterministic 20-byte validator operator address.
func hookTestValAddr(t *testing.T) sdk.ValAddress {
	t.Helper()
	return sdk.ValAddress([]byte("hook-test-validator!"))
}

// TestHooks_DoubleSignJailsRegisteredValidator: a slash at the double-sign
// fraction (0.05 >= 0.025) on a registered, active validator routes through
// SetCompliance as a CRITICAL_VIOLATION — flagging it non-compliant and
// jailing it.
func TestHooks_DoubleSignJailsRegisteredValidator(t *testing.T) {
	f := initFixture(t)
	addr := hookTestValAddr(t)
	f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_ACTIVE

	err := f.keeper.Hooks().BeforeValidatorSlashed(f.ctx, addr, math.LegacyNewDecWithPrec(5, 2))
	require.NoError(t, err)

	require.Equal(t, false, f.vr.flags[addr.String()])
	require.Equal(t, vrtypes.Status_STATUS_JAILED, f.vr.statuses[addr.String()])

	state, found, err := f.keeper.GetCompliance(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION, state.LastEvent)
}

// Under the Register's slashing policy (ADR-0010) ordinary downtime carries a
// ZERO stake slash, and x/staking skips this hook entirely when the burn is zero
// (cosmos-sdk@v0.53.6/x/staking/keeper/slash.go:148-151). So the only infraction
// that can reach the hook with a positive burn is a double-sign, and the old
// 0.025 midpoint — chosen as the middle of the SDK's 0.01…0.05 band — has an
// empty band to sit in.
//
// This test previously asserted the OPPOSITE: that a 0.01 slash was "downtime"
// and had to be ignored. Keeping that behaviour under the new params would MISS
// real double-signs, because the hook receives the *effective* burn fraction
// (tokensToBurn / validator.Tokens), which falls below the configured 0.10
// whenever bonded tokens shrank between infraction and slash.
func TestHooks_AnyPositiveBurnIsADoubleSign(t *testing.T) {
	for _, fraction := range []string{
		"0.100000000000000000", // the configured double-sign fraction
		"0.010000000000000000", // an effective burn far below it — still a double-sign
		"0.000000000000000001", // the smallest positive burn the SDK can deliver
	} {
		t.Run(fraction, func(t *testing.T) {
			f := initFixture(t)
			addr := hookTestValAddr(t)
			f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_ACTIVE

			dec, err := math.LegacyNewDecFromStr(fraction)
			require.NoError(t, err)
			require.NoError(t, f.keeper.Hooks().BeforeValidatorSlashed(f.ctx, addr, dec))

			require.Equal(t, vrtypes.Status_STATUS_JAILED, f.vr.statuses[addr.String()],
				"a positive burn can only be a double-sign once downtime slashing is zero")
			state, found, gerr := f.keeper.GetCompliance(f.ctx, addr)
			require.NoError(t, gerr)
			require.True(t, found)
			require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION, state.LastEvent)
		})
	}
}

// A zero fraction must still be a no-op. The SDK does not deliver one today, but
// the hook is public and must not jail a validator for a burn of nothing.
func TestHooks_ZeroFractionIsIgnored(t *testing.T) {
	f := initFixture(t)
	addr := hookTestValAddr(t)
	f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_ACTIVE

	require.NoError(t, f.keeper.Hooks().BeforeValidatorSlashed(f.ctx, addr, math.LegacyZeroDec()))

	_, found, err := f.keeper.GetCompliance(f.ctx, addr)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, f.vr.statuses[addr.String()])
}

// TestHooks_UnregisteredValidatorNoError: a slash on a validator not present in
// the SAiCoN registry must be a no-op — the hook short-circuits when
// GetValidatorStatus reports not-found, and must never error (it runs inside
// slashing and would otherwise halt the chain).
func TestHooks_UnregisteredValidatorNoError(t *testing.T) {
	f := initFixture(t)
	addr := hookTestValAddr(t)
	// Intentionally do NOT seed f.vr.statuses.

	err := f.keeper.Hooks().BeforeValidatorSlashed(f.ctx, addr, math.LegacyNewDecWithPrec(5, 2))
	require.NoError(t, err)

	_, found, err := f.keeper.GetCompliance(f.ctx, addr)
	require.NoError(t, err)
	require.False(t, found)
}

// The boundary that matters now is zero, not 0.025: the smallest positive burn
// flags, and nothing else does. (This replaces a test that locked the old 0.025
// midpoint, which no longer has a band to sit in.)
func TestHooks_BoundaryIsZeroNotAMidpoint(t *testing.T) {
	f := initFixture(t)
	addr := hookTestValAddr(t)
	f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_ACTIVE

	smallest := math.LegacySmallestDec()
	require.NoError(t, f.keeper.Hooks().BeforeValidatorSlashed(f.ctx, addr, smallest))
	require.Equal(t, vrtypes.Status_STATUS_JAILED, f.vr.statuses[addr.String()])
}

// ADR-0010 §4 — the revised V-8 end to end from the punishment side.
//
// A double-sign must zero the in-flight epoch's participation score and must
// NOT touch EP. The two halves are asserted together because the value of the
// rule is precisely that one happens and the other does not.
func TestHooks_DoubleSignZeroesReputationButNotEP(t *testing.T) {
	f := initFixture(t)
	addr := hookTestValAddr(t)
	f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_ACTIVE

	require.NoError(t, f.keeper.Hooks().BeforeValidatorSlashed(f.ctx, addr, math.LegacyNewDecWithPrec(10, 2)))

	require.True(t, f.participation.zeroed[addr.String()],
		"a double-sign must zero the in-flight reputation (ADR-0010 §4)")
	require.False(t, f.participation.epTouched,
		"punishment must never reach EP — invariant V-8 holds by construction, "+
			"and the ParticipationKeeper interface does not even expose SetEP")
}

// A punished validator whose epoch cannot be determined must still be jailed:
// the compliance decision does not depend on the participation layer answering.
func TestHooks_ReputationFailureDoesNotBlockTheJail(t *testing.T) {
	f := initFixture(t)
	addr := hookTestValAddr(t)
	f.vr.statuses[addr.String()] = vrtypes.Status_STATUS_ACTIVE
	f.participation.zeroErr = errors.New("participation store unavailable")

	// The hook runs inside slashing: it must not error out and halt the chain.
	require.NoError(t, f.keeper.Hooks().BeforeValidatorSlashed(f.ctx, addr, math.LegacyNewDecWithPrec(10, 2)))

	require.Equal(t, vrtypes.Status_STATUS_JAILED, f.vr.statuses[addr.String()],
		"the jail is the load-bearing sanction and must land regardless")
}
