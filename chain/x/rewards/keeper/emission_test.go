package keeper_test

import (
	"context"

	"cosmossdk.io/core/address"
	"errors"
	"fmt"
	"testing"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	compliancetypes "github.com/saicon-network/saicon/chain/x/compliance/types"
	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
	"github.com/saicon-network/saicon/chain/x/rewards/keeper"
	module "github.com/saicon-network/saicon/chain/x/rewards/module"
	"github.com/saicon-network/saicon/chain/x/rewards/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// --- mock validator -------------------------------------------------------

// fakeValidator embeds ValidatorI (nil) and overrides only the methods
// DistributeEpochRewards uses: GetOperator, and GetTokens for the payout leg's
// allocatable check. Any other call would panic, which is acceptable for these
// focused tests.
type fakeValidator struct {
	stakingtypes.ValidatorI
	operator string
	tokens   math.Int // nil => a positive stake; zero is the not-allocatable case (S10 R3)
}

func (f fakeValidator) GetOperator() string { return f.operator }

func (f fakeValidator) GetTokens() math.Int {
	if f.tokens.IsNil() {
		return math.NewInt(1_000_000)
	}
	return f.tokens
}

// --- mock staking keeper --------------------------------------------------

type mockStakingKeeper struct {
	validators  []stakingtypes.ValidatorI
	totalBonded math.Int // stake-linked term of the security budget; nil => zero
	// gone lists operators the bonded iteration yields but Validator does not
	// find: the not-found case of S10 R3.
	gone map[string]bool
	// getErr, when non-nil, makes Validator fail with an error that does not
	// mean not-found, which must abort the epoch.
	getErr error
	// getErrFor, when set, limits getErr to that operator, so the payout loop
	// can fail after an earlier validator was already paid.
	getErrFor string
}

// Validator answers the payout leg's lookup the way x/staking does: the
// stored validator, or a zero Validator with ErrNoValidatorFound.
func (m mockStakingKeeper) Validator(_ context.Context, addr sdk.ValAddress) (stakingtypes.ValidatorI, error) {
	if m.getErr != nil && (m.getErrFor == "" || m.getErrFor == addr.String()) {
		return nil, m.getErr
	}
	if !m.gone[addr.String()] {
		for _, v := range m.validators {
			if v.GetOperator() == addr.String() {
				return v, nil
			}
		}
	}
	return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
}

func (m mockStakingKeeper) TotalBondedTokens(_ context.Context) (math.Int, error) {
	if m.totalBonded.IsNil() {
		return math.ZeroInt(), nil
	}
	return m.totalBonded, nil
}

func (m mockStakingKeeper) PowerReduction(context.Context) math.Int { return sdk.DefaultPowerReduction }

func (m mockStakingKeeper) IterateBondedValidatorsByPower(_ context.Context, fn func(index int64, validator stakingtypes.ValidatorI) (stop bool)) error {
	for i, v := range m.validators {
		if fn(int64(i), v) {
			break
		}
	}
	return nil
}

// mockAuthKeeper resolves the fee collector for the gas-fee offset. The address
// is arbitrary but stable — what matters is that the keeper asks for the fee
// collector by name and reads THAT account's balance.
type mockAuthKeeper struct{ addressCodec address.Codec }

func (m mockAuthKeeper) AddressCodec() address.Codec                             { return m.addressCodec }
func (m mockAuthKeeper) GetAccount(context.Context, sdk.AccAddress) sdk.AccountI { return nil }
func (m mockAuthKeeper) GetModuleAddress(name string) sdk.AccAddress {
	return authtypes.NewModuleAddress(name)
}

// --- mock bank keeper (records mints + sends) -----------------------------

type mockBankKeeper struct {
	minted  []sdk.Coins
	sends   []sendRecord
	supply  math.Int // current nscn supply the cap check reads; zero value => zero supply
	mintErr error    // when non-nil, MintCoins fails (records nothing) — exercises the abort branch
	sendErr error    // when non-nil, SendCoinsFromModuleToModule fails — exercises the abort branch
	// modules holds module-account nscn balances: a mint credits the minting
	// module, a send moves the amount between modules. It is what lets a test
	// read "rewards module balance before minus after" (S10 R4).
	modules map[string]math.Int
	// ledger, when set, is a store each mint and send also writes a marker
	// into, keyed by the module. Unlike the slices above it lives in the
	// context's multistore, so a test can see whether a mint or a send
	// survived a discarded cache context.
	ledger storetypes.StoreKey
}

// sendMarker is the ledger key a send to module writes.
func sendMarker(module string) []byte { return []byte("test/sent-to/" + module) }

// mintMarker is the ledger key a mint into module writes.
func mintMarker(module string) []byte { return []byte("test/minted-to/" + module) }

func (m *mockBankKeeper) moduleBalance(name string) math.Int {
	if v, ok := m.modules[name]; ok {
		return v
	}
	return math.ZeroInt()
}

func (m *mockBankKeeper) credit(name string, amt math.Int) {
	if m.modules == nil {
		m.modules = map[string]math.Int{}
	}
	m.modules[name] = m.moduleBalance(name).Add(amt)
}

// GetBalance answers the fee-collector read. The fixtures drive the offset
// through AddObservedGasFees directly, so an empty collector is right here.
func (m *mockBankKeeper) GetBalance(_ context.Context, _ sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewCoin(denom, math.ZeroInt())
}

func (m *mockBankKeeper) GetSupply(_ context.Context, denom string) sdk.Coin {
	if m.supply.IsNil() {
		return sdk.NewCoin(denom, math.ZeroInt())
	}
	return sdk.NewCoin(denom, m.supply)
}

type sendRecord struct {
	from, to string // module names
	amt      sdk.Coins
}

func (m *mockBankKeeper) MintCoins(ctx context.Context, module string, amt sdk.Coins) error {
	if m.mintErr != nil {
		return m.mintErr
	}
	if m.ledger != nil {
		sdk.UnwrapSDKContext(ctx).KVStore(m.ledger).Set(mintMarker(module), []byte(amt.String()))
	}
	m.minted = append(m.minted, amt)
	m.credit(module, amt.AmountOf(types.RewardDenom))
	// Minting raises supply, which shrinks the cap headroom for the next epoch.
	// Modelling that is what makes the 21M ceiling testable across epochs.
	if m.supply.IsNil() {
		m.supply = math.ZeroInt()
	}
	m.supply = m.supply.Add(amt.AmountOf(types.RewardDenom))
	return nil
}

func (m *mockBankKeeper) SendCoinsFromModuleToModule(ctx context.Context, from, to string, amt sdk.Coins) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	n := amt.AmountOf(types.RewardDenom)
	if m.moduleBalance(from).LT(n) {
		return fmt.Errorf("mock bank: module %s holds %s, cannot send %s", from, m.moduleBalance(from), n)
	}
	m.sends = append(m.sends, sendRecord{from: from, to: to, amt: amt})
	m.credit(from, n.Neg())
	m.credit(to, n)
	if m.ledger != nil {
		sdk.UnwrapSDKContext(ctx).KVStore(m.ledger).Set(sendMarker(to), []byte(amt.String()))
	}
	return nil
}

