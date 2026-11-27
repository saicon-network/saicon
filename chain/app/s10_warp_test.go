package app

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"

	compliancetypes "github.com/saicon-network/saicon/chain/x/compliance/types"
	participationkeeper "github.com/saicon-network/saicon/chain/x/participation/keeper"
	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
	rewardskeeper "github.com/saicon-network/saicon/chain/x/rewards/keeper"
	rewardstypes "github.com/saicon-network/saicon/chain/x/rewards/types"
	stakeenginekeeper "github.com/saicon-network/saicon/chain/x/stakeengine/keeper"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// x/rewards takes its keepers by concrete type under depinject, so these
// assertions are what turns a drifted interface into a compile error rather
// than a node that fails to start.
var (
	_ rewardstypes.StakeengineKeeper      = stakeenginekeeper.Keeper{}
	_ rewardstypes.StakingKeeper          = (*stakingkeeper.Keeper)(nil)
	_ rewardstypes.ParticipationKeeper    = participationkeeper.Keeper{}
	_ compliancetypes.ParticipationKeeper = participationkeeper.Keeper{}
)

// x/participation is at version 2 and its 1 -> 2 migration is registered with
// the app's configurator, so an upgrade handler's RunMigrations reaches it
// (ruling R9). The store is put back in its version 1 shape byte for byte: the
// params as v0.10 wrote them, with no WARP fields at all.
func TestParticipationMigration1to2IsRegistered(t *testing.T) {
	c := newTestChain(t)
	require.Equal(t, uint64(2), c.app.ModuleManager.GetVersionMap()[participationtypes.ModuleName])

	ctx := c.ctx()
	epDecay := math.LegacyMustNewDecFromStr("0.02")
	dec, err := epDecay.Marshal()
	require.NoError(t, err)
	var v1 []byte
	v1 = protowire.AppendTag(v1, 1, protowire.VarintType)
	v1 = protowire.AppendVarint(v1, 100)
	v1 = protowire.AppendTag(v1, 2, protowire.BytesType)
	v1 = protowire.AppendBytes(v1, dec)
	ctx.KVStore(c.app.GetKey(participationtypes.StoreKey)).Set(participationtypes.ParamsKey, v1)

	old, err := c.app.ParticipationKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.True(t, old.WarpThreshold.IsNil(), "the version 1 shape has no WARP parameters")

	vm := c.app.ModuleManager.GetVersionMap()
	vm[participationtypes.ModuleName] = 1
	after, err := c.app.ModuleManager.RunMigrations(ctx, c.app.Configurator(), vm)
	require.NoError(t, err)
	require.Equal(t, uint64(2), after[participationtypes.ModuleName])

	got, err := c.app.ParticipationKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.NoError(t, got.Validate())
	require.Equal(t, uint64(100), got.EpochLengthBlocks)
	require.True(t, epDecay.Equal(got.EpDecay))
	require.Equal(t, participationtypes.DefaultWarpFall(), got.WarpFall)
	require.Equal(t, participationtypes.DefaultWarpRise(), got.WarpRise)
	require.Equal(t, participationtypes.DefaultWarpInitial(), got.WarpInitial)
	require.Equal(t, participationtypes.DefaultWarpThreshold(), got.WarpThreshold)
}

