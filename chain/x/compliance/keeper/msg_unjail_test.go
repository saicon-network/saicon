package keeper_test

import (
	"math"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestUnjail(t *testing.T) {
	valAddr := sdk.ValAddress([]byte("unjailvalidator20byt"))[:20]

	// setup seeds a JAILED validator at jailHeight, sets the block height and the
	// cooldown param, and returns the fixture + msgServer.
	setup := func(t *testing.T, jailHeight, nowHeight, cooldown int64, tombstoned bool) (*fixture, types.MsgServer) {
		f := initFixture(t)
		f.vr.statuses[valAddr.String()] = vrtypes.Status_STATUS_JAILED
		f.vr.heights[valAddr.String()] = jailHeight
		f.slashing.tombstoned = tombstoned
		require.NoError(t, f.keeper.Params.Set(f.ctx, types.NewParams(cooldown)))
		f.ctx = sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(nowHeight)
		return f, keeper.NewMsgServerImpl(f.keeper)
	}
	authorityOf := func(f *fixture) string {
		s, err := f.addressCodec.BytesToString(f.keeper.GetAuthority())
		require.NoError(t, err)
		return s
	}

	// happy path: cooldown elapsed (boundary now-jail == cooldown), not tombstoned
	// -> ACTIVE + C=1
	t.Run("happy", func(t *testing.T) {
		f, ms := setup(t, 100, 150, 50, false)
		_, err := ms.Unjail(f.ctx, &types.MsgUnjail{Authority: authorityOf(f), ValidatorAddress: valAddr.String(), Reason: "ok"})
		require.NoError(t, err)
		require.Equal(t, vrtypes.Status_STATUS_ACTIVE, f.vr.statuses[valAddr.String()])
		require.True(t, f.vr.flags[valAddr.String()])
		// validator_unjailed event emitted (design Testing requirement)
		var emitted bool
		for _, e := range sdk.UnwrapSDKContext(f.ctx).EventManager().Events() {
			if e.Type == types.EventTypeValidatorUnjailed {
				emitted = true
			}
		}
		require.True(t, emitted, "expected a validator_unjailed event")
	})

	// overflow regression: a near-MaxInt64 cooldown must still gate (fail closed),
	// not wrap negative and bypass. Guards against the jailHeight+cooldown form.
	t.Run("cooldown_overflow_safe", func(t *testing.T) {
		f, ms := setup(t, 100, 150, math.MaxInt64, false)
		_, err := ms.Unjail(f.ctx, &types.MsgUnjail{Authority: authorityOf(f), ValidatorAddress: valAddr.String()})
		require.ErrorIs(t, err, types.ErrUnjailCooldown)
	})

	// cooldown not elapsed (one block short) -> ErrUnjailCooldown
	t.Run("cooldown_not_elapsed", func(t *testing.T) {
		f, ms := setup(t, 100, 149, 50, false)
		_, err := ms.Unjail(f.ctx, &types.MsgUnjail{Authority: authorityOf(f), ValidatorAddress: valAddr.String()})
		require.ErrorIs(t, err, types.ErrUnjailCooldown)
		require.Equal(t, vrtypes.Status_STATUS_JAILED, f.vr.statuses[valAddr.String()])
	})

	// tombstoned -> ErrValidatorTombstoned
	t.Run("tombstoned", func(t *testing.T) {
		f, ms := setup(t, 100, 150, 50, true)
		_, err := ms.Unjail(f.ctx, &types.MsgUnjail{Authority: authorityOf(f), ValidatorAddress: valAddr.String()})
		require.ErrorIs(t, err, types.ErrValidatorTombstoned)
		require.Equal(t, vrtypes.Status_STATUS_JAILED, f.vr.statuses[valAddr.String()])
	})

	// not jailed (ACTIVE) -> ErrNotJailed
	t.Run("not_jailed", func(t *testing.T) {
		f, ms := setup(t, 100, 150, 50, false)
		f.vr.statuses[valAddr.String()] = vrtypes.Status_STATUS_ACTIVE
		_, err := ms.Unjail(f.ctx, &types.MsgUnjail{Authority: authorityOf(f), ValidatorAddress: valAddr.String()})
		require.ErrorIs(t, err, types.ErrNotJailed)
	})

	// QUARANTINED -> ErrNotJailed (unjail is JAILED-only; quarantine uses ClearFlag)
	t.Run("not_jailed_quarantined", func(t *testing.T) {
		f, ms := setup(t, 100, 150, 50, false)
		f.vr.statuses[valAddr.String()] = vrtypes.Status_STATUS_QUARANTINED
		_, err := ms.Unjail(f.ctx, &types.MsgUnjail{Authority: authorityOf(f), ValidatorAddress: valAddr.String()})
		require.ErrorIs(t, err, types.ErrNotJailed)
	})

	// non-authority signer -> ErrInvalidSigner
	t.Run("bad_authority", func(t *testing.T) {
		f, ms := setup(t, 100, 150, 50, false)
		other := sdk.AccAddress([]byte("someotheraccount0000")).String()
		_, err := ms.Unjail(f.ctx, &types.MsgUnjail{Authority: other, ValidatorAddress: valAddr.String()})
		require.ErrorIs(t, err, types.ErrInvalidSigner)
	})

	// validator absent in staking -> ErrValidatorNotFound (fail-safe)
	t.Run("val_not_found", func(t *testing.T) {
		f, ms := setup(t, 100, 150, 50, false)
		f.staking.found = false
		_, err := ms.Unjail(f.ctx, &types.MsgUnjail{Authority: authorityOf(f), ValidatorAddress: valAddr.String()})
		require.ErrorIs(t, err, types.ErrValidatorNotFound)
	})
}
