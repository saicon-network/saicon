package keeper_test

import (
	"context"
	"testing"

	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/core/comet"

	"github.com/saicon-network/saicon/chain/x/participation/keeper"
	module "github.com/saicon-network/saicon/chain/x/participation/module"
	"github.com/saicon-network/saicon/chain/x/participation/types"
)

// ---------------------------------------------------------------------------
// comet.VoteInfo / comet.Validator fakes
// ---------------------------------------------------------------------------

// fakeVoteValidator implements comet.Validator.
type fakeVoteValidator struct {
	addr  []byte
	power int64
}

func (f fakeVoteValidator) Address() []byte { return f.addr }
func (f fakeVoteValidator) Power() int64    { return f.power }

// fakeVoteInfo implements comet.VoteInfo.
type fakeVoteInfo struct {
	validator comet.Validator
	flag      comet.BlockIDFlag
}

func (f fakeVoteInfo) Validator() comet.Validator        { return f.validator }
func (f fakeVoteInfo) GetBlockIDFlag() comet.BlockIDFlag { return f.flag }

// ---------------------------------------------------------------------------
// Extended mock staking keeper (signing tests)
// ---------------------------------------------------------------------------

// signingMockStakingKeeper extends mockStakingKeeper with a per-cons-addr map
// so specific consensus addresses can be resolved to known validators. The
// IterateBondedValidatorsByPower stub is inherited via composition and continues
// to return whatever validators were provided, so existing epoch_test.go tests
// are unaffected when they construct their own mockStakingKeeper without the map.
type signingMockStakingKeeper struct {
	// Embed the existing mockStakingKeeper to inherit IterateBondedValidatorsByPower.
	mockStakingKeeper
	// consToVal maps consensus address string → validator (for GetValidatorByConsAddr).
	// Keys are added by individual tests.
	consToVal map[string]stakingtypes.Validator
}

func (m signingMockStakingKeeper) GetValidatorByConsAddr(_ context.Context, consAddr sdk.ConsAddress) (stakingtypes.Validator, error) {
	if v, ok := m.consToVal[consAddr.String()]; ok {
		return v, nil
	}
	return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
}

// ---------------------------------------------------------------------------
// Fixture for signing tests (uses signingMockStakingKeeper).
// ---------------------------------------------------------------------------

