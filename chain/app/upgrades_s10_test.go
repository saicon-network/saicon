package app

import (
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	"github.com/stretchr/testify/require"

	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
)

// preS10Participation puts x/participation back where v0.10.x leaves it:
// consensus version 1, params without the four WARP fields (they read back as
// zeros), and no WARP rows. The epoch length is set to a value that is not the
// default, so a migration that reset it is seen.
func preS10Participation(t *testing.T, c *testChain) {
	t.Helper()
	ctx := c.ctx()
	pk := c.app.ParticipationKeeper
	require.NoError(t, pk.Params.Set(ctx, participationtypes.Params{
		EpochLengthBlocks: 37,
		EpDecay:           math.LegacyZeroDec(),
	}))
	require.NoError(t, pk.Warp.Clear(ctx, nil))
	vm, err := c.app.UpgradeKeeper.GetModuleVersionMap(ctx)
	require.NoError(t, err)
	vm[participationtypes.ModuleName] = 1
	require.NoError(t, c.app.UpgradeKeeper.SetModuleVersionMap(ctx, vm))
}

// v0.11.0, the S10 part, through a real block: x/participation goes 1 -> 2 and
// its migration writes the WARP defaults while keeping the stored params; no
// WARP row is seeded; the handler logs what it found; and the voting-power
// preview, which refuses invalid WARP params, answers in the same state.
func TestUpgradeV0110RunsTheS10Migrations(t *testing.T) {
	c := newTestChain(t)
	require.True(t, c.app.UpgradeKeeper.HasHandler(UpgradeV0110))
	require.Equal(t, uint64(2), c.app.ModuleManager.GetVersionMap()[participationtypes.ModuleName])

	preS10Participation(t, c)
	ctx := c.ctx()
	_, err := c.app.RewardsKeeper.VotingPowerPreview(ctx)
	require.Error(t, err, "before the upgrade the stored params carry no WARP and the preview refuses")

	logs := captureBlockLogs(t, c.app)
	plan := upgradetypes.Plan{Name: UpgradeV0110, Height: c.height + 1}
	require.NoError(t, c.app.UpgradeKeeper.ScheduleUpgrade(ctx, plan))
	c.nextBlock(time.Second)

	ctx = c.ctx()
	_, err = c.app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.Error(t, err, "the plan must be cleared once applied")
	vm, err := c.app.UpgradeKeeper.GetModuleVersionMap(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), vm[participationtypes.ModuleName])

	p, err := c.app.ParticipationKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(37), p.EpochLengthBlocks, "the stored epoch length is kept")
	require.True(t, p.EpDecay.IsZero())
	require.Equal(t, participationtypes.DefaultWarpFall(), p.WarpFall)
	require.Equal(t, participationtypes.DefaultWarpRise(), p.WarpRise)
	require.Equal(t, participationtypes.DefaultWarpInitial(), p.WarpInitial)
	require.Equal(t, participationtypes.DefaultWarpThreshold(), p.WarpThreshold)

	out := logs.String()
	require.Contains(t, out, "v0.11.0: S10 WARP params in force")
	require.Contains(t, out, "params=valid")
	require.Contains(t, out, "warp_rows=0")

	res, err := c.app.RewardsKeeper.VotingPowerPreview(ctx)
	require.NoError(t, err)
	require.Len(t, res.Validators, len(c.vals))
	for _, e := range res.Validators {
		require.False(t, e.WarpMeasured, "%s: no WARP is measured before the first epoch after the upgrade", e.OperatorAddress)
		require.True(t, e.Warp.Equal(participationtypes.DefaultWarpInitial()), "%s: shown at warp_initial", e.OperatorAddress)
	}
}

// The handler's record of the S10 state writes nothing and never fails the
// upgrade: WARP params that do not validate are logged as an error.
func TestLogS10StateReportsInvalidWarpParams(t *testing.T) {
	c := newTestChain(t)
	var logs strings.Builder
	ctx := c.ctx().WithLogger(log.NewLogger(&logs, log.ColorOption(false)))
	require.NoError(t, c.app.ParticipationKeeper.Params.Set(ctx, participationtypes.Params{
		EpochLengthBlocks: 100,
		EpDecay:           math.LegacyZeroDec(),
	}))
	logS10State(ctx, c.app.ParticipationKeeper)
	out := logs.String()
	require.Contains(t, out, "the WARP params do not validate")
	require.Contains(t, out, "v0.11.0: S10 WARP params in force")
	require.NotContains(t, out, "params=valid")
}
