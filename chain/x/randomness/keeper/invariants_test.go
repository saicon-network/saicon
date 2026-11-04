package keeper_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"unicode/utf8"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

// openWithSubmissions builds round 2 (cutoff 10, O=30, R=40) OPEN with three
// keyed entities and a valid submission from acme and beta.
func openWithSubmissions(t *testing.T) (*fixture, []vrf.PrivateKey) {
	t.Helper()
	f := newFixture(t)
	vals, keys := threeKeyed(f)
	f.run(1, 29)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, f.round().Status)
	f.submit(31, vals[0], keys[0])
	f.submit(31, vals[1], keys[1])
	msg, broken := f.k.RandomnessInvariant(f.ctx)
	require.False(t, broken, msg)
	return f, keys
}

func TestV14(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(t *testing.T, f *fixture, keys []vrf.PrivateKey)
		want   string
	}{
		{"(a) status unspecified", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			r := f.round()
			r.Status = types.RoundStatus_ROUND_STATUS_UNSPECIFIED
			require.NoError(t, f.k.CurrentRound.Set(f.ctx, r))
		}, "(a)"},
		{"(a) heights out of order", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			r := f.round()
			r.WindowOpenHeight = r.RotationHeight
			require.NoError(t, f.k.CurrentRound.Set(f.ctx, r))
		}, "(a)"},
		{"(a) cutoff above the window", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			r := f.round()
			r.KeyCutoffHeight = r.WindowOpenHeight + 1
			require.NoError(t, f.k.CurrentRound.Set(f.ctx, r))
		}, "(a)"},
		{"(b) designated entry missing", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			require.NoError(t, f.k.Designated.Remove(f.ctx, val(1)))
		}, "(b)"},
		{"(b) designated entry without a pool validator", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			require.NoError(t, f.k.Designated.Set(f.ctx, val(9), "acme"))
		}, "(b)"},
		{"(b) designated entry points at another entity", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			require.NoError(t, f.k.Designated.Set(f.ctx, val(1), "beta"))
		}, "(b)"},
		{"(c) submission without a pool entry", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			s, err := f.k.Submissions.Get(f.ctx, "acme")
			require.NoError(t, err)
			s.EntityId = "zeta"
			require.NoError(t, f.k.Submissions.Set(f.ctx, "zeta", s))
		}, "(c)"},
		{"(c) wrong validator", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			s, err := f.k.Submissions.Get(f.ctx, "acme")
			require.NoError(t, err)
			s.Validator = val(7).String()
			require.NoError(t, f.k.Submissions.Set(f.ctx, "acme", s))
		}, "(c)"},
		{"(c) height before the window", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			s, err := f.k.Submissions.Get(f.ctx, "acme")
			require.NoError(t, err)
			s.Height = 29
			require.NoError(t, f.k.Submissions.Set(f.ctx, "acme", s))
		}, "(c)"},
		{"(c) height at the rotation", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			s, err := f.k.Submissions.Get(f.ctx, "acme")
			require.NoError(t, err)
			s.Height = 40
			require.NoError(t, f.k.Submissions.Set(f.ctx, "acme", s))
		}, "(c)"},
		{"(c) proof of another key", func(t *testing.T, f *fixture, keys []vrf.PrivateKey) {
			s, err := f.k.Submissions.Get(f.ctx, "acme")
			require.NoError(t, err)
			r := f.round()
			input := hexBytes(t, r.Input, 32)
			s.Proof = hex.EncodeToString(keys[2].Prove(types.RoundAlpha(testChainID, r.Number, input)))
			require.NoError(t, f.k.Submissions.Set(f.ctx, "acme", s))
		}, "(c)"},
		{"(c) proof of another round", func(t *testing.T, f *fixture, keys []vrf.PrivateKey) {
			s, err := f.k.Submissions.Get(f.ctx, "acme")
			require.NoError(t, err)
			r := f.round()
			input := hexBytes(t, r.Input, 32)
			s.Proof = hex.EncodeToString(keys[0].Prove(types.RoundAlpha(testChainID, r.Number+1, input)))
			require.NoError(t, f.k.Submissions.Set(f.ctx, "acme", s))
		}, "(c)"},
		{"(c) malformed proof", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			s, err := f.k.Submissions.Get(f.ctx, "acme")
			require.NoError(t, err)
			s.Proof = "zz"
			require.NoError(t, f.k.Submissions.Set(f.ctx, "acme", s))
		}, "(c)"},
		{"(c) altered output", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			s, err := f.k.Submissions.Get(f.ctx, "acme")
			require.NoError(t, err)
			s.Output = strings.Repeat("00", vrf.OutputSize)
			require.NoError(t, f.k.Submissions.Set(f.ctx, "acme", s))
		}, "(c)"},
		{"(d) more submissions than pool entries", func(t *testing.T, f *fixture, _ []vrf.PrivateKey) {
			s, err := f.k.Submissions.Get(f.ctx, "acme")
			require.NoError(t, err)
			for _, id := range []string{"x1", "x2", "x3", "x4"} {
				s.EntityId = id
				require.NoError(t, f.k.Submissions.Set(f.ctx, id, s))
			}
		}, "(d)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, keys := openWithSubmissions(t)
			tc.break_(t, f, keys)
			msg, broken := f.k.RandomnessInvariant(f.ctx)
			require.True(t, broken, "invariant held after: %s", tc.name)
			require.Contains(t, msg, tc.want)
		})
	}
}

