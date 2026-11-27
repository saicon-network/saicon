package keeper_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"cosmossdk.io/core/address"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
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

// mockStakingKeeper returns a validator with a fixed consensus pubkey, resolves
// validators by consensus address from byCons, and can be made to fail that
// lookup for chosen consensus addresses.
type mockStakingKeeper struct {
	val   stakingtypes.Validator
	found bool

	// byCons maps a consensus address (string of the raw bytes) to its validator.
	byCons map[string]stakingtypes.Validator
	// failCons lists consensus addresses whose GetValidatorByConsAddr errors.
	failCons map[string]bool
	consCdc  address.Codec

	// slashes records every SlashWithInfractionReason call, in order.
	slashes []slashCall
	// slashFn, when set, answers a slash call (it may return an error, call the
	// hooks, or read the window); the default burns nothing.
	slashFn func(ctx context.Context, call slashCall) (math.Int, error)
}

// slashCall is one SlashWithInfractionReason call as x/staking receives it.
type slashCall struct {
	cons       []byte
	height     int64
	power      int64
	fraction   math.LegacyDec
	infraction stakingtypes.Infraction
}

func (m *mockStakingKeeper) GetValidatorByConsAddr(_ context.Context, consAddr sdk.ConsAddress) (stakingtypes.Validator, error) {
	if m.failCons[string(consAddr)] {
		return stakingtypes.Validator{}, errors.New("staking: injected lookup failure")
	}
	v, ok := m.byCons[string(consAddr)]
	if !ok {
		return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
	}
	return v, nil
}

func (m *mockStakingKeeper) SlashWithInfractionReason(ctx context.Context, consAddr sdk.ConsAddress, height, power int64,
	fraction math.LegacyDec, infraction stakingtypes.Infraction) (math.Int, error) {
	call := slashCall{cons: consAddr, height: height, power: power, fraction: fraction, infraction: infraction}
	m.slashes = append(m.slashes, call)
	if m.slashFn != nil {
		return m.slashFn(ctx, call)
	}
	return math.ZeroInt(), nil
}

func (m *mockStakingKeeper) ConsensusAddressCodec() address.Codec { return m.consCdc }

func (m *mockStakingKeeper) TokensFromConsensusPower(_ context.Context, power int64) math.Int {
	return sdk.TokensFromConsensusPower(power, sdk.DefaultPowerReduction)
}

// mockEvidence is x/evidence's store as the compliance keeper reads it: the
// hashes (raw bytes as a string) it accepted.
type mockEvidence struct {
	hashes map[string]bool
}

func (m *mockEvidence) HasEquivocation(_ context.Context, hash []byte) (bool, error) {
	return m.hashes[string(hash)], nil
}

// mockEntities is the entity registry as the compliance keeper reads it: the
// entity of a validator at a height, by (operator, height); calls counts the
// EntityAt lookups.
type mockEntities struct {
	at    map[string]string
	calls int
	// absent lists entity ids HasEntity denies; every other id exists.
	absent map[string]bool
}

func (m *mockEntities) EntityAt(_ context.Context, val sdk.ValAddress, height int64) (string, bool, error) {
	m.calls++
	id, ok := m.at[entityKey(val, height)]
	return id, ok, nil
}

func (m *mockEntities) HasEntity(_ context.Context, id string) (bool, error) {
	return !m.absent[id], nil
}

func entityKey(val sdk.ValAddress, height int64) string {
	return fmt.Sprintf("%s@%d", val.String(), height)
}

// mockWindow records the slash window calls; closeErr makes the close fail.
type mockWindow struct {
	opened, closed int
	closeErr       error
}

func (m *mockWindow) OpenBeginBlockWindow(_ context.Context) error { m.opened++; return nil }
func (m *mockWindow) CloseBeginBlockWindow(_ context.Context) error {
	m.closed++
	return m.closeErr
}

func (m *mockStakingKeeper) GetValidator(_ context.Context, _ sdk.ValAddress) (stakingtypes.Validator, error) {
	if !m.found {
		return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
	}
	return m.val, nil
}

// mockSlashingKeeper reports a configurable tombstone state and the chain's
// double-sign fraction d (the harness default is 0.10).
type mockSlashingKeeper struct {
	tombstoned bool
	d          math.LegacyDec

	// calls counts IsTombstoned calls; with falseAfter > 0 the answer turns false
	// once calls exceeds it (a state that V-15 passes and the plan then disputes).
	calls      int
	falseAfter int
	// notTombstoned lists consensus addresses that are never tombstoned.
	notTombstoned map[string]bool
}

func (m *mockSlashingKeeper) SlashFractionDoubleSign(_ context.Context) (math.LegacyDec, error) {
	return m.d, nil
}

func (m *mockSlashingKeeper) IsTombstoned(_ context.Context, consAddr sdk.ConsAddress) bool {
	m.calls++
	if m.notTombstoned[string(consAddr)] {
		return false
	}
	if m.falseAfter > 0 && m.calls > m.falseAfter {
		return false
	}
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

// SetComplianceFlag fails for a validator without a record, as the registry's
// own SetComplianceFlag does.
func (m *mockVRKeeper) SetComplianceFlag(_ context.Context, addr sdk.ValAddress, compliant bool) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.statuses[addr.String()]; !ok {
		return errorsmod.Wrapf(vrtypes.ErrValidatorNotFound, "validator %s", addr.String())
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
	// epoch is the epoch the last ZeroReputation call named.
	epoch uint64
}

func (m *mockParticipationKeeper) ZeroReputation(_ context.Context, addr sdk.ValAddress, epoch uint64) error {
	if m.zeroErr != nil {
		return m.zeroErr
	}
	m.zeroed[addr.String()] = true
	m.epoch = epoch
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
	evidence      *mockEvidence
	entities      *mockEntities
	window        *mockWindow
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
	stakingK := &mockStakingKeeper{
		found:    true,
		val:      testValidator(t),
		byCons:   map[string]stakingtypes.Validator{},
		failCons: map[string]bool{},
		consCdc:  addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32ConsensusAddrPrefix()),
	}
	evidenceK := &mockEvidence{hashes: map[string]bool{}}
	entitiesK := &mockEntities{at: map[string]string{}}
	windowK := &mockWindow{}
	slashingK := &mockSlashingKeeper{d: math.LegacyNewDecWithPrec(10, 2)}
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
		evidenceK,
		entitiesK,
		windowK,
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
		evidence:      evidenceK,
		entities:      entitiesK,
		window:        windowK,
	}
}
