package app

import (
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

// Days of 24 hours, the unit the Register states unbonding in.
const day = 24 * time.Hour

// v091EvidenceBlocks and v091EvidenceAge are the evidence limits the live DevNet
// holds (measured 2026-08-04): CometBFT's stock values, which v0.9.1 never
// changed.
const (
	v091EvidenceBlocks = int64(100_000)
	v091EvidenceAge    = 48 * time.Hour
)

// oldConsensusParams is a consensus params set whose every group differs from
// the harness's InitChain values, so a handler that rewrote a group it should
// keep (or reset one to a default) is seen. The evidence group is the v0.9.1
// DevNet's, with a max_bytes that is not CometBFT's default.
func oldConsensusParams() cmtproto.ConsensusParams {
	return cmtproto.ConsensusParams{
		Block:     &cmtproto.BlockParams{MaxBytes: 22_020_096, MaxGas: -1},
		Evidence:  &cmtproto.EvidenceParams{MaxAgeNumBlocks: v091EvidenceBlocks, MaxAgeDuration: v091EvidenceAge, MaxBytes: 1_048_577},
		Validator: &cmtproto.ValidatorParams{PubKeyTypes: []string{"ed25519"}},
		Version:   &cmtproto.VersionParams{App: 0},
		Abci:      &cmtproto.ABCIParams{VoteExtensionsEnableHeight: 0},
	}
}

func setConsensusParams(t *testing.T, c *testChain, ctx sdk.Context, p cmtproto.ConsensusParams) {
	t.Helper()
	require.NoError(t, c.app.ConsensusParamsKeeper.ParamsStore.Set(ctx, p))
}

func consensusParams(t *testing.T, c *testChain, ctx sdk.Context) cmtproto.ConsensusParams {
	t.Helper()
	p, err := c.app.ConsensusParamsKeeper.ParamsStore.Get(ctx)
	require.NoError(t, err)
	return p
}

func setUnbondingTime(t *testing.T, c *testChain, ctx sdk.Context, u time.Duration) {
	t.Helper()
	p, err := c.app.StakingKeeper.GetParams(ctx)
	require.NoError(t, err)
	p.UnbondingTime = u
	require.NoError(t, c.app.StakingKeeper.SetParams(ctx, p))
}

// requireOnlyEvidenceAgeMoved fails unless after equals before in every group
// but the two evidence age limits, which must be the given ones. The groups are
// compared as encoded bytes, so "byte-identical" is what is checked.
func requireOnlyEvidenceAgeMoved(t *testing.T, before, after cmtproto.ConsensusParams, blocks int64, age time.Duration) {
	t.Helper()
	require.Equal(t, blocks, after.Evidence.MaxAgeNumBlocks, "max_age_num_blocks")
	require.Equal(t, age, after.Evidence.MaxAgeDuration, "max_age_duration")
	require.Equal(t, before.Evidence.MaxBytes, after.Evidence.MaxBytes, "evidence max_bytes must be kept")
	want := before
	want.Evidence = &cmtproto.EvidenceParams{MaxAgeNumBlocks: blocks, MaxAgeDuration: age, MaxBytes: before.Evidence.MaxBytes}
	wantBz, err := want.Marshal()
	require.NoError(t, err)
	gotBz, err := after.Marshal()
	require.NoError(t, err)
	require.Equal(t, wantBz, gotBz, "a group other than the evidence age limits changed")
}

// The upgrade sets the evidence window to the unbonding period U (owner
// 2026-10-29): max_age_duration = U and max_age_num_blocks = floor(U / 6 s), so
// that the window, max(blocks x block time, U) because CometBFT and x/evidence
// expire evidence only when both limits have passed, is exactly U while the
// average block interval, halts included, is at most 6 s. A window shorter than U lets a reported double-sign go
// unslashed; one longer than U lets old evidence reach a validator re-created on
// the key, so a larger stored value is capped as well. Nothing else in the
// consensus params moves.
func TestUpgradeSetsTheEvidenceWindowToTheUnbondingPeriod(t *testing.T) {
	type limits struct {
		blocks int64
		age    time.Duration
	}
	for _, tc := range []struct {
		name      string
		unbonding time.Duration
		stored    limits
		want      limits
		changed   bool
	}{
		{"the v0.9.1 DevNet's 100,000 blocks and 48h are raised", 21 * day,
			limits{v091EvidenceBlocks, v091EvidenceAge}, limits{302_400, 21 * day}, true},
		{"the target itself is left alone", 21 * day,
			limits{302_400, 21 * day}, limits{302_400, 21 * day}, false},
		{"the old genesis templates' 362,880 blocks are capped", 21 * day,
			limits{362_880, 21 * day}, limits{302_400, 21 * day}, true},
		{"both above the target are capped", 21 * day,
			limits{1_000_000, 60 * day}, limits{302_400, 21 * day}, true},
		{"a longer duration alone is capped", 21 * day,
			limits{302_400, 21*day + time.Second}, limits{302_400, 21 * day}, true},
		{"one above and one below, both to the target", 21 * day,
			limits{500_000, v091EvidenceAge}, limits{302_400, 21 * day}, true},
		{"a block short of the target is raised", 21 * day,
			limits{302_399, 21 * day}, limits{302_400, 21 * day}, true},
		{"it follows the stored unbonding time, 14 days", 14 * day,
			limits{v091EvidenceBlocks, v091EvidenceAge}, limits{201_600, 14 * day}, true},
		{"floor of a non-round unbonding time", 21*day + 5*time.Second,
			limits{v091EvidenceBlocks, v091EvidenceAge}, limits{302_400, 21*day + 5*time.Second}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestChain(t)
			var logs strings.Builder
			ctx := c.ctx().WithLogger(log.NewLogger(&logs, log.ColorOption(false)))
			emptyEntityRegistry(t, c, ctx)
			setUnbondingTime(t, c, ctx, tc.unbonding)
			before := oldConsensusParams()
			before.Evidence.MaxAgeNumBlocks, before.Evidence.MaxAgeDuration = tc.stored.blocks, tc.stored.age
			setConsensusParams(t, c, ctx, before)

			require.NoError(t, c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo(newAcct("a").addr)}))

			requireOnlyEvidenceAgeMoved(t, before, consensusParams(t, c, ctx), tc.want.blocks, tc.want.age)
			out := logs.String()
			if tc.changed {
				require.Contains(t, out, "setting the evidence window to the unbonding period")
			} else {
				require.NotContains(t, out, "evidence window", "a window already at the target is not rewritten")
			}
		})
	}
}