// --- mock distribution keeper (records allocations) -----------------------

type allocRecord struct {
	val    stakingtypes.ValidatorI
	tokens sdk.DecCoins
}

type mockDistributionKeeper struct {
	allocs   []allocRecord
	allocErr error // when non-nil, AllocateTokensToValidator fails — exercises the abort branch
}

func (m *mockDistributionKeeper) AllocateTokensToValidator(_ context.Context, val stakingtypes.ValidatorI, tokens sdk.DecCoins) error {
	if m.allocErr != nil {
		return m.allocErr
	}
	m.allocs = append(m.allocs, allocRecord{val: val, tokens: tokens})
	return nil
}

// --- mock participation keeper (EP) ---------------------------------------

type mockParticipationKeeper struct {
	ep          map[string]math.Int // valoper string -> accumulated EP; absent key => not found
	epochLen    uint64              // epoch length returned by EpochLengthBlocks
	epochLenErr error               // when non-nil, EpochLengthBlocks fails
	warps       map[string]math.LegacyDec
	lastEpochs  map[string]uint64              // a WARP row's last_epoch; absent => 1 (measured)
	warpParams  *participationtypes.WarpParams // nil => the participation defaults
}

func (m mockParticipationKeeper) GetWarp(_ context.Context, addr sdk.ValAddress) (participationtypes.WarpState, bool, error) {
	v, ok := m.warps[addr.String()]
	if !ok {
		return participationtypes.WarpState{}, false, nil
	}
	last, set := m.lastEpochs[addr.String()]
	if !set {
		last = 1
	}
	return participationtypes.WarpState{ValidatorAddress: addr.String(), Value: v, LastEpoch: last}, true, nil
}

