package keeper_test

import (
	"context"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
	"github.com/saicon-network/saicon/chain/x/rewards/keeper"
	module "github.com/saicon-network/saicon/chain/x/rewards/module"
	"github.com/saicon-network/saicon/chain/x/rewards/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// previewValidator is a bonded validator with tokens: the preview reads its
// operator, its bonded tokens and its CometBFT power.
type previewValidator struct {
	stakingtypes.ValidatorI
	operator string
	tokens   math.Int
}

func (v previewValidator) GetOperator() string       { return v.operator }
func (v previewValidator) GetBondedTokens() math.Int { return v.tokens }
func (v previewValidator) GetConsensusPower(r math.Int) int64 {
	return sdk.TokensToConsensusPower(v.tokens, r)
}

type mockStakeengineKeeper struct {
	cap math.Int
	err error
}

func (m mockStakeengineKeeper) ValidatorCap(context.Context) (math.Int, error) {
	return m.cap, m.err
}

// power is n units of CometBFT power, in nscn.
func power(n int64) math.Int { return sdk.TokensFromConsensusPower(n, sdk.DefaultPowerReduction) }

type previewVal struct {
	addr      sdk.ValAddress
	tokens    math.Int
	warp      string // "" => no WARP stored yet
	compliant bool
	status    vrtypes.Status
}

func previewKeeper(t *testing.T, height int64, cap math.Int, capErr error, vals []previewVal, opts ...func(*mockParticipationKeeper)) (context.Context, keeper.Keeper) {
	t.Helper()
	sk := mockStakingKeeper{}
	pk := mockParticipationKeeper{epochLen: 10, warps: map[string]math.LegacyDec{}, ep: map[string]math.Int{}}
	for _, o := range opts {
		o(&pk)
	}
	vr := mockVRKeeper{statuses: map[string]vrtypes.Status{}}
	cp := mockComplianceKeeper{records: map[string]bool{}}
	for _, v := range vals {
		sk.validators = append(sk.validators, previewValidator{operator: v.addr.String(), tokens: v.tokens})
		// Every validator has EP unless an option set it: the EP gate has a
		// test of its own.
		if _, set := pk.ep[v.addr.String()]; !set {
			pk.ep[v.addr.String()] = math.NewInt(1)
		}
		if v.warp != "" {
			pk.warps[v.addr.String()] = math.LegacyMustNewDecFromStr(v.warp)
		}
		if v.status != vrtypes.Status_STATUS_UNSPECIFIED {
			vr.statuses[v.addr.String()] = v.status
		}
		if !v.compliant {
			cp.records[v.addr.String()] = false
		}
	}

	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_test")).Ctx.WithBlockHeight(height)
	k := keeper.NewKeeper(
		runtime.NewKVStoreService(storeKey),
		encCfg.Codec,
		addressCodec,
		authtypes.NewModuleAddress(types.GovModuleName),
		mockAuthKeeper{addressCodec: addressCodec},
		&mockBankKeeper{},
		sk, pk, vr, cp,
		mockStakeengineKeeper{cap: cap, err: capErr},
		nil, // distributionKeeper: the preview never pays
	)
	return ctx, k
}

// withEP sets a validator's accumulated EP in the preview fixture.
func withEP(addr sdk.ValAddress, ep math.Int) func(*mockParticipationKeeper) {
	return func(pk *mockParticipationKeeper) { pk.ep[addr.String()] = ep }
}

func previewAddr(tag string) sdk.ValAddress {
	return sdk.ValAddress([]byte("preview-val-" + tag + "-padding-bytes"))[:20]
}

// The formula, every gate and the shares on one set. Numbers recomputed with
// exact fractions outside Go:
//
//	cap 1000 power; A 3000 @90 -> 900, B 500 @82 -> 410, C 800 unmeasured @80
//	-> 640; total_vp 1950; D non-compliant, E @64.9 below 65, F not active.
func TestVotingPowerPreview_FormulaGatesAndShares(t *testing.T) {
	a, b, c := previewAddr("a"), previewAddr("b"), previewAddr("c")
	d, e, f := previewAddr("d"), previewAddr("e"), previewAddr("f")
	active := vrtypes.Status_STATUS_ACTIVE
	ctx, k := previewKeeper(t, 25, power(1000), nil, []previewVal{
		{a, power(3000), "90", true, active},
		{b, power(500), "82", true, active},
		{c, power(800), "", true, active},
		{f, power(400), "100", false, vrtypes.Status_STATUS_QUARANTINED},
		{d, power(700), "95", false, active},
		{e, power(600), "64.9", true, active},
	})

	res, err := keeper.NewQueryServerImpl(k).VotingPowerPreview(ctx, &types.QueryVotingPowerPreviewRequest{})
	require.NoError(t, err)

	require.Equal(t, int64(25), res.Height)
	require.Equal(t, uint64(2), res.Epoch, "25 / 10: epoch 2 is the last closed")
	require.Equal(t, math.LegacyNewDec(65), res.Params.WarpThreshold)
	require.Equal(t, math.LegacyNewDec(80), res.Params.WarpInitial)
	require.Equal(t, power(1000), res.Params.ValidatorCap)
	require.Equal(t, math.NewInt(1_950_000_000), res.TotalVp)
	require.Equal(t, int64(6000), res.TotalStockPower)

	type row struct {
		addr                sdk.ValAddress
		capped, vp          int64
		warp                string
		measured, compliant bool
		status              string
		vpShare, stockShare string
		stock               int64
		eligible            bool
		reason              string
	}
	want := []row{
		{a, 1_000_000_000, 900_000_000, "90", true, true, "STATUS_ACTIVE", "0.461538", "0.500000", 3000, true, ""},
		{c, 800_000_000, 640_000_000, "80", false, true, "STATUS_ACTIVE", "0.328205", "0.133333", 800, true, ""},
		{b, 500_000_000, 410_000_000, "82", true, true, "STATUS_ACTIVE", "0.210256", "0.083333", 500, true, ""},
		{e, 600_000_000, 389_400_000, "64.9", true, true, "STATUS_ACTIVE", "0", "0.100000", 600, false, "warp below threshold"},
		// D and F tie at 0: the operator_address strings decide, not the
		// iteration order.
		{d, 700_000_000, 0, "95", true, false, "STATUS_ACTIVE", "0", "0.116666", 700, false, "non-compliant"},
		{f, 400_000_000, 0, "100", true, false, "STATUS_QUARANTINED", "0", "0.066666", 400, false, "not active"},
	}
	require.Len(t, res.Validators, len(want))
	for i, w := range want {
		got := res.Validators[i]
		require.Equal(t, w.addr.String(), got.OperatorAddress, "row %d", i)
		require.Equal(t, math.NewInt(w.capped), got.CappedBonded, "capped_bonded of %d", i)
		require.Equal(t, math.NewInt(w.vp), got.Vp, "vp of %d", i)
		require.Equal(t, math.LegacyMustNewDecFromStr(w.warp), got.Warp, "warp of %d", i)
		require.Equal(t, w.measured, got.WarpMeasured, "warp_measured of %d", i)
		require.Equal(t, w.compliant, got.Compliant, "compliant of %d", i)
		require.Equal(t, w.status, got.Status, "status of %d", i)
		require.Equal(t, w.vpShare, got.VpShare, "vp_share of %d", i)
		require.Equal(t, w.stockShare, got.StockShare, "stock_share of %d", i)
		require.Equal(t, w.stock, got.StockPower, "stock_power of %d", i)
		require.Equal(t, w.eligible, got.Eligible, "eligible of %d", i)
		require.Equal(t, w.reason, got.Reason, "reason of %d", i)
	}
	require.Equal(t, power(3000), res.Validators[0].Bonded, "bonded is reported before the cap")
}

// Shares truncate, never round: three equal validators get 0.333333 each.
func TestVotingPowerPreview_SharesTruncate(t *testing.T) {
	active := vrtypes.Status_STATUS_ACTIVE
	ctx, k := previewKeeper(t, 10, power(1000), nil, []previewVal{
		{previewAddr("x"), power(100), "70", true, active},
		{previewAddr("y"), power(100), "70", true, active},
		{previewAddr("z"), power(100), "70", true, active},
	})
	res, err := k.VotingPowerPreview(ctx)
	require.NoError(t, err)
	for _, v := range res.Validators {
		require.Equal(t, "0.333333", v.VpShare)
		require.Equal(t, "0.333333", v.StockShare)
	}
}

// The floor is exact: 999 · 33.333333333333333333 / 100 = 332.99999…, so 332,
// where a rounding Dec would say 333.
func TestVotingPowerPreview_VPIsFloored(t *testing.T) {
	ctx, k := previewKeeper(t, 10, math.NewInt(1_000_000), nil, []previewVal{
		{previewAddr("q"), math.NewInt(999), "33.333333333333333333", true, vrtypes.Status_STATUS_ACTIVE},
	})
	res, err := k.VotingPowerPreview(ctx)
	require.NoError(t, err)
	require.Equal(t, math.NewInt(332), res.Validators[0].Vp)
	// Below the threshold of 65, so not eligible: the vp is still shown.
	require.Equal(t, "warp below threshold", res.Validators[0].Reason)
	require.Equal(t, "0", res.Validators[0].VpShare)
}

// No bonded validators: an empty list and zero totals, not an error.
func TestVotingPowerPreview_EmptySet(t *testing.T) {
	ctx, k := previewKeeper(t, 7, power(1000), nil, nil)
	res, err := k.VotingPowerPreview(ctx)
	require.NoError(t, err)
	require.Empty(t, res.Validators)
	require.True(t, res.TotalVp.IsZero())
	require.Zero(t, res.TotalStockPower)
	require.Zero(t, res.Epoch, "no epoch has closed at height 7")
}

// An eligible set whose vp is all zero (a zero cap) divides nothing.
func TestVotingPowerPreview_ZeroTotalVP(t *testing.T) {
	ctx, k := previewKeeper(t, 10, math.ZeroInt(), nil, []previewVal{
		{previewAddr("w"), power(100), "90", true, vrtypes.Status_STATUS_ACTIVE},
	})
	res, err := k.VotingPowerPreview(ctx)
	require.NoError(t, err)
	require.True(t, res.Validators[0].Eligible)
	require.Equal(t, "0.000000", res.Validators[0].VpShare)
	require.Equal(t, "1.000000", res.Validators[0].StockShare)
}

func TestVotingPowerPreview_Errors(t *testing.T) {
	ctx, k := previewKeeper(t, 10, math.Int{}, stakeenginetypes.ErrCapsNotComputed, nil)
	_, err := keeper.NewQueryServerImpl(k).VotingPowerPreview(ctx, &types.QueryVotingPowerPreviewRequest{})
	require.Equal(t, codes.NotFound, status.Code(err), "caps not computed answers like x/stakeengine's own query")

	_, err = keeper.NewQueryServerImpl(k).VotingPowerPreview(ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// WARP params that do not pass WarpParams.Validate are the ones the epoch
	// step refuses too: the preview refuses rather than show a WARP that is not
	// moving. Nil is a store that never ran its 1 -> 2 migration; zeros are
	// params stored from a genesis without WARP fields, which read back as
	// zeros, not nils; the third breaks R8's asymmetry.
	zero := math.LegacyZeroDec()
	symmetric := participationtypes.DefaultParams().Warp()
	symmetric.Rise = symmetric.Fall
	for name, wp := range map[string]participationtypes.WarpParams{
		"nil":       {},
		"zeros":     {Fall: zero, Rise: zero, Initial: zero, Threshold: zero},
		"symmetric": symmetric,
	} {
		ctx, k = previewKeeper(t, 10, power(1000), nil, []previewVal{
			{previewAddr("p"), power(100), "90", true, vrtypes.Status_STATUS_ACTIVE},
		}, func(pk *mockParticipationKeeper) {
			pk.warpParams = &wp
		})
		_, err = keeper.NewQueryServerImpl(k).VotingPowerPreview(ctx, &types.QueryVotingPowerPreviewRequest{})
		require.Equal(t, codes.FailedPrecondition, status.Code(err), "%s: %v", name, err)
		require.ErrorContains(t, err, "x/participation WARP parameters do not validate", name)
	}
}

// warp >= threshold is eligible, to the tick: 65 is in, 65 - 1e-18 is out.
func TestVotingPowerPreview_ThresholdIsInclusive(t *testing.T) {
	at, below := previewAddr("at"), previewAddr("below")
	active := vrtypes.Status_STATUS_ACTIVE
	ctx, k := previewKeeper(t, 10, power(1000), nil, []previewVal{
		{at, power(100), "65", true, active},
		{below, power(100), "64.999999999999999999", true, active},
	})
	res, err := k.VotingPowerPreview(ctx)
	require.NoError(t, err)
	require.Len(t, res.Validators, 2)
	byAddr := map[string]types.VotingPowerPreviewEntry{}
	for _, e := range res.Validators {
		byAddr[e.OperatorAddress] = e
	}
	require.True(t, byAddr[at.String()].Eligible, byAddr[at.String()].Reason)
	require.Equal(t, "1.000000", byAddr[at.String()].VpShare)
	require.False(t, byAddr[below.String()].Eligible)
	require.Equal(t, "warp below threshold", byAddr[below.String()].Reason)
	require.Equal(t, "0", byAddr[below.String()].VpShare)
	require.Equal(t, math.NewInt(65_000_000), res.TotalVp, "only the validator at the threshold counts")
}

// Ties are broken by operator_address ascending AS A STRING, which is what a
// reader sees. bech32's alphabet is not in ASCII order, so byte order would
// disagree: here lo < hi as bytes, while hi's string sorts first.
func TestVotingPowerPreview_TiesByOperatorString(t *testing.T) {
	lo, hi := make([]byte, 20), make([]byte, 20)
	lo[0] = 0x08 // first 5-bit group 1 -> 'p'
	hi[0] = 0x28 // first 5-bit group 5 -> '9'
	a, b := sdk.ValAddress(lo), sdk.ValAddress(hi)
	require.Less(t, string(a), string(b), "lo < hi as bytes")
	require.Less(t, b.String(), a.String(), "hi < lo as strings")

	active := vrtypes.Status_STATUS_ACTIVE
	for _, order := range [][]previewVal{
		{{a, power(100), "90", true, active}, {b, power(100), "90", true, active}},
		{{b, power(100), "90", true, active}, {a, power(100), "90", true, active}},
	} {
		ctx, k := previewKeeper(t, 10, power(1000), nil, order)
		res, err := k.VotingPowerPreview(ctx)
		require.NoError(t, err)
		require.Len(t, res.Validators, 2)
		require.Equal(t, res.Validators[0].Vp, res.Validators[1].Vp)
		require.Equal(t, b.String(), res.Validators[0].OperatorAddress)
		require.Equal(t, a.String(), res.Validators[1].OperatorAddress)
	}
}

// A validator punished before its first scored epoch has R7's zero row,
// last_epoch 0. The preview shows the stored 0, not warp_initial, and calls it
// unmeasured: the row is a punishment, not a measurement. A row with a
// measured epoch is measured, whatever its value.
func TestVotingPowerPreview_ZeroRowIsShownUnmeasured(t *testing.T) {
	punished, scored, fresh := previewAddr("punished"), previewAddr("scored"), previewAddr("fresh")
	ctx, k := previewKeeper(t, 10, power(1000), nil, []previewVal{
		{punished, power(100), "0", true, vrtypes.Status_STATUS_ACTIVE},
		{scored, power(100), "0", true, vrtypes.Status_STATUS_ACTIVE},
		{fresh, power(100), "", true, vrtypes.Status_STATUS_ACTIVE},
	}, func(pk *mockParticipationKeeper) {
		pk.lastEpochs = map[string]uint64{punished.String(): 0, scored.String(): 3}
	})
	res, err := k.VotingPowerPreview(ctx)
	require.NoError(t, err)
	byAddr := map[string]types.VotingPowerPreviewEntry{}
	for _, e := range res.Validators {
		byAddr[e.OperatorAddress] = e
	}

	p := byAddr[punished.String()]
	require.True(t, p.Warp.IsZero(), "the stored 0, not warp_initial")
	require.False(t, p.WarpMeasured)
	require.True(t, p.Vp.IsZero())
	require.Equal(t, "warp below threshold", p.Reason)

	s := byAddr[scored.String()]
	require.True(t, s.Warp.IsZero())
	require.True(t, s.WarpMeasured)

	f := byAddr[fresh.String()]
	require.Equal(t, math.LegacyNewDec(80), f.Warp, "no row: warp_initial")
	require.False(t, f.WarpMeasured)
	require.True(t, f.Eligible)
}

// EP 0 fails the reward gate, so it fails the preview too: a validator that
// passes every other condition, WARP included, is shown not eligible with the
// reward gate's own reason, and the payout denies it for the same reason. The
// preview and the payout read one gate (rewardGate), so they cannot disagree.
func TestVotingPowerPreview_EPZeroIsNotEligible(t *testing.T) {
	zero := previewAddr("ep-zero")
	ctx, k := previewKeeper(t, 25, power(1000), nil, []previewVal{
		{zero, power(500), "90", true, vrtypes.Status_STATUS_ACTIVE},
	}, withEP(zero, math.ZeroInt()))

	res, err := k.VotingPowerPreview(ctx)
	require.NoError(t, err)
	require.Len(t, res.Validators, 1)
	got := res.Validators[0]
	require.False(t, got.Eligible)
	require.Equal(t, "EP=0", got.Reason)
	require.Equal(t, "0", got.VpShare)
	require.True(t, got.Compliant)
	require.Equal(t, "STATUS_ACTIVE", got.Status)
	require.Equal(t, math.NewInt(450_000_000), got.Vp, "vp is still shown, as for any ineligible validator")
	require.True(t, res.TotalVp.IsZero(), "an ineligible validator adds nothing to total_vp")

	// The reward gate, on the same keeper and the same state.
	params := registerV1Params()
	params.BlocksPerYear = 100_000
	require.NoError(t, k.Params.Set(ctx, params))
	require.NoError(t, k.DistributeEpochRewards(ctx, testEpoch))
	snap, found, err := k.GetRewardSnapshot(ctx, testEpoch, zero)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, snap.Eligible)
	require.Equal(t, got.Reason, snap.Reason)
}
