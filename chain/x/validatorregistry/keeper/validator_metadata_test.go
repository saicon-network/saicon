package keeper_test

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func sampleValidatorMetadata(addrStr string) types.ValidatorMetadata {
	return types.ValidatorMetadata{
		ValidatorAddress:       addrStr,
		Status:                 types.Status_STATUS_BONDING,
		BondingStart:           time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
		LockupDurationSeconds:  180 * 24 * 60 * 60,
		ComplianceFlag:         true,
		LastStatusChangeHeight: 100,
	}
}

func TestSetAndGetValidatorMetadata(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("validator-001-twenty-bytes!!!"))[:20]

	// Initially not present
	_, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.False(t, found)

	addrStr, err := f.addressCodec.BytesToString(addr)
	require.NoError(t, err)

	md := sampleValidatorMetadata(addrStr)
	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, md))

	got, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, addrStr, got.ValidatorAddress)
	require.Equal(t, types.Status_STATUS_BONDING, got.Status)
	require.Equal(t, int64(180*24*60*60), got.LockupDurationSeconds)
	require.True(t, got.ComplianceFlag)
}

func TestSetValidatorMetadata_RejectsInvalidStatus(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("validator-002-twenty-bytes!!!"))[:20]

	md := types.ValidatorMetadata{
		Status:                types.Status_STATUS_UNSPECIFIED, // invalid
		BondingStart:          time.Now().UTC(),
		LockupDurationSeconds: 1,
	}

	err := f.keeper.SetValidatorMetadata(f.ctx, addr, md)
	require.Error(t, err)
}

func TestHasValidatorMetadata(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("validator-003-twenty-bytes!!!"))[:20]

	has, err := f.keeper.HasValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.False(t, has)

	addrStr, _ := f.addressCodec.BytesToString(addr)
	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, sampleValidatorMetadata(addrStr)))

	has, err = f.keeper.HasValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, has)
}

func TestRemoveValidatorMetadata(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("validator-004-twenty-bytes!!!"))[:20]
	addrStr, _ := f.addressCodec.BytesToString(addr)

	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, sampleValidatorMetadata(addrStr)))
	require.NoError(t, f.keeper.RemoveValidatorMetadata(f.ctx, addr))

	_, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.False(t, found)
}

func TestIterateValidatorMetadata(t *testing.T) {
	f := initFixture(t)

	// Insert 3 distinct validators
	addrs := []sdk.ValAddress{
		sdk.ValAddress([]byte("validator-A-twenty-bytes!!!!!"))[:20],
		sdk.ValAddress([]byte("validator-B-twenty-bytes!!!!!"))[:20],
		sdk.ValAddress([]byte("validator-C-twenty-bytes!!!!!"))[:20],
	}
	for _, a := range addrs {
		addrStr, _ := f.addressCodec.BytesToString(a)
		require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, a, sampleValidatorMetadata(addrStr)))
	}

	seen := map[string]bool{}
	err := f.keeper.IterateValidatorMetadata(f.ctx, func(addr sdk.ValAddress, md types.ValidatorMetadata) (stop bool) {
		seen[addr.String()] = true
		return false
	})
	require.NoError(t, err)
	require.Len(t, seen, 3)
	for _, a := range addrs {
		require.True(t, seen[a.String()], "expected to iterate over %s", a.String())
	}
}

func TestIterateValidatorMetadata_StopEarly(t *testing.T) {
	f := initFixture(t)
	for i := 0; i < 5; i++ {
		buf := []byte("validator-X-twenty-bytes!!!!!")
		buf[10] = byte('a' + i)
		addr := sdk.ValAddress(buf)[:20]
		addrStr, _ := f.addressCodec.BytesToString(addr)
		require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, sampleValidatorMetadata(addrStr)))
	}

	count := 0
	err := f.keeper.IterateValidatorMetadata(f.ctx, func(addr sdk.ValAddress, md types.ValidatorMetadata) (stop bool) {
		count++
		return count >= 2
	})
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestStatusHelpers(t *testing.T) {
	require.Equal(t, "active", types.Status_STATUS_ACTIVE.HumanString())
	require.Equal(t, "unknown", types.Status_STATUS_UNSPECIFIED.HumanString())

	s, err := types.StatusFromString("jailed")
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_JAILED, s)

	_, err = types.StatusFromString("nonsense")
	require.Error(t, err)

	require.True(t, types.Status_STATUS_ACTIVE.IsValid())
	require.False(t, types.Status_STATUS_UNSPECIFIED.IsValid())

	require.True(t, types.Status_STATUS_ACTIVE.IsActive())
	require.False(t, types.Status_STATUS_BONDING.IsActive())

	require.True(t, types.Status_STATUS_ACTIVE.CanReceiveDelegations())
	require.False(t, types.Status_STATUS_QUARANTINED.CanReceiveDelegations())
}
