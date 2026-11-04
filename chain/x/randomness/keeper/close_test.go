package keeper_test

import (
	"encoding/hex"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/randomness/keeper"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

func hexBytes(t *testing.T, s string, n int) []byte {
	t.Helper()
	b, err := types.DecodeHex(s, n)
	require.NoError(t, err)
	return b
}

// threeKeyed builds acme, beta and gamma, one ACTIVE validator each, keys bound
// at height 0, and returns the validators and their keys.
func threeKeyed(f *fixture) ([]sdk.ValAddress, []vrf.PrivateKey) {
	vals := []sdk.ValAddress{val(1), val(2), val(3)}
	ids := []string{"acme", "beta", "gamma"}
	keys := make([]vrf.PrivateKey, 3)
	for i, v := range vals {
		f.entity(ids[i], v)
		f.active(v)
		keys[i] = f.key(v, byte(i+1), 0)
	}
	return vals, keys
}

func TestRoundOneIsAlwaysSkipped(t *testing.T) {
	f := newFixture(t)
	threeKeyed(f)
	input1 := hexBytes(t, f.round().Input, 32)

	f.run(1, 20)

	d := f.draw(1)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, d.Outcome)
	require.True(t, strings.HasPrefix(d.SkipReason, "quorum not met: 0 of 3 submitted, 2 required (0 had an eligible key)"), d.SkipReason)
	require.Equal(t, []string{"acme", "beta", "gamma"}, d.Pool)
	require.Empty(t, d.Winners)
	require.Zero(t, d.CohortRound)

	r := f.round()
	require.EqualValues(t, 2, r.Number)
	require.Equal(t, hex.EncodeToString(types.Seed(1, input1, nil)), r.Input)
	require.Equal(t, d.Seed, r.Input)
	require.EqualValues(t, 10, r.KeyCutoffHeight)
	require.EqualValues(t, 40, r.RotationHeight, "scheduled in the same EndBlock")
	require.EqualValues(t, 30, r.WindowOpenHeight)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_SCHEDULED, r.Status)
	requireEvent(t, f.ctx, types.EventTypeDrawSkipped, types.AttributeKeyCohortRound, "0")
}

func TestAQuorumDraws(t *testing.T) {
	f := newFixture(t)
	vals, keys := threeKeyed(f)
	f.run(1, 29)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, f.round().Status)
	input2 := hexBytes(t, f.round().Input, 32)

	f.submit(31, vals[0], keys[0])
	f.submit(31, vals[2], keys[2])
	outputs := f.outputsOf()
	f.run(30, 40)

	d := f.draw(2)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_DRAWN, d.Outcome)
	require.Equal(t, []string{"acme", "beta", "gamma"}, d.Pool)
	require.EqualValues(t, 3, d.Keyed)
	require.EqualValues(t, 2, d.Submitted)
	require.EqualValues(t, 2, d.Required)
	require.Len(t, d.Outputs, 2)
	require.True(t, sort.StringsAreSorted(d.Outputs))
	seed := types.Seed(2, input2, outputs)
	require.Equal(t, hex.EncodeToString(seed), d.Seed)
	require.Equal(t, types.Winners(seed, d.Pool), d.Winners)
	require.Len(t, d.Winners, 3)
	require.EqualValues(t, 2, d.CohortRound)
	require.Equal(t, hex.EncodeToString(input2), d.Input)
	require.EqualValues(t, 30, d.WindowOpenHeight)
	require.EqualValues(t, 40, d.RotationHeight)
	require.Empty(t, d.SkipReason)

	requireEvent(t, f.ctx, types.EventTypeSeedComputed, types.AttributeKeyRound, "2")
	requireEvent(t, f.ctx, types.EventTypeSeedComputed, types.AttributeKeySeed, d.Seed)
	requireEvent(t, f.ctx, types.EventTypeShadowDraw, types.AttributeKeyWinners, strings.Join(d.Winners, ","))

	require.Empty(t, f.poolIDs())
	n := 0
	require.NoError(t, f.k.Designated.Walk(f.ctx, nil, func([]byte, string) (bool, error) { n++; return false, nil }))
	require.Zero(t, n)
	require.NoError(t, f.k.Submissions.Walk(f.ctx, nil, func(string, types.Submission) (bool, error) { n++; return false, nil }))
	require.Zero(t, n)

	r := f.round()
	require.EqualValues(t, 3, r.Number)
	require.Equal(t, d.Seed, r.Input)
	require.EqualValues(t, 30, r.KeyCutoffHeight)
}