func (m mockParticipationKeeper) WarpParams(context.Context) (participationtypes.WarpParams, error) {
	if m.warpParams != nil {
		return *m.warpParams, nil
	}
	return participationtypes.DefaultParams().Warp(), nil
}

func (m mockParticipationKeeper) EpochLengthBlocks(_ context.Context) (uint64, error) {
	if m.epochLenErr != nil {
		return 0, m.epochLenErr
	}
	return m.epochLen, nil
}

func (m mockParticipationKeeper) GetEP(_ context.Context, addr sdk.ValAddress) (participationtypes.EmpiricalPointsState, bool, error) {
	v, ok := m.ep[addr.String()]
	if !ok {
		return participationtypes.EmpiricalPointsState{}, false, nil
	}
	return participationtypes.EmpiricalPointsState{ValidatorAddress: addr.String(), AccumulatedEp: v}, true, nil
}

// --- mock validatorregistry keeper (status) -------------------------------

type mockVRKeeper struct {
	statuses map[string]vrtypes.Status // absent key => not found
}

func (m mockVRKeeper) GetValidatorStatus(_ context.Context, addr sdk.ValAddress) (vrtypes.Status, bool, error) {
	s, ok := m.statuses[addr.String()]
	if !ok {
		return vrtypes.Status_STATUS_UNSPECIFIED, false, nil
	}
	return s, true, nil
}

// --- mock compliance keeper (C flag) --------------------------------------

type mockComplianceKeeper struct {
	records map[string]bool // valoper string -> compliant; absent key => no record (=> compliant)
}

func (m mockComplianceKeeper) GetCompliance(_ context.Context, addr sdk.ValAddress) (compliancetypes.ComplianceState, bool, error) {
	c, ok := m.records[addr.String()]
	if !ok {
		return compliancetypes.ComplianceState{}, false, nil
	}
	return compliancetypes.ComplianceState{ValidatorAddress: addr.String(), Compliant: c}, true, nil
}

// --- emission fixture -----------------------------------------------------

type emissionFixture struct {
	ctx      context.Context
	keeper   keeper.Keeper
	bank     *mockBankKeeper
	distr    *mockDistributionKeeper
	storeKey storetypes.StoreKey
}

func initEmissionFixture(
	t *testing.T,
	sk types.StakingKeeper,
	pk types.ParticipationKeeper,
	vr types.ValidatorregistryKeeper,
	cp types.ComplianceKeeper,
	params types.Params,
) *emissionFixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)

	storeService := runtime.NewKVStoreService(storeKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_test")).Ctx

	authority := authtypes.NewModuleAddress(types.GovModuleName)

	bank := &mockBankKeeper{supply: testSupply}
	distr := &mockDistributionKeeper{}

	k := keeper.NewKeeper(
		storeService,
		encCfg.Codec,
		addressCodec,
		authority,
		mockAuthKeeper{addressCodec: addressCodec},
		bank,
		sk,
		pk,
		vr,
		cp,
		nil, // stakeengineKeeper: emission never reads it
		distr,
	)

	// Normalise the year so the budget divides exactly into the fixture's
	// 100-block epochs (1,000 epochs/year). Callers pass whatever params their
	// case needs; only the block count is pinned, and only to keep the expected
	// amounts readable.
	params.BlocksPerYear = 100_000
	require.NoError(t, k.Params.Set(ctx, params))

	return &emissionFixture{ctx: ctx, keeper: k, bank: bank, distr: distr, storeKey: storeKey}
}