// The whole path on the real app: two 5-block epochs at full uptime (the
// harness feeds no commit votes, so every validator gets the benefit of the
// doubt, uptime 1) take each validator's WARP 80 -> 82 -> 83.8, the snapshots
// pin it, V-16 holds, and the preview answers through the query router with
// that WARP while CometBFT power stays what x/staking says.
func TestWarpAndPreviewOnTheRealApp(t *testing.T) {
	c := newTestChain(t, withFastRounds())
	for c.height < 10 {
		c.nextBlock(time.Second)
	}
	ctx := c.ctx()
	pk := c.app.ParticipationKeeper

	for i, v := range c.vals {
		w, found, err := pk.GetWarp(ctx, v.val)
		require.NoError(t, err)
		require.True(t, found, "validator %d has no WARP after two epochs", i)
		require.Equal(t, math.LegacyMustNewDecFromStr("83.8"), w.Value, "validator %d", i)
		require.Equal(t, uint64(2), w.LastEpoch)
		for epoch, want := range map[uint64]string{1: "82", 2: "83.8"} {
			snap, found, err := pk.GetSnapshot(ctx, epoch, v.val)
			require.NoError(t, err)
			require.True(t, found)
			require.NotNil(t, snap.Warp)
			require.Equal(t, math.LegacyMustNewDecFromStr(want), *snap.Warp, "validator %d epoch %d", i, epoch)
		}
	}
	msg, broken := pk.WarpInvariant(ctx)
	require.False(t, broken, msg)

	require.NotNil(t, c.app.GRPCQueryRouter().Route("/chain.rewards.v1.Query/VotingPowerPreview"),
		"the preview must be reachable through the app's query router")
	require.NotNil(t, c.app.GRPCQueryRouter().Route("/chain.participation.v1.Query/Warps"))

	res, err := rewardskeeper.NewQueryServerImpl(c.app.RewardsKeeper).VotingPowerPreview(ctx, &rewardstypes.QueryVotingPowerPreviewRequest{})
	require.NoError(t, err)
	require.Equal(t, uint64(2), res.Epoch)
	require.Len(t, res.Validators, len(c.vals))
	valCap, err := c.app.StakeengineKeeper.ValidatorCap(ctx)
	require.NoError(t, err)
	require.Equal(t, valCap, res.Params.ValidatorCap)

	pr := c.app.StakingKeeper.PowerReduction(ctx)
	sumVP := math.ZeroInt()
	var sumStock int64
	for _, e := range res.Validators {
		require.True(t, e.Eligible, "%s: %s", e.OperatorAddress, e.Reason)
		require.True(t, e.WarpMeasured)
		require.Equal(t, math.LegacyMustNewDecFromStr("83.8"), e.Warp)
		require.Equal(t, "STATUS_ACTIVE", e.Status)

		valAddr, err := sdk.ValAddressFromBech32(e.OperatorAddress)
		require.NoError(t, err)
		v, err := c.app.StakingKeeper.GetValidator(ctx, valAddr)
		require.NoError(t, err)
		require.Equal(t, v.GetBondedTokens(), e.Bonded)
		require.Equal(t, math.MinInt(e.Bonded, valCap), e.CappedBonded)
		// floor(capped · 838 / 1000), exactly.
		want := new(big.Int).Mul(e.CappedBonded.BigInt(), big.NewInt(838))
		require.Equal(t, math.NewIntFromBigInt(want.Quo(want, big.NewInt(1000))), e.Vp)
		require.Equal(t, v.GetConsensusPower(pr), e.StockPower)
		sumVP = sumVP.Add(e.Vp)
		sumStock += e.StockPower
	}
	require.Equal(t, sumVP, res.TotalVp)
	require.Equal(t, sumStock, res.TotalStockPower)
}

// snapshotBytes is every participation snapshot as the store holds it: raw
// value bytes under their raw keys.
func (c *testChain) snapshotBytes() map[string][]byte {
	c.t.Helper()
	store := c.ctx().KVStore(c.app.GetKey(participationtypes.StoreKey))
	it := storetypes.KVStorePrefixIterator(store, participationtypes.ParticipationScoreKey.Bytes())
	defer func() { _ = it.Close() }()
	out := map[string][]byte{}
	for ; it.Valid(); it.Next() {
		out[string(it.Key())] = append([]byte(nil), it.Value()...)
	}
	return out
}

