package keeper_test

import (
	"context"
	"testing"
	"time"

	"cosmossdk.io/core/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// testLockHeight is the block height at which every seeded locked validator's
// lock-up begins (md.BondingStartHeight). Exit entries created at or after it
// were created WHILE locked (a breach); entries below it predate the lock.
const testLockHeight int64 = 100

// stubStakingKeeper is a configurable test double implementing types.StakingKeeper.
// The V-3 invariant exercises GetUnbondingDelegation and
// GetRedelegationsFromSrcValidator; ConsensusAddressCodec / ValidatorByConsAddr
// return zero values.
type stubStakingKeeper struct {
	// unbondingHeights, when non-nil, makes GetUnbondingDelegation return a UBD
	// with one entry per CreationHeight; nil => not found (no self-unbonding).
	unbondingHeights []int64
	// redelegations is returned verbatim from GetRedelegationsFromSrcValidator.
	redelegations []stakingtypes.Redelegation
	// V-5: validatorUnbondingTime is the staking record's UnbondingTime (the instant
	// the cooldown ends). Zero + validatorMissing=false means the record exists but
	// carries no unbonding time.
	validatorUnbondingTime time.Time
	// validatorMissing makes GetValidator fail, exercising the params fallback.
	validatorMissing bool
	// paramsUnbondingTime is the fallback cooldown length from staking params.
	paramsUnbondingTime time.Duration
}

func (s stubStakingKeeper) ConsensusAddressCodec() address.Codec { return nil }

func (s stubStakingKeeper) ValidatorByConsAddr(context.Context, sdk.ConsAddress) (stakingtypes.ValidatorI, error) {
	return nil, nil
}

func (s stubStakingKeeper) GetUnbondingDelegation(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) (stakingtypes.UnbondingDelegation, error) {
	if s.unbondingHeights == nil {
		return stakingtypes.UnbondingDelegation{}, stakingtypes.ErrNoUnbondingDelegation
	}
	ubd := stakingtypes.UnbondingDelegation{}
	for _, h := range s.unbondingHeights {
		ubd.Entries = append(ubd.Entries, stakingtypes.UnbondingDelegationEntry{CreationHeight: h})
	}
	return ubd, nil
}

func (s stubStakingKeeper) GetValidator(_ context.Context, _ sdk.ValAddress) (stakingtypes.Validator, error) {
	if s.validatorMissing {
		return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
	}
	return stakingtypes.Validator{UnbondingTime: s.validatorUnbondingTime}, nil
}

func (s stubStakingKeeper) UnbondingTime(_ context.Context) (time.Duration, error) {
	return s.paramsUnbondingTime, nil
}

func (s stubStakingKeeper) GetRedelegationsFromSrcValidator(_ context.Context, _ sdk.ValAddress) ([]stakingtypes.Redelegation, error) {
	return s.redelegations, nil
}

// selfRedelegation builds a Redelegation sourced from valAddr whose delegator is
// the validator's own account, with one entry at the given CreationHeight.
func selfRedelegation(valAddr sdk.ValAddress, creationHeight int64) stakingtypes.Redelegation {
	return stakingtypes.Redelegation{
		DelegatorAddress:    sdk.AccAddress(valAddr.Bytes()).String(),
		ValidatorSrcAddress: valAddr.String(),
		Entries:             []stakingtypes.RedelegationEntry{{CreationHeight: creationHeight}},
	}
}

// seedLockedValidator writes metadata for a validator whose lock-up window is
// active at blockTime (BondingStart == blockTime, 180-day duration) and whose
// lock began at testLockHeight, bypassing status validation via a direct Set.
func seedLockedValidator(t *testing.T, f *fixture, valAddr sdk.ValAddress, bondingStart time.Time) {
	t.Helper()
	addrStr, err := f.addressCodec.BytesToString(valAddr)
	require.NoError(t, err)
	md := sampleValidatorMetadata(addrStr)
	md.Status = types.Status_STATUS_ACTIVE
	md.BondingStart = bondingStart
	md.BondingStartHeight = testLockHeight
	md.LockupDurationSeconds = int64(180 * 24 * 3600)
	require.NoError(t, f.keeper.ValidatorMetadata.Set(f.ctx, valAddr.Bytes(), md))
}

func TestSelfStakeLockupInvariant(t *testing.T) {
	blockTime := time.Unix(1700000000, 0).UTC()
	valAddr := sdk.ValAddress([]byte("lockedvalidator20byt"))[:20]

	// (a) CLEAN: locked validator, no self-unbonding in flight -> not broken.
	t.Run("clean", func(t *testing.T) {
		f := initFixtureWithStaking(t, stubStakingKeeper{})
		seedLockedValidator(t, f, valAddr, blockTime)
		ctx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)

		_, broken := f.keeper.SelfStakeLockupInvariant(ctx)
		require.False(t, broken)
	})

	// (b) VIOLATION: locked validator WITH a self-unbonding created AT the lock
	// start (>= BondingStartHeight) -> broken, names addr.
	t.Run("violation", func(t *testing.T) {
		f := initFixtureWithStaking(t, stubStakingKeeper{unbondingHeights: []int64{testLockHeight}})
		seedLockedValidator(t, f, valAddr, blockTime)
		ctx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)

		msg, broken := f.keeper.SelfStakeLockupInvariant(ctx)
		require.True(t, broken)
		require.Contains(t, msg, valAddr.String())
	})

	// (b2) PRE-LOCK UNBONDING GUARD (false-positive fix): a self-unbonding whose
	// entry was created BEFORE the lock began (< BondingStartHeight) is legitimate
	// (the validator self-undelegated while still unbonded, then bonded) and must
	// NOT halt the chain, even though it lingers into the window.
	t.Run("pre_lock_unbonding_ok", func(t *testing.T) {
		f := initFixtureWithStaking(t, stubStakingKeeper{unbondingHeights: []int64{testLockHeight - 1}})
		seedLockedValidator(t, f, valAddr, blockTime)
		ctx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)

		_, broken := f.keeper.SelfStakeLockupInvariant(ctx)
		require.False(t, broken, "a pre-lock self-unbonding must not break the invariant")
	})

	// (c) UNLOCKED GUARD: lock-up expired (BondingStart 200 days ago) even with a
	// self-unbonding present -> not broken; the invariant must skip unlocked ones.
	t.Run("unlocked_guard", func(t *testing.T) {
		f := initFixtureWithStaking(t, stubStakingKeeper{unbondingHeights: []int64{testLockHeight}})
		seedLockedValidator(t, f, valAddr, blockTime.Add(-200*24*time.Hour))
		ctx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)

		_, broken := f.keeper.SelfStakeLockupInvariant(ctx)
		require.False(t, broken)
	})

	// (c2) STATUS-INDEPENDENCE: a JAILED validator inside its window with a
	// post-lock self-unbonding is still locked -> broken (design decision #2:
	// lock-up is purely time-based, jailed validators cannot exit self-stake).
	t.Run("jailed_still_locked", func(t *testing.T) {
		f := initFixtureWithStaking(t, stubStakingKeeper{unbondingHeights: []int64{testLockHeight}})
		addrStr, err := f.addressCodec.BytesToString(valAddr)
		require.NoError(t, err)
		md := sampleValidatorMetadata(addrStr)
		md.Status = types.Status_STATUS_JAILED
		md.BondingStart = blockTime
		md.BondingStartHeight = testLockHeight
		md.LockupDurationSeconds = int64(180 * 24 * 3600)
		require.NoError(t, f.keeper.ValidatorMetadata.Set(f.ctx, valAddr.Bytes(), md))
		ctx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)

		_, broken := f.keeper.SelfStakeLockupInvariant(ctx)
		require.True(t, broken, "a jailed validator is still locked (status-independent)")
	})

	// (d) REDELEGATION VIOLATION: locked validator redelegating its OWN self-stake
	// away (delegator == validator account, entry created at the lock start) ->
	// broken. Guards the escape vector GetUnbondingDelegation cannot see.
	t.Run("self_redelegation_violation", func(t *testing.T) {
		f := initFixtureWithStaking(t, stubStakingKeeper{
			redelegations: []stakingtypes.Redelegation{selfRedelegation(valAddr, testLockHeight)},
		})
		seedLockedValidator(t, f, valAddr, blockTime)
		ctx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)

		msg, broken := f.keeper.SelfStakeLockupInvariant(ctx)
		require.True(t, broken)
		require.Contains(t, msg, valAddr.String())
		require.Contains(t, msg, "self-redelegation")
	})

	// (d2) PRE-LOCK REDELEGATION GUARD: a self-redelegation entry created before the
	// lock began must NOT break the invariant.
	t.Run("pre_lock_redelegation_ok", func(t *testing.T) {
		f := initFixtureWithStaking(t, stubStakingKeeper{
			redelegations: []stakingtypes.Redelegation{selfRedelegation(valAddr, testLockHeight-1)},
		})
		seedLockedValidator(t, f, valAddr, blockTime)
		ctx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)

		_, broken := f.keeper.SelfStakeLockupInvariant(ctx)
		require.False(t, broken, "a pre-lock self-redelegation must not break the invariant")
	})

	// (e) THIRD-PARTY REDELEGATION: a non-self delegator redelegating away from the
	// locked validator is legitimate (third parties are never locked) -> not broken.
	t.Run("third_party_redelegation_ok", func(t *testing.T) {
		other := sdk.AccAddress([]byte("someotherdelegator00")).String()
		f := initFixtureWithStaking(t, stubStakingKeeper{
			redelegations: []stakingtypes.Redelegation{
				{DelegatorAddress: other, ValidatorSrcAddress: valAddr.String(), Entries: []stakingtypes.RedelegationEntry{{CreationHeight: testLockHeight}}},
			},
		})
		seedLockedValidator(t, f, valAddr, blockTime)
		ctx := sdk.UnwrapSDKContext(f.ctx).WithBlockTime(blockTime)

		_, broken := f.keeper.SelfStakeLockupInvariant(ctx)
		require.False(t, broken)
	})
}