// The emission fixtures use numbers chosen so the security budget divides
// exactly, keeping the assertions readable:
//
//	supply          1e15 nscn (10,000,000 SCN)
//	total bonded    4e13 nscn  -> 0.15 x bonded = 6e12, inside the clip band
//	                              (floor 0.005 x supply = 5e12, ceiling 7.5e12)
//	blocks_per_year 100,000 with 100-block epochs -> 1,000 epochs/year
//	                              => epoch pool = 6e12 / 1000 = 6e9 nscn
//
// The formula itself is pinned separately, against the Register's own headline
// figures, in budget_test.go.
var (
	testSupply = math.NewInt(1_000_000_000_000_000) // 1e15
	testBonded = math.NewInt(40_000_000_000_000)    // 4e13
	testPool   = math.NewInt(6_000_000_000)         // 6e9 — one epoch's budget
)

// valAddr builds a deterministic 20-byte valoper address from a single byte.
func valAddr(b byte) sdk.ValAddress {
	bz := make([]byte, 20)
	bz[0] = b
	return sdk.ValAddress(bz)
}

const testEpoch uint64 = 7

// fully eligible validator -> mint + send + snapshot eligible=true.
func TestDistributeEpochRewards_Eligible(t *testing.T) {
	va := valAddr(1)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.NewInt(5)}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	// One eligible validator takes the whole epoch budget. The flat
	// reward_per_epoch_amount is superseded by the security budget (ADR-0011 D1).
	expected := sdk.NewCoins(sdk.NewCoin(types.RewardDenom, testPool))
	require.Len(t, f.bank.minted, 1)
	require.True(t, f.bank.minted[0].Equal(expected))
	// The share goes to x/distribution, booked against this validator — never
	// to the operator account (S10 R1).
	require.Len(t, f.bank.sends, 1)
	require.Equal(t, types.ModuleName, f.bank.sends[0].from)
	require.Equal(t, distrtypes.ModuleName, f.bank.sends[0].to)
	require.True(t, f.bank.sends[0].amt.Equal(expected))
	require.Len(t, f.distr.allocs, 1)
	require.Equal(t, va.String(), f.distr.allocs[0].val.GetOperator())
	require.True(t, f.distr.allocs[0].tokens.Equal(sdk.NewDecCoinsFromCoins(expected...)))

	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, snap.Eligible)
	require.Equal(t, expected.String(), snap.Amount)
	require.Equal(t, "eligible", snap.Reason)
}

// MintCoins failure aborts the epoch: the error is returned to the caller and no
// positive-amount eligible snapshot is persisted (fail-closed for a money op).
func TestDistributeEpochRewards_MintFailureAborts(t *testing.T) {
	va := valAddr(20) // fully eligible: active, compliant, EP>0 -> reaches MintCoins
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.NewInt(5)}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())
	f.bank.mintErr = errors.New("mint boom")

	err := f.keeper.DistributeEpochRewards(f.ctx, testEpoch)
	require.Error(t, err)

	require.Empty(t, f.bank.minted)
	require.Empty(t, f.bank.sends)

	// No eligible snapshot with a positive amount was persisted: the epoch aborted
	// at MintCoins, before SetRewardSnapshot for this validator.
	snap, found, gErr := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, gErr)
	if found {
		require.False(t, snap.Eligible)
		require.Empty(t, snap.Amount)
	}
}