// A real double-sign (x/evidence in BeginBlock, x/staking's slash, the
// compliance hook) never rewrites a closed snapshot, at the boundary block of
// an epoch, right after it, and at the last block before the next one. Before
// S10 the hook named floor(h/L), the closed epoch, at the last two heights;
// with S10 that also zeroed the WARP the closed snapshot pins. What lands is
// WARP = 0 with its epoch kept, and no snapshot for the jailed validator from
// the in-flight epoch on (V-7).
func TestDoubleSignNeverRewritesAClosedSnapshot(t *testing.T) {
	const l, e = 5, 2 // withFastRounds: 5-block epochs
	for _, h := range []int64{e * l, e*l + 1, (e+1)*l - 1} {
		t.Run(fmt.Sprintf("height %d", h), func(t *testing.T) {
			c := newTestChain(t, withFastRounds(), withDoubleSignReady())
			for c.height < h-1 {
				c.nextBlock(time.Second)
			}
			pk := c.app.ParticipationKeeper
			v := c.vals[1].val
			lastClosed := uint64(h-1) / l

			before := c.snapshotBytes()
			require.NotEmpty(t, before)
			pinned, found, err := pk.GetSnapshot(c.ctx(), lastClosed, v)
			require.NoError(t, err)
			require.True(t, found)
			require.NotNil(t, pinned.Warp)
			require.True(t, pinned.Warp.IsPositive(), "the closed snapshot pins a WARP the bug would zero")
			wBefore, found, err := pk.GetWarp(c.ctx(), v)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, lastClosed, wBefore.LastEpoch)

			c.doubleSign(1)
			require.Equal(t, h, c.height)
			require.Equal(t, vrtypes.Status_STATUS_JAILED, c.regStatus(v))

			after := c.snapshotBytes()
			for k, bz := range before {
				require.Equal(t, bz, after[k], "a closed snapshot changed (key %x)", k)
			}
			ctx := c.ctx()
			for epoch := uint64(1); epoch <= uint64(h)/l; epoch++ {
				_, found, err := pk.GetSnapshot(ctx, epoch, v)
				require.NoError(t, err)
				require.Equal(t, epoch <= lastClosed, found, "epoch %d", epoch)
			}
			if h%l == 0 {
				// The boundary block closed epoch e for the others only.
				require.Len(t, after, len(before)+len(c.vals)-1)
			} else {
				require.Len(t, after, len(before))
			}

			w, found, err := pk.GetWarp(ctx, v)
			require.NoError(t, err)
			require.True(t, found)
			require.True(t, w.Value.IsZero(), "WARP is zeroed")
			require.Equal(t, wBefore.LastEpoch, w.LastEpoch, "punishment is not a measurement")
			msg, broken := pk.WarpInvariant(ctx)
			require.False(t, broken, msg)
		})
	}
}

// V-16 is checked, never halting (S10 spec): a broken WARP row is reported in
// the epoch's EndBlock log, and the block that closes the epoch still
// finalizes. The row is one V-16 must keep reporting after the epoch step: a
// validator that does not exist, "never scored" with a non-zero WARP.
func TestBrokenWarpRowIsLoggedNotHalted(t *testing.T) {
	c := newTestChain(t, withFastRounds())
	for c.height < 8 {
		c.nextBlock(time.Second)
	}
	ghost := sdk.ValAddress([]byte("v16-ghost-validator!"))
	ctx := c.ctx()
	pk := c.app.ParticipationKeeper
	require.NoError(t, pk.Warp.Set(ctx, ghost, participationtypes.WarpState{
		ValidatorAddress: ghost.String(), Value: math.LegacyNewDec(50), LastEpoch: 0,
	}))
	msg, broken := pk.WarpInvariant(ctx)
	require.True(t, broken)
	require.Contains(t, msg, ghost.String()+" was never scored but holds 50")

	logs := captureBlockLogs(t, c.app)
	require.NotPanics(t, func() {
		for c.height < 10 { // 10 closes epoch 2; finalizeBlock requires no error
			c.nextBlock(time.Second)
		}
	})
	out := logs.String()
	require.Contains(t, out, "participation V-16 invariant broken")
	require.Contains(t, out, "participation: epoch computed", "EndBlock went on past V-16")

	// The others were stepped as usual.
	w, found, err := pk.GetWarp(c.ctx(), c.vals[0].val)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(2), w.LastEpoch)
}