func TestV14HoldsOnAFreshRound(t *testing.T) {
	f := newFixture(t)
	msg, broken := f.k.RandomnessInvariant(f.ctx)
	require.False(t, broken, msg)
	f.run(1, 40)
	msg, broken = f.k.RandomnessInvariant(f.ctx)
	require.False(t, broken, msg)
}

func TestV14ReportsAMissingRoundAsBroken(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.CurrentRound.Remove(f.ctx))
	msg, broken := f.k.RandomnessInvariant(f.ctx)
	require.True(t, broken)
	require.Contains(t, msg, "read error")
}

func TestABrokenV14SkipsTheDraw(t *testing.T) {
	f, _ := openWithSubmissions(t)
	s, err := f.k.Submissions.Get(f.ctx, "acme")
	require.NoError(t, err)
	s.Output = strings.Repeat("11", vrf.OutputSize)
	require.NoError(t, f.k.Submissions.Set(f.ctx, "acme", s))

	f.run(30, 40) // a nil error from every EndBlock, the rotation included

	d := f.draw(2)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, d.Outcome)
	require.True(t, strings.HasPrefix(d.SkipReason, "V-14: "), d.SkipReason)
	require.Empty(t, d.Winners)
	require.EqualValues(t, 3, f.round().Number, "the chain of rounds goes on")
}

// withLogs makes the fixture's blocks log into the returned buffer.
func withLogs(f *fixture) *bytes.Buffer {
	out := &bytes.Buffer{}
	f.ctx = f.ctx.WithLogger(log.NewLogger(out, log.ColorOption(false)))
	return out
}

// A broken V-14 is corrupted state and is logged at error level (spec §15).
func TestABrokenV14IsLoggedAtError(t *testing.T) {
	f, _ := openWithSubmissions(t)
	out := withLogs(f)
	s, err := f.k.Submissions.Get(f.ctx, "acme")
	require.NoError(t, err)
	s.Output = strings.Repeat("11", vrf.OutputSize)
	require.NoError(t, f.k.Submissions.Set(f.ctx, "acme", s))

	f.run(30, 40)
	require.Contains(t, out.String(), "ERR")
	require.Contains(t, out.String(), "V-14 broken")
	require.Contains(t, out.String(), "stored output differs")
}

