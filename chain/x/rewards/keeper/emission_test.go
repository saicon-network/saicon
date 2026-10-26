package keeper_test

import (
	"context"

	"cosmossdk.io/core/address"
	"errors"
	"testing"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
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

// fakeValidator embeds ValidatorI (nil) and overrides only GetOperator, the
// single method DistributeEpochRewards uses. Any other call would panic, which
// is acceptable for these focused tests.
type fakeValidator struct {
	stakingtypes.ValidatorI
	operator string
}

func (f fakeValidator) GetOperator() string { return f.operator }

// --- mock staking keeper --------------------------------------------------

type mockStakingKeeper struct {
	validators  []stakingtypes.ValidatorI
	totalBonded math.Int // stake-linked term of the security budget; nil => zero
}

func (m mockStakingKeeper) TotalBondedTokens(_ context.Context) (math.Int, error) {
	if m.totalBonded.IsNil() {
		return math.ZeroInt(), nil
	}
	return m.totalBonded, nil
}

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
	sendErr error    // when non-nil, SendCoinsFromModuleToAccount fails — exercises the abort branch
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
	recipient sdk.AccAddress
	amt       sdk.Coins
}

func (m *mockBankKeeper) MintCoins(_ context.Context, _ string, amt sdk.Coins) error {
	if m.mintErr != nil {
		return m.mintErr
	}
	m.minted = append(m.minted, amt)
	// Minting raises supply, which shrinks the cap headroom for the next epoch.
	// Modelling that is what makes the 21M ceiling testable across epochs.
	if m.supply.IsNil() {
		m.supply = math.ZeroInt()
	}
	m.supply = m.supply.Add(amt.AmountOf(types.RewardDenom))
	return nil
}

func (m *mockBankKeeper) SendCoinsFromModuleToAccount(_ context.Context, _ string, recipient sdk.AccAddress, amt sdk.Coins) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.sends = append(m.sends, sendRecord{recipient: recipient, amt: amt})
	return nil
}

// --- mock participation keeper (EP) ---------------------------------------

type mockParticipationKeeper struct {
	ep          map[string]math.Int // valoper string -> accumulated EP; absent key => not found
	epochLen    uint64              // epoch length returned by EpochLengthBlocks
	epochLenErr error               // when non-nil, EpochLengthBlocks fails
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
	ctx    context.Context
	keeper keeper.Keeper
	bank   *mockBankKeeper
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
	)

	// Normalise the year so the budget divides exactly into the fixture's
	// 100-block epochs (1,000 epochs/year). Callers pass whatever params their
	// case needs; only the block count is pinned, and only to keep the expected
	// amounts readable.
	params.BlocksPerYear = 100_000
	require.NoError(t, k.Params.Set(ctx, params))

	return &emissionFixture{ctx: ctx, keeper: k, bank: bank}
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
	require.Len(t, f.bank.sends, 1)
	require.Equal(t, sdk.AccAddress(va.Bytes()), f.bank.sends[0].recipient)
	require.True(t, f.bank.sends[0].amt.Equal(expected))

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

// SendCoinsFromModuleToAccount failure aborts the epoch the same way.
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
	require.Equal(t, sdk.AccAddress(good.Bytes()), f.bank.sends[0].recipient)

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