// A failed transfer to x/distribution aborts the epoch the same way.
func TestDistributeEpochRewards_SendFailureAborts(t *testing.T) {
	va := valAddr(21) // fully eligible -> reaches the send after a successful mint
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.NewInt(5)}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())
	f.bank.sendErr = errors.New("send boom")

	err := f.keeper.DistributeEpochRewards(f.ctx, testEpoch)
	require.Error(t, err)

	require.Empty(t, f.bank.sends)

	snap, found, gErr := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, gErr)
	if found {
		require.False(t, snap.Eligible)
		require.Empty(t, snap.Amount)
	}
}

// EP=0 -> no mint, snapshot ineligible with EP reason (foundation for V-2).
func TestDistributeEpochRewards_EPZero(t *testing.T) {
	va := valAddr(2)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.ZeroInt()}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	require.Empty(t, f.bank.minted)
	require.Empty(t, f.bank.sends)

	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, snap.Eligible)
	require.Empty(t, snap.Amount)
	require.Contains(t, snap.Reason, "EP")
}

// missing EP record -> treated as EP=0 -> no mint.
func TestDistributeEpochRewards_NoEPRecord(t *testing.T) {
	va := valAddr(3)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	require.Empty(t, f.bank.minted)
	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, snap.Eligible)
	require.Contains(t, snap.Reason, "EP")
}

// non-active status -> no mint, snapshot reason "not active".
func TestDistributeEpochRewards_NotActive(t *testing.T) {
	va := valAddr(4)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.NewInt(3)}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_QUARANTINED}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	require.Empty(t, f.bank.minted)
	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, snap.Eligible)
	require.Equal(t, "not active", snap.Reason)
}

// missing status record -> not found -> not active -> no mint.
func TestDistributeEpochRewards_NoStatusRecord(t *testing.T) {
	va := valAddr(5)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.NewInt(3)}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{}}
	cp := mockComplianceKeeper{records: map[string]bool{}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	require.Empty(t, f.bank.minted)
	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, snap.Eligible)
	require.Equal(t, "not active", snap.Reason)
}

// non-compliant (C=0) -> no mint, snapshot reason "non-compliant".
func TestDistributeEpochRewards_NonCompliant(t *testing.T) {
	va := valAddr(6)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.NewInt(3)}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): false}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	require.Empty(t, f.bank.minted)
	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, snap.Eligible)
	require.Equal(t, "non-compliant", snap.Reason)
}

// no compliance record -> treated as compliant -> eligible if other gates pass.
func TestDistributeEpochRewards_NoComplianceRecordIsCompliant(t *testing.T) {
	va := valAddr(7)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.NewInt(3)}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{}} // no record

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	require.Len(t, f.bank.minted, 1)
	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, snap.Eligible)
}

// params.Enabled=false -> no mints at all, even for eligible validators.
func TestDistributeEpochRewards_Disabled(t *testing.T) {
	va := valAddr(8)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.NewInt(3)}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, types.NewParams(types.DefaultRewardPerEpochAmount, false))
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	require.Empty(t, f.bank.minted)
	require.Empty(t, f.bank.sends)
	// A disabled epoch is still a DOCUMENTED epoch (ADR-0011 D4): the snapshot is
	// written with the reason, so a validator earning nothing can see why instead
	// of finding an empty epoch and suspecting an outage. This inverts the
	// original assertion, which recorded the earlier silent behaviour.
	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, snap.Eligible)
	require.Contains(t, snap.Reason, "disabled")
}