func TestAMissedQuorumSkipsAndTheCohortStands(t *testing.T) {
	f := newFixture(t)
	vals, keys := threeKeyed(f)
	f.run(1, 29)
	f.submit(31, vals[0], keys[0])
	f.submit(31, vals[1], keys[1])
	f.run(30, 49) // round 2 drawn at 40, round 3 open at 49
	require.EqualValues(t, 3, f.round().Number)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, f.round().Status)
	input3 := hexBytes(t, f.round().Input, 32)

	f.submit(55, vals[2], keys[2])
	outputs := f.outputsOf()
	f.run(50, 60)

	d := f.draw(3)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, d.Outcome)
	require.True(t, strings.HasPrefix(d.SkipReason, "quorum not met: 1 of 3 submitted, 2 required (3 had an eligible key)"), d.SkipReason)
	require.EqualValues(t, 2, d.CohortRound)
	require.Empty(t, d.Winners)
	requireEvent(t, f.ctx, types.EventTypeDrawSkipped, types.AttributeKeyCohortRound, "2")
	requireEvent(t, f.ctx, types.EventTypeDrawSkipped, types.AttributeKeyReason, d.SkipReason)

	// The outputs of a skipped round chain into the next input (K7).
	r := f.round()
	require.EqualValues(t, 4, r.Number)
	require.Equal(t, hex.EncodeToString(types.Seed(3, input3, outputs)), r.Input)
	require.Equal(t, d.Seed, r.Input)
}

func TestEmptyPoolSkips(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1)) // never ACTIVE
	f.run(1, 20)

	d := f.draw(1)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, d.Outcome)
	require.Equal(t, "pool is empty", d.SkipReason)
	require.Empty(t, d.Pool)
}

func TestAWindowThatNeverOpenedSkips(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1))
	f.active(val(1))
	f.stake.fail = true

	f.run(1, 20)

	d := f.draw(1)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, d.Outcome)
	require.Equal(t, "window never opened", d.SkipReason)
	require.EqualValues(t, 2, f.round().Number, "the chain goes on")
}

func TestARestartPastTheRotationHeightClosesTheRoundOnce(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1))
	f.active(val(1))
	require.NoError(t, f.k.CurrentRound.Set(f.ctx, types.Round{
		Number: 7, Input: types.DefaultInitialInput(), KeyCutoffHeight: 10,
		WindowOpenHeight: 30, RotationHeight: 40, Status: types.RoundStatus_ROUND_STATUS_SCHEDULED,
	}))

	f.run(500, 500)

	d := f.draw(7)
	require.Equal(t, "window never opened", d.SkipReason)
	r := f.round()
	require.EqualValues(t, 8, r.Number)
	require.EqualValues(t, 520, r.RotationHeight, "500+11=511 rounds up to 520")
}

func TestHistoryKeepsFiftyTwoRounds(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1))
	f.active(val(1))
	f.part.epochLen = 1
	require.NoError(t, f.k.Params.Set(f.ctx, types.Params{RotationEpochs: 1, WindowBlocks: 1}))

	f.run(1, 107) // round n closes at 2n+1: round 53 at 107

	require.EqualValues(t, 54, f.round().Number)
	var rounds []uint64
	require.NoError(t, keeper.Draws(f.k).Walk(f.ctx, nil, func(n uint64, _ types.Draw) (bool, error) {
		rounds = append(rounds, n)
		return false, nil
	}))
	require.Len(t, rounds, 52)
	require.EqualValues(t, 2, rounds[0])
	require.EqualValues(t, 53, rounds[len(rounds)-1])
	for _, n := range rounds {
		d := f.draw(n)
		require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, d.Outcome, "round "+strconv.Itoa(int(n)))
	}
}

func TestKeeperInterfacesAreReadOnly(t *testing.T) {
	methods := func(i any) []string {
		typ := reflect.TypeOf(i).Elem()
		var names []string
		for n := 0; n < typ.NumMethod(); n++ {
			names = append(names, typ.Method(n).Name)
		}
		sort.Strings(names)
		return names
	}
	require.Equal(t, []string{"EntityOf", "Tickets"}, methods((*types.EntityregistryKeeper)(nil)))
	require.Equal(t, []string{"GetValidatorStatus"}, methods((*types.ValidatorregistryKeeper)(nil)))
	require.Equal(t, []string{"EntityStanding"}, methods((*types.StakeengineKeeper)(nil)))
	require.Equal(t, []string{"EpochLengthBlocks"}, methods((*types.ParticipationKeeper)(nil)))
}
