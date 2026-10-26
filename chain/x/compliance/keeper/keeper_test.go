package keeper_test

import (
	"context"
	"testing"

	"cosmossdk.io/core/address"
	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	module "github.com/saicon-network/saicon/chain/x/compliance/module"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// mockVRKeeper is a test double for the x/validatorregistry keeper dependency.
// It records mirrored C flags + per-validator status and can be forced to error.
type mockVRKeeper struct {
	flags    map[string]bool
	statuses map[string]vrtypes.Status
	heights  map[string]int64 // last_status_change_height per validator
	err      error
}

func newMockVRKeeper() *mockVRKeeper {
	return &mockVRKeeper{
		flags:    map[string]bool{},
		statuses: map[string]vrtypes.Status{},
		heights:  map[string]int64{},
	}
}

func (m *mockVRKeeper) GetValidatorMetadata(_ context.Context, addr sdk.ValAddress) (vrtypes.ValidatorMetadata, bool, error) {
	if m.err != nil {
		return vrtypes.ValidatorMetadata{}, false, m.err
	}
	s, ok := m.statuses[addr.String()]
	if !ok {
		return vrtypes.ValidatorMetadata{}, false, nil
	}
	return vrtypes.ValidatorMetadata{
		Status:                 s,
		LastStatusChangeHeight: m.heights[addr.String()],
	}, true, nil
}

// mockStakingKeeper returns a validator with a fixed consensus pubkey.
type mockStakingKeeper struct {
	val   stakingtypes.Validator
	found bool
}

func (m *mockStakingKeeper) GetValidator(_ context.Context, _ sdk.ValAddress) (stakingtypes.Validator, error) {
	if !m.found {
		return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
	}
	return m.val, nil
}

// mockSlashingKeeper reports a configurable tombstone state.
type mockSlashingKeeper struct{ tombstoned bool }

func (m *mockSlashingKeeper) IsTombstoned(_ context.Context, _ sdk.ConsAddress) bool {
	return m.tombstoned
}

// testValidator builds a minimal stakingtypes.Validator whose GetConsAddr works
// (only the consensus pubkey is read by MsgUnjail).
func testValidator(t *testing.T) stakingtypes.Validator {
	t.Helper()
	pk := ed25519.GenPrivKey().PubKey()
	pkAny, err := codectypes.NewAnyWithValue(pk)
	require.NoError(t, err)
	return stakingtypes.Validator{ConsensusPubkey: pkAny}
}

func (m *mockVRKeeper) SetComplianceFlag(_ context.Context, addr sdk.ValAddress, compliant bool) error {
	if m.err != nil {
		return m.err
	}
	m.flags[addr.String()] = compliant
	return nil
}

func (m *mockVRKeeper) GetValidatorStatus(_ context.Context, addr sdk.ValAddress) (vrtypes.Status, bool, error) {
	if m.err != nil {
		return vrtypes.Status_STATUS_UNSPECIFIED, false, m.err
	}
	s, ok := m.statuses[addr.String()]
	if !ok {
		return vrtypes.Status_STATUS_UNSPECIFIED, false, nil
	}
	return s, true, nil
}

func (m *mockVRKeeper) ChangeStatus(_ context.Context, addr sdk.ValAddress, newStatus vrtypes.Status) error {
	if m.err != nil {
		return m.err
	}
	m.statuses[addr.String()] = newStatus
	return nil
}

// mockParticipationKeeper records what the punishment path touched. It
// deliberately has NO way to write EP: it satisfies types.ParticipationKeeper,
// which does not expose SetEP, so invariant V-8 is a property of the interface
// rather than of every caller. epTouched exists only so a test can assert that
// the absence is real and not just untested.
type mockParticipationKeeper struct {
	zeroed    map[string]bool
	epTouched bool
	zeroErr   error
}

func (m *mockParticipationKeeper) ZeroReputation(_ context.Context, addr sdk.ValAddress, _ uint64) error {
	if m.zeroErr != nil {
		return m.zeroErr
	}
	m.zeroed[addr.String()] = true
	return nil
}

func (m *mockParticipationKeeper) EpochLengthBlocks(_ context.Context) (uint64, error) {
	return 100, nil
}

type fixture struct {
	ctx           context.Context
	keeper        keeper.Keeper
	addressCodec  address.Codec
	vr            *mockVRKeeper
	staking       *mockStakingKeeper
	slashing      *mockSlashingKeeper
	participation *mockParticipationKeeper
}

func initFixture(t *testing.T) *fixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)

	storeService := runtime.NewKVStoreService(storeKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_test")).Ctx

	authority := authtypes.NewModuleAddress(types.GovModuleName)

	vr := newMockVRKeeper()
	stakingK := &mockStakingKeeper{found: true, val: testValidator(t)}
	slashingK := &mockSlashingKeeper{}
	participationK := &mockParticipationKeeper{zeroed: map[string]bool{}}

	k := keeper.NewKeeper(
		storeService,
		encCfg.Codec,
		addressCodec,
		authority,
		vr,
		stakingK,
		slashingK,
		participationK,
	)

	// Initialize params
	if err := k.Params.Set(ctx, types.DefaultParams()); err != nil {
		t.Fatalf("failed to set params: %v", err)
	}

	return &fixture{
		ctx:           ctx,
		keeper:        k,
		addressCodec:  addressCodec,
		vr:            vr,
		staking:       stakingK,
		slashing:      slashingK,
		participation: participationK,
	}
}