// multiple validators: only the eligible ones get minted; mixed reasons recorded.
func TestDistributeEpochRewards_MultipleValidators(t *testing.T) {
	good := valAddr(10) // eligible
	noEP := valAddr(11) // EP=0
	jail := valAddr(12) // not active

	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{
		fakeValidator{operator: good.String()},
		fakeValidator{operator: noEP.String()},
		fakeValidator{operator: jail.String()},
	}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{
		good.String(): math.NewInt(9),
		noEP.String(): math.ZeroInt(),
		jail.String(): math.NewInt(4),
	}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{
		good.String(): vrtypes.Status_STATUS_ACTIVE,
		noEP.String(): vrtypes.Status_STATUS_ACTIVE,
		jail.String(): vrtypes.Status_STATUS_JAILED,
	}}
	cp := mockComplianceKeeper{records: map[string]bool{}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	// exactly one mint (good).
	require.Len(t, f.bank.minted, 1)
	require.Len(t, f.bank.sends, 1)
	require.Len(t, f.distr.allocs, 1)
	require.Equal(t, good.String(), f.distr.allocs[0].val.GetOperator())

	goodSnap, _, _ := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, good)
	require.True(t, goodSnap.Eligible)

	noEPSnap, _, _ := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, noEP)
	require.False(t, noEPSnap.Eligible)
	require.Contains(t, noEPSnap.Reason, "EP")

	jailSnap, _, _ := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, jail)
	require.False(t, jailSnap.Eligible)
	require.Equal(t, "not active", jailSnap.Reason)
}

// TestDistributeEpochRewards_EmitsMintedEvent: an eligible validator (active,
// compliant, EP>0) must emit exactly one types.EventTypeRewardMinted event
// with the validator, epoch_id, and minted amount attributes.
func TestDistributeEpochRewards_EmitsMintedEvent(t *testing.T) {
	va := valAddr(1)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.NewInt(5)}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())

	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	require.NoError(t, f.keeper.DistributeEpochRewards(sdkCtx, 3))

	// One eligible validator takes the whole epoch budget. The flat
	// reward_per_epoch_amount is superseded by the security budget (ADR-0011 D1).
	expected := sdk.NewCoins(sdk.NewCoin(types.RewardDenom, testPool))

	events := sdkCtx.EventManager().Events()
	var matches []sdk.Event
	for _, e := range events {
		if e.Type == types.EventTypeRewardMinted {
			matches = append(matches, e)
		}
	}
	require.Len(t, matches, 1, "expected exactly one %s event", types.EventTypeRewardMinted)

	attrs := map[string]string{}
	for _, a := range matches[0].Attributes {
		attrs[a.Key] = a.Value
	}
	require.Equal(t, va.String(), attrs[types.AttributeKeyValidator])
	require.Equal(t, "3", attrs[types.AttributeKeyEpochID])
	require.Equal(t, expected.String(), attrs[types.AttributeKeyAmount])
}

// TestDistributeEpochRewards_EmitsDeniedEvent: a validator failing the gate
// (here EP=0) must emit exactly one types.EventTypeRewardDenied event with
// the validator, epoch_id, and reason "EP=0" attributes.
func TestDistributeEpochRewards_EmitsDeniedEvent(t *testing.T) {
	va := valAddr(2)
	sk := mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}}
	pk := mockParticipationKeeper{ep: map[string]math.Int{va.String(): math.ZeroInt()}, epochLen: 100}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{va.String(): vrtypes.Status_STATUS_ACTIVE}}
	cp := mockComplianceKeeper{records: map[string]bool{va.String(): true}}

	f := initEmissionFixture(t, sk, pk, vr, cp, registerV1Params())

	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	require.NoError(t, f.keeper.DistributeEpochRewards(sdkCtx, 3))

	events := sdkCtx.EventManager().Events()
	var matches []sdk.Event
	for _, e := range events {
		if e.Type == types.EventTypeRewardDenied {
			matches = append(matches, e)
		}
	}
	require.Len(t, matches, 1, "expected exactly one %s event", types.EventTypeRewardDenied)

	attrs := map[string]string{}
	for _, a := range matches[0].Attributes {
		attrs[a.Key] = a.Value
	}
	require.Equal(t, va.String(), attrs[types.AttributeKeyValidator])
	require.Equal(t, "3", attrs[types.AttributeKeyEpochID])
	require.Equal(t, "EP=0", attrs[types.AttributeKeyReason])
}