// The upgrade must not write consensus params the node would refuse: they are
// validated exactly as x/consensus validates a MsgUpdateParams. An unbonding
// time below one 6 s block gives max_age_num_blocks 0, which CometBFT refuses,
// so the upgrade halts and nothing of the handler is written: not the evidence
// params, not the slashing raise before them, not an attestation.
func TestUpgradeRefusesAnEvidenceWindowTheNodeWouldRefuse(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)
	setSlashFractions(t, c, ctx, math.LegacyNewDecWithPrec(5, 2), math.LegacyNewDecWithPrec(1, 2))
	setUnbondingTime(t, c, ctx, 5*time.Second)
	before := oldConsensusParams()
	setConsensusParams(t, c, ctx, before)

	err := c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo(newAcct("a").addr)})
	require.ErrorContains(t, err, "evidence.MaxAgeNumBlocks must be greater than 0")
	require.ErrorContains(t, err, "v0.10.0")

	requireOnlyEvidenceAgeMoved(t, before, consensusParams(t, c, ctx), v091EvidenceBlocks, v091EvidenceAge)
	sp, err := c.app.SlashingKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, "0.050000000000000000", sp.SlashFractionDoubleSign.String(), "the raise was written by a failed upgrade")
	for _, v := range c.vals {
		_, found, err := c.app.EntityregistryKeeper.EntityOf(ctx, v.val)
		require.NoError(t, err)
		require.False(t, found, "%s was attested by a failed upgrade", v.val)
	}
}

// A stored record without a block, evidence or validator group cannot be
// rewritten group by group (and CometBFT would refuse it): the upgrade halts
// with nothing of the handler written.
func TestUpgradeRefusesConsensusParamsMissingAGroup(t *testing.T) {
	for _, tc := range []struct {
		name string
		drop func(*cmtproto.ConsensusParams)
	}{
		{"no block group", func(p *cmtproto.ConsensusParams) { p.Block = nil }},
		{"no evidence group", func(p *cmtproto.ConsensusParams) { p.Evidence = nil }},
		{"no validator group", func(p *cmtproto.ConsensusParams) { p.Validator = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestChain(t)
			ctx := c.ctx()
			emptyEntityRegistry(t, c, ctx)
			setSlashFractions(t, c, ctx, math.LegacyNewDecWithPrec(5, 2), math.LegacyNewDecWithPrec(1, 2))
			before := oldConsensusParams()
			tc.drop(&before)
			setConsensusParams(t, c, ctx, before)

			err := c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo(newAcct("a").addr)})
			require.ErrorContains(t, err, "lack a block, evidence or validator group")

			after := consensusParams(t, c, ctx)
			require.Equal(t, before, after, "the consensus params were written by a failed upgrade")
			sp, err := c.app.SlashingKeeper.GetParams(ctx)
			require.NoError(t, err)
			require.Equal(t, "0.050000000000000000", sp.SlashFractionDoubleSign.String(), "the raise was written by a failed upgrade")
		})
	}
}

// A record with no version group (x/consensus fills it with zero only to
// validate) is accepted, and written back still without one: the version group
// keeps its bytes like the others.
func TestUpgradeKeepsAMissingVersionGroupMissing(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)
	before := oldConsensusParams()
	before.Version = nil
	setConsensusParams(t, c, ctx, before)

	require.NoError(t, c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo(newAcct("a").addr)}))

	after := consensusParams(t, c, ctx)
	require.Nil(t, after.Version, "the upgrade added a version group")
	requireOnlyEvidenceAgeMoved(t, before, after, 302_400, 21*day)
}