func initSigningFixture(t *testing.T, sk types.StakingKeeper) *fixture {
	t.Helper()
	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(storeKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_test")).Ctx
	authority := authtypes.NewModuleAddress(types.GovModuleName)

	k := keeper.NewKeeper(
		storeService,
		encCfg.Codec,
		addressCodec,
		authority,
		nil,
		sk,
	)
	if err := k.Params.Set(ctx, types.DefaultParams()); err != nil {
		t.Fatalf("failed to set params: %v", err)
	}
	return &fixture{ctx: ctx, keeper: k, addressCodec: addressCodec}
}

// ---------------------------------------------------------------------------
// Helper to build a minimal stakingtypes.Validator with just an Operator field.
// ---------------------------------------------------------------------------

func makeStakingVal(valAddr sdk.ValAddress) stakingtypes.Validator {
	return stakingtypes.Validator{OperatorAddress: valAddr.String()}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// Test 1: one committed vote for a known validator → Signed=1, Missed absent/0.
func TestRecordEpochVotes_CommittedVote(t *testing.T) {
	consAddr := sdk.ConsAddress([]byte("signing-cons-addr-aaaa"))[:20]
	valAddr := sdk.ValAddress([]byte("signing-val-addr-aaaa"))[:20]

	sk := signingMockStakingKeeper{
		consToVal: map[string]stakingtypes.Validator{
			consAddr.String(): makeStakingVal(valAddr),
		},
	}
	f := initSigningFixture(t, sk)

	votes := []comet.VoteInfo{
		fakeVoteInfo{
			validator: fakeVoteValidator{addr: consAddr, power: 100},
			flag:      comet.BlockIDFlagCommit,
		},
	}
	require.NoError(t, f.keeper.RecordEpochVotes(f.ctx, votes))

	signed, err := f.keeper.EpochBlocksSigned.Get(f.ctx, valAddr.Bytes())
	require.NoError(t, err)
	require.Equal(t, uint64(1), signed)

	// Missed should not exist (ErrNotFound acceptable, or 0).
	missed, err := f.keeper.EpochBlocksMissed.Get(f.ctx, valAddr.Bytes())
	if err == nil {
		require.Equal(t, uint64(0), missed)
	}
}

// Test 2: one absent vote for a known validator → Missed=1, Signed absent/0.
func TestRecordEpochVotes_AbsentVote(t *testing.T) {
	consAddr := sdk.ConsAddress([]byte("signing-cons-addr-bbbb"))[:20]
	valAddr := sdk.ValAddress([]byte("signing-val-addr-bbbb"))[:20]

	sk := signingMockStakingKeeper{
		consToVal: map[string]stakingtypes.Validator{
			consAddr.String(): makeStakingVal(valAddr),
		},
	}
	f := initSigningFixture(t, sk)

	votes := []comet.VoteInfo{
		fakeVoteInfo{
			validator: fakeVoteValidator{addr: consAddr, power: 100},
			flag:      comet.BlockIDFlagAbsent,
		},
	}
	require.NoError(t, f.keeper.RecordEpochVotes(f.ctx, votes))

	missed, err := f.keeper.EpochBlocksMissed.Get(f.ctx, valAddr.Bytes())
	require.NoError(t, err)
	require.Equal(t, uint64(1), missed)

	// Signed should not exist or be 0.
	signed, err := f.keeper.EpochBlocksSigned.Get(f.ctx, valAddr.Bytes())
	if err == nil {
		require.Equal(t, uint64(0), signed)
	}
}

// Test 3: vote for an unknown cons-addr → no counter changes (skipped).
func TestRecordEpochVotes_UnknownConsAddr(t *testing.T) {
	// sk has no entries — all lookups return ErrNoValidatorFound.
	sk := signingMockStakingKeeper{
		consToVal: map[string]stakingtypes.Validator{},
	}
	f := initSigningFixture(t, sk)

	unknownCons := sdk.ConsAddress([]byte("signing-cons-addr-cccc"))[:20]
	votes := []comet.VoteInfo{
		fakeVoteInfo{
			validator: fakeVoteValidator{addr: unknownCons, power: 50},
			flag:      comet.BlockIDFlagCommit,
		},
	}
	require.NoError(t, f.keeper.RecordEpochVotes(f.ctx, votes))

	// No counter should exist for any address.
	// We probe the unknown address (its valAddr bytes won't be set either).
	fakeValAddr := sdk.ValAddress([]byte("signing-val-addr-cccc"))[:20]
	_, err := f.keeper.EpochBlocksSigned.Get(f.ctx, fakeValAddr.Bytes())
	require.Error(t, err) // ErrNotFound
	_, err = f.keeper.EpochBlocksMissed.Get(f.ctx, fakeValAddr.Bytes())
	require.Error(t, err) // ErrNotFound
}

// Test 4: multiple calls accumulate (3 committed + 1 absent → signed=3, missed=1).
func TestRecordEpochVotes_Accumulates(t *testing.T) {
	consAddr := sdk.ConsAddress([]byte("signing-cons-addr-dddd"))[:20]
	valAddr := sdk.ValAddress([]byte("signing-val-addr-dddd"))[:20]

	sk := signingMockStakingKeeper{
		consToVal: map[string]stakingtypes.Validator{
			consAddr.String(): makeStakingVal(valAddr),
		},
	}
	f := initSigningFixture(t, sk)

	commit := []comet.VoteInfo{
		fakeVoteInfo{validator: fakeVoteValidator{addr: consAddr, power: 100}, flag: comet.BlockIDFlagCommit},
	}
	absent := []comet.VoteInfo{
		fakeVoteInfo{validator: fakeVoteValidator{addr: consAddr, power: 100}, flag: comet.BlockIDFlagAbsent},
	}

	require.NoError(t, f.keeper.RecordEpochVotes(f.ctx, commit))
	require.NoError(t, f.keeper.RecordEpochVotes(f.ctx, commit))
	require.NoError(t, f.keeper.RecordEpochVotes(f.ctx, commit))
	require.NoError(t, f.keeper.RecordEpochVotes(f.ctx, absent))

	signed, err := f.keeper.EpochBlocksSigned.Get(f.ctx, valAddr.Bytes())
	require.NoError(t, err)
	require.Equal(t, uint64(3), signed)

	missed, err := f.keeper.EpochBlocksMissed.Get(f.ctx, valAddr.Bytes())
	require.NoError(t, err)
	require.Equal(t, uint64(1), missed)
}

// Test 5: epochUptime — 3 signed / 1 missed → 0.75; 0 signed / 2 missed → 0; no blocks → 1.
func TestEpochUptime(t *testing.T) {
	sk := signingMockStakingKeeper{consToVal: map[string]stakingtypes.Validator{}}
	f := initSigningFixture(t, sk)

	valAddr := sdk.ValAddress([]byte("signing-val-addr-eeee"))[:20]

	// Sub-case A: 3 signed, 1 missed → 0.75
	require.NoError(t, f.keeper.EpochBlocksSigned.Set(f.ctx, valAddr.Bytes(), 3))
	require.NoError(t, f.keeper.EpochBlocksMissed.Set(f.ctx, valAddr.Bytes(), 1))

	uptime, signed, missed, err := f.keeper.EpochUptimeForTest(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, uint64(3), signed)
	require.Equal(t, uint64(1), missed)
	require.Equal(t, "0.750000000000000000", uptime.String())

	// Sub-case B: 0 signed, 2 missed → 0.0
	require.NoError(t, f.keeper.EpochBlocksSigned.Set(f.ctx, valAddr.Bytes(), 0))
	require.NoError(t, f.keeper.EpochBlocksMissed.Set(f.ctx, valAddr.Bytes(), 2))

	uptime, _, _, err = f.keeper.EpochUptimeForTest(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, "0.000000000000000000", uptime.String())

	// Sub-case C: no entries at all → uptime == 1 (benefit of doubt).
	valAddr2 := sdk.ValAddress([]byte("signing-val-addr-zzzz"))[:20]
	uptime, signed, missed, err = f.keeper.EpochUptimeForTest(f.ctx, valAddr2)
	require.NoError(t, err)
	require.Equal(t, uint64(0), signed)
	require.Equal(t, uint64(0), missed)
	require.Equal(t, "1.000000000000000000", uptime.String())
}

// Test 6: ResetEpochCounters clears both maps; epochUptime returns default 1 afterward.
func TestResetEpochCounters(t *testing.T) {
	consAddr := sdk.ConsAddress([]byte("signing-cons-addr-ffff"))[:20]
	valAddr := sdk.ValAddress([]byte("signing-val-addr-ffff"))[:20]

	sk := signingMockStakingKeeper{
		consToVal: map[string]stakingtypes.Validator{
			consAddr.String(): makeStakingVal(valAddr),
		},
	}
	f := initSigningFixture(t, sk)

	// Record some data first.
	require.NoError(t, f.keeper.EpochBlocksSigned.Set(f.ctx, valAddr.Bytes(), 5))
	require.NoError(t, f.keeper.EpochBlocksMissed.Set(f.ctx, valAddr.Bytes(), 2))

	require.NoError(t, f.keeper.ResetEpochCounters(f.ctx))

	// Both counters must be gone.
	_, errS := f.keeper.EpochBlocksSigned.Get(f.ctx, valAddr.Bytes())
	require.Error(t, errS)
	_, errM := f.keeper.EpochBlocksMissed.Get(f.ctx, valAddr.Bytes())
	require.Error(t, errM)

	// epochUptime must return the "no blocks" default: 1.
	uptime, s, m, err := f.keeper.EpochUptimeForTest(f.ctx, valAddr)
	require.NoError(t, err)
	require.Equal(t, uint64(0), s)
	require.Equal(t, uint64(0), m)
	require.Equal(t, "1.000000000000000000", uptime.String())
}
