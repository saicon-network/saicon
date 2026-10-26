package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/keeper"
	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestRegisterValidator_CreatesBonding(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	accAddr := sdk.AccAddress([]byte("registrant-acct-20by"))[:20]
	creator, err := f.addressCodec.BytesToString(accAddr)
	require.NoError(t, err)

	resp, err := ms.RegisterValidator(f.ctx, &types.MsgRegisterValidator{
		Creator:               creator,
		LockupDurationSeconds: 0, // default
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.ValidatorAddress)

	valAddr := sdk.ValAddress(accAddr)
	md, found, err := f.keeper.GetValidatorMetadata(f.ctx, valAddr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.Status_STATUS_BONDING, md.Status)
	require.Equal(t, types.DefaultLockupDurationSeconds, md.LockupDurationSeconds)
	require.True(t, md.ComplianceFlag)
}

// A validator may commit to a LONGER lock-up than the chain minimum.
func TestRegisterValidator_CustomLockup(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	const fiveYears = int64(5 * 365 * 24 * 60 * 60)

	accAddr := sdk.AccAddress([]byte("registrant-acct-cust"))[:20]
	creator, _ := f.addressCodec.BytesToString(accAddr)

	_, err := ms.RegisterValidator(f.ctx, &types.MsgRegisterValidator{
		Creator:               creator,
		LockupDurationSeconds: fiveYears,
	})
	require.NoError(t, err)

	md, found, err := f.keeper.GetValidatorMetadata(f.ctx, sdk.ValAddress(accAddr))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, fiveYears, md.LockupDurationSeconds)
}

// ...but never a SHORTER one. This message is permissionless and the duration is
// signer-supplied, so without a floor an operator could register a one-second
// lock-up and walk straight past V-3: IsLocked would be false, the ante would
// wave the self-undelegate through, and the invariant would skip the validator.
// The bypass was previously neutralised only by accident — the registry record
// was deleted on MsgCreateValidator and re-created with the default — and became
// live once the reset was correctly narrowed to terminal records.
func TestRegisterValidator_RejectsLockupBelowTheMinimum(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	accAddr := sdk.AccAddress([]byte("registrant-acct-shrt"))[:20]
	creator, _ := f.addressCodec.BytesToString(accAddr)

	_, err := ms.RegisterValidator(f.ctx, &types.MsgRegisterValidator{
		Creator:               creator,
		LockupDurationSeconds: 1,
	})
	require.ErrorIs(t, err, types.ErrLockupTooShort)

	_, found, gerr := f.keeper.GetValidatorMetadata(f.ctx, sdk.ValAddress(accAddr))
	require.NoError(t, gerr)
	require.False(t, found, "a rejected registration must leave no record")
}

// Zero keeps its meaning of "use the chain default".
func TestRegisterValidator_ZeroLockupUsesDefault(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	accAddr := sdk.AccAddress([]byte("registrant-acct-zero"))[:20]
	creator, _ := f.addressCodec.BytesToString(accAddr)

	_, err := ms.RegisterValidator(f.ctx, &types.MsgRegisterValidator{Creator: creator})
	require.NoError(t, err)

	md, found, err := f.keeper.GetValidatorMetadata(f.ctx, sdk.ValAddress(accAddr))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.DefaultLockupDurationSeconds, md.LockupDurationSeconds)
	require.Positive(t, md.BondingStartHeight,
		"a zero lock start is the V-3 false-positive-halt primitive; it must never be stored")
}

func TestRegisterValidator_RejectsDuplicate(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	accAddr := sdk.AccAddress([]byte("registrant-acct-dup!"))[:20]
	creator, _ := f.addressCodec.BytesToString(accAddr)

	msg := &types.MsgRegisterValidator{Creator: creator}
	_, err := ms.RegisterValidator(f.ctx, msg)
	require.NoError(t, err)

	_, err = ms.RegisterValidator(f.ctx, msg)
	require.Error(t, err)
	require.ErrorIs(t, err, types.ErrValidatorAlreadyRegistered)
}

func TestRegisterValidator_InvalidCreator(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	_, err := ms.RegisterValidator(f.ctx, &types.MsgRegisterValidator{Creator: "not-a-bech32"})
	require.Error(t, err)
}

func TestRegisterThenChangeStatus_EndToEnd(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	accAddr := sdk.AccAddress([]byte("registrant-acct-e2e!"))[:20]
	creator, _ := f.addressCodec.BytesToString(accAddr)
	valAddr := sdk.ValAddress(accAddr)

	// register → bonding
	_, err := ms.RegisterValidator(f.ctx, &types.MsgRegisterValidator{Creator: creator})
	require.NoError(t, err)
	md, _, _ := f.keeper.GetValidatorMetadata(f.ctx, valAddr)
	require.Equal(t, types.Status_STATUS_BONDING, md.Status)

	// bonding → active via ChangeStatus
	require.NoError(t, f.keeper.ChangeStatus(f.ctx, valAddr, types.Status_STATUS_ACTIVE))
	md, _, _ = f.keeper.GetValidatorMetadata(f.ctx, valAddr)
	require.Equal(t, types.Status_STATUS_ACTIVE, md.Status)
}

// The duration is multiplied into nanoseconds, so an unbounded value overflows
// int64 and wraps the expiry into the past: 9223372037 seconds put it in 1733 and
// left a freshly bonded self-stake unlocked. The maximum is accepted, one second
// more is not, and neither is the overflowing value.
func TestRegisterValidator_LockupMaximum(t *testing.T) {
	cases := []struct {
		name   string
		lockup int64
		err    error
	}{
		{"maximum", types.MaxLockupDurationSeconds, nil},
		{"maximum plus one", types.MaxLockupDurationSeconds + 1, types.ErrLockupTooLong},
		{"overflowing duration", 9223372037, types.ErrLockupTooLong},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := initFixture(t)
			ms := keeper.NewMsgServerImpl(f.keeper)

			accAddr := sdk.AccAddress([]byte{byte('a' + i), 'm', 'a', 'x', '-', 'l', 'o', 'c', 'k', 'u', 'p', '-', 'r', 'e', 'g', 'i', 's', 't', 'r', 'y'})
			creator, _ := f.addressCodec.BytesToString(accAddr)

			_, err := ms.RegisterValidator(f.ctx, &types.MsgRegisterValidator{
				Creator:               creator,
				LockupDurationSeconds: tc.lockup,
			})
			_, found, gerr := f.keeper.GetValidatorMetadata(f.ctx, sdk.ValAddress(accAddr))
			require.NoError(t, gerr)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.False(t, found, "a rejected registration must leave no record")
				return
			}
			require.NoError(t, err)
			require.True(t, found)
		})
	}
}