// A missed quorum, an empty pool and a window that never opened are ordinary
// outcomes and stay at info level.
func TestOrdinarySkipsAreLoggedAtInfo(t *testing.T) {
	f := newFixture(t)
	out := withLogs(f)
	vals, keys := threeKeyed(f)
	f.run(1, 29)
	f.submit(31, vals[0], keys[0]) // 1 of 3: below the quorum
	f.run(30, 40)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, f.draw(2).Outcome)
	require.Contains(t, out.String(), "quorum not met")
	require.Contains(t, out.String(), "INF")
	require.NotContains(t, out.String(), "ERR")

	f = newFixture(t)
	out = withLogs(f)
	f.entity("acme", val(1)) // never ACTIVE: an empty pool
	f.run(1, 20)
	require.Contains(t, out.String(), "pool is empty")
	require.NotContains(t, out.String(), "ERR")

	f = newFixture(t)
	out = withLogs(f)
	f.entity("acme", val(1))
	f.active(val(1))
	f.stake.fail = true // the snapshot never succeeds: the window never opens
	f.run(1, 20)
	require.Contains(t, out.String(), "window never opened")
	require.NotContains(t, out.String(), "V-14", "a failed snapshot is logged, but not as a V-14 break")
	require.Contains(t, out.String(), "INF randomness: draw skipped")
}

// The V-14 message spells out the first violations and carries the full count.
func TestV14MessageListsOnlyTheFirstViolations(t *testing.T) {
	f, _ := openWithSubmissions(t)
	s, err := f.k.Submissions.Get(f.ctx, "acme")
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		id := "x" + strings.Repeat("a", i+1)
		s.EntityId = id
		require.NoError(t, f.k.Submissions.Set(f.ctx, id, s))
	}
	msg, broken := f.k.RandomnessInvariant(f.ctx)
	require.True(t, broken)
	require.Contains(t, msg, "21 violation(s)") // 20 strays and (d)
	require.Contains(t, msg, "and 16 more")
	require.Equal(t, 5, strings.Count(msg, "has no pool entry"))
}

// A stored skip reason has a hard length bound, whatever the violation says.
func TestASkipReasonIsCapped(t *testing.T) {
	f, _ := openWithSubmissions(t)
	// One violation whose text is far longer than the bound, in multi-byte runes.
	long := strings.Repeat("é", 600)
	require.NoError(t, f.k.Pool.Set(f.ctx, "zeta", types.PoolEntry{Validator: long}))
	f.run(30, 40)

	d := f.draw(2)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, d.Outcome)
	require.True(t, strings.HasPrefix(d.SkipReason, "V-14: "), d.SkipReason)
	require.LessOrEqual(t, len(d.SkipReason), 512)
	require.True(t, utf8.ValidString(d.SkipReason))
	require.True(t, strings.HasSuffix(d.SkipReason, "..."))
}

// A window that never opened is ordinary, but a V-14 break in the same round is
// not: the break wins, so the record and the error log carry the violation
// (spec §15) rather than "window never opened".
func TestABrokenV14WinsOverAWindowThatNeverOpened(t *testing.T) {
	f := newFixture(t)
	out := withLogs(f)
	f.entity("acme", val(1))
	f.active(val(1))
	f.stake.fail = true // the snapshot never succeeds: the window never opens
	f.run(1, 19)
	require.NotEqual(t, types.RoundStatus_ROUND_STATUS_OPEN, f.round().Status)
	// A stray submission with no pool entry behind it breaks V-14 (c) and (d).
	require.NoError(t, f.k.Submissions.Set(f.ctx, "stray", types.Submission{
		EntityId: "stray", Validator: val(9).String(), Output: strings.Repeat("11", vrf.OutputSize), Height: 15,
	}))

	f.run(20, 20)

	d := f.draw(1)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, d.Outcome)
	require.True(t, strings.HasPrefix(d.SkipReason, "V-14: "), d.SkipReason)
	require.Contains(t, d.SkipReason, "has no pool entry")
	require.Contains(t, out.String(), "ERR")
	require.Contains(t, out.String(), "V-14 broken")
	require.Contains(t, out.String(), "has no pool entry")
	require.NotContains(t, out.String(), "window never opened")
}
