package types_test

import (
	"bytes"
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

func valoper(b byte) sdk.ValAddress {
	return sdk.ValAddress(bytes.Repeat([]byte{b}, 20))
}

func TestValidateEntityID(t *testing.T) {
	for _, tc := range []struct {
		id string
		ok bool
	}{
		{"abc", true},
		{"saicon-devnet-operator", true},
		{"a" + strings.Repeat("b", 63), true},
		{"", false},
		{"ab", false},
		{"1abc", false},
		{"Abc", false},
		{"ab_c", false},
		{"a" + strings.Repeat("b", 64), false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			err := types.ValidateEntityID(tc.id)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.ErrInvalidEntity)
		})
	}
}

func TestValidateDisplayName(t *testing.T) {
	for _, tc := range []struct {
		name, v string
		ok      bool
	}{
		{"ascii", "SAiCoN DevNet operator", true},
		{"cyrillic", "\u0401\u043b\u043a\u0430 Ltd", true},
		{"64 bytes", strings.Repeat("a", 64), true},
		{"empty", "", false},
		{"65 bytes", strings.Repeat("a", 65), false},
		{"newline", "a\nb", false},
		{"nul", "a\x00b", false},
		{"invalid utf-8", string([]byte{0xff}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := types.ValidateDisplayName(tc.v)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.ErrInvalidEntity)
		})
	}
}

func TestValidateAttestationHash(t *testing.T) {
	for _, tc := range []struct {
		name, v string
		ok      bool
	}{
		{"64 lowercase hex", strings.Repeat("ab", 32), true},
		{"63 chars", strings.Repeat("a", 63), false},
		{"65 chars", strings.Repeat("a", 65), false},
		{"uppercase", strings.Repeat("AB", 32), false},
		{"non-hex", "zz" + strings.Repeat("a", 62), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := types.ValidateAttestationHash(tc.v)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.ErrInvalidEntity)
		})
	}
}

func TestValidateReason(t *testing.T) {
	for _, tc := range []struct {
		name, v string
		ok      bool
	}{
		{"one byte", "x", true},
		{"256 bytes", strings.Repeat("x", 256), true},
		{"empty", "", false},
		{"257 bytes", strings.Repeat("x", 257), false},
		{"invalid utf-8", string([]byte{0xff}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := types.ValidateReason(tc.v)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.ErrInvalidEntity)
		})
	}
}

func TestAttestationValidate(t *testing.T) {
	vals := func(n int) []sdk.ValAddress {
		out := make([]sdk.ValAddress, n)
		for i := range out {
			out[i] = valoper(byte(i + 1))
		}
		return out
	}
	good := func() types.Attestation {
		return types.Attestation{
			EntityID:        "acme",
			DisplayName:     "Acme Ltd",
			AttestationHash: strings.Repeat("ab", 32),
			Validators:      vals(2),
		}
	}
	for _, tc := range []struct {
		name string
		mut  func(*types.Attestation)
		want error
	}{
		{"good", func(*types.Attestation) {}, nil},
		{"no validators", func(a *types.Attestation) { a.Validators = nil }, types.ErrInvalidEntity},
		{"eight validators", func(a *types.Attestation) { a.Validators = vals(8) }, nil},
		{"nine validators", func(a *types.Attestation) { a.Validators = vals(9) }, types.ErrTooManyMembers},
		{"duplicate validator", func(a *types.Attestation) { a.Validators = []sdk.ValAddress{valoper(1), valoper(2), valoper(1)} }, types.ErrInvalidEntity},
		{"empty validator", func(a *types.Attestation) { a.Validators = []sdk.ValAddress{nil} }, types.ErrInvalidEntity},
		{"bad id", func(a *types.Attestation) { a.EntityID = "Bad" }, types.ErrInvalidEntity},
		{"bad name", func(a *types.Attestation) { a.DisplayName = "" }, types.ErrInvalidEntity},
		{"bad hash", func(a *types.Attestation) { a.AttestationHash = "00" }, types.ErrInvalidEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := good()
			tc.mut(&a)
			err := a.Validate()
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestDecodeAddresses(t *testing.T) {
	acc := sdk.MustBech32ifyAddressBytes(types.AccountPrefix, bytes.Repeat([]byte{1}, 20))
	val := sdk.MustBech32ifyAddressBytes(types.ValidatorPrefix, bytes.Repeat([]byte{1}, 20))
	_, err := types.DecodeAccount(acc)
	require.NoError(t, err)
	_, err = types.DecodeAccount(val)
	require.Error(t, err)
	bz, err := types.DecodeValidator(val)
	require.NoError(t, err)
	require.Equal(t, bytes.Repeat([]byte{1}, 20), bz)
	_, err = types.DecodeValidator(acc)
	require.Error(t, err)
	_, err = types.DecodeValidator("not-bech32")
	require.Error(t, err)
}

func TestAttestationVrfKeys(t *testing.T) {
	key := func(h string) []byte {
		pk, err := types.DecodeVrfPublicKey(h)
		require.NoError(t, err)
		return pk
	}
	base := func(bindings ...types.VrfKeyBinding) types.Attestation {
		return types.Attestation{
			EntityID:        "alpha",
			DisplayName:     "Alpha",
			AttestationHash: strings.Repeat("ab", 32),
			Validators:      []sdk.ValAddress{valoper(1), valoper(2)},
			VrfKeys:         bindings,
		}
	}

	require.NoError(t, base().Validate())
	require.NoError(t, base(types.VrfKeyBinding{Validator: valoper(1), PublicKey: key(rfcKey19)}).Validate())
	require.NoError(t, base(
		types.VrfKeyBinding{Validator: valoper(1), PublicKey: key(rfcKey19)},
		types.VrfKeyBinding{Validator: valoper(2), PublicKey: key(rfcKey20)},
	).Validate())

	t.Run("validator not listed", func(t *testing.T) {
		err := base(types.VrfKeyBinding{Validator: valoper(3), PublicKey: key(rfcKey19)}).Validate()
		require.ErrorIs(t, err, types.ErrInvalidEntity)
	})
	t.Run("two bindings for one validator", func(t *testing.T) {
		err := base(
			types.VrfKeyBinding{Validator: valoper(1), PublicKey: key(rfcKey19)},
			types.VrfKeyBinding{Validator: valoper(1), PublicKey: key(rfcKey20)},
		).Validate()
		require.ErrorIs(t, err, types.ErrInvalidEntity)
	})
	t.Run("bad key", func(t *testing.T) {
		for name, pk := range map[string][]byte{
			"short":    key(rfcKey19)[:31],
			"empty":    nil,
			"identity": append([]byte{1}, make([]byte, 31)...),
		} {
			t.Run(name, func(t *testing.T) {
				err := base(types.VrfKeyBinding{Validator: valoper(1), PublicKey: pk}).Validate()
				require.ErrorIs(t, err, types.ErrInvalidVrfKey)
			})
		}
	})
}