func TestValidatorStatusInvariant_GoodState(t *testing.T) {
	f := initFixture(t)
	seedValidator(t, f, "ok1", types.Status_STATUS_ACTIVE)
	seedValidator(t, f, "ok2", types.Status_STATUS_BONDING)
	seedValidator(t, f, "ok3", types.Status_STATUS_UNBONDING)

	msg, broken := f.keeper.ValidatorStatusInvariant(f.ctx)
	require.False(t, broken, "valid statuses should not break the invariant: %s", msg)
}

func TestValidatorStatusInvariant_BadState(t *testing.T) {
	f := initFixture(t)
	seedValidator(t, f, "good", types.Status_STATUS_ACTIVE)

	// Bypass the validating SetValidatorMetadata by writing directly to the
	// collections map, simulating state corruption.
	badAddr := sdk.ValAddress([]byte("corrupt-validator20b"))[:20]
	addrStr, err := f.addressCodec.BytesToString(badAddr)
	require.NoError(t, err)
	bad := sampleValidatorMetadata(addrStr)
	bad.Status = types.Status_STATUS_UNSPECIFIED
	require.NoError(t, f.keeper.ValidatorMetadata.Set(f.ctx, badAddr.Bytes(), bad))

	msg, broken := f.keeper.ValidatorStatusInvariant(f.ctx)
	require.True(t, broken, "invalid status must break the invariant")
	require.Contains(t, msg, badAddr.String())
}
