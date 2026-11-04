package types_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

func u64(n uint64) []byte { return binary.BigEndian.AppendUint64(nil, n) }
func u32(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func fill(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }

func TestRoundAlpha(t *testing.T) {
	in := fill(0xab, 32)
	want := cat([]byte("saicon/randomness/alpha/v1"), []byte{0, 11}, []byte("saicon-test"), u64(3), in)
	require.Equal(t, want, types.RoundAlpha("saicon-test", 3, in))

	base := types.RoundAlpha("saicon-test", 3, in)
	require.NotEqual(t, base, types.RoundAlpha("saicon-other", 3, in))
	require.NotEqual(t, base, types.RoundAlpha("saicon-test", 4, in))
	require.NotEqual(t, base, types.RoundAlpha("saicon-test", 3, fill(0xac, 32)))
}

func TestSeedSortsAndChains(t *testing.T) {
	in := fill(0x11, 32)
	o1 := fill(0x01, 64)
	o2 := fill(0x02, 64)

	outs := [][]byte{o2, o1}
	inCopy := append([]byte(nil), in...)
	outsCopy := [][]byte{append([]byte(nil), o2...), append([]byte(nil), o1...)}

	seed := types.Seed(5, in, outs)
	require.Equal(t, seed, types.Seed(5, in, [][]byte{o1, o2}))

	h := sha256.Sum256(cat([]byte("saicon/randomness/seed/v1"), u64(5), in, u32(2), o1, o2))
	require.Equal(t, h[:], seed)

	require.Equal(t, inCopy, in, "input is not modified")
	require.Equal(t, outsCopy, outs, "outputs slice is not modified")

	// Zero outputs: the chain stays alive through the round number.
	h0 := sha256.Sum256(cat([]byte("saicon/randomness/seed/v1"), u64(5), in, u32(0)))
	require.Equal(t, h0[:], types.Seed(5, in, nil))
	require.Equal(t, h0[:], types.Seed(5, in, [][]byte{}))
	require.NotEqual(t, types.Seed(5, in, nil), types.Seed(6, in, nil))
}

func scoreOf(seed []byte, id string) [32]byte {
	return sha256.Sum256(cat([]byte("saicon/randomness/draw/v1"), seed, []byte(id)))
}

func TestDrawOrder(t *testing.T) {
	ids := []string{"acme", "beta", "gamma"}
	seed := fill(0x42, 32)

	want := append([]string(nil), ids...)
	sort.Slice(want, func(i, j int) bool {
		a, b := scoreOf(seed, want[i]), scoreOf(seed, want[j])
		return bytes.Compare(a[:], b[:]) < 0
	})
	input := append([]string(nil), ids...)
	got := types.DrawOrder(seed, input)
	require.Equal(t, want, got)
	require.ElementsMatch(t, ids, got)
	require.Equal(t, ids, input, "input slice is not modified")

	other := fill(0x43, 32)
	require.NotEqual(t, got, types.DrawOrder(other, ids), "for this fixed seed pair the order differs")
}

func TestWinners(t *testing.T) {
	seed := fill(0x07, 32)
	mk := func(n int) []string {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = "entity-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		}
		return ids
	}
	for _, tc := range []struct{ pool, want int }{{0, 0}, {1, 1}, {22, 22}, {30, 22}} {
		ids := mk(tc.pool)
		w := types.Winners(seed, ids)
		require.Len(t, w, tc.want)
		require.Equal(t, types.DrawOrder(seed, ids)[:tc.want], w)
	}
}

func TestQuorum(t *testing.T) {
	for _, tc := range []struct {
		pool, submitted uint32
		met             bool
	}{
		{0, 0, false}, {1, 0, false}, {1, 1, true}, {2, 1, false}, {2, 2, true},
		{3, 1, false}, {3, 2, true}, {75, 49, false}, {75, 50, true},
	} {
		require.Equal(t, tc.met, types.QuorumMet(tc.pool, tc.submitted), "pool %d submitted %d", tc.pool, tc.submitted)
	}
	require.Equal(t, uint32(1), types.Required(1))
	require.Equal(t, uint32(2), types.Required(2))
	require.Equal(t, uint32(2), types.Required(3))
	require.Equal(t, uint32(50), types.Required(75))

	// The integer rule agrees with 3·submitted ≥ 2·pool for every small pool.
	for p := uint32(1); p <= 200; p++ {
		for s := uint32(0); s <= p; s++ {
			require.Equal(t, 3*s >= 2*p, types.QuorumMet(p, s), "pool %d submitted %d", p, s)
		}
	}
}

func TestScheduleRotation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		h      int64
		epochL uint64
		rotE   uint32
		window uint64
		r, o   int64
		wantOK bool
	}{
		{"first grid line past window", 0, 5, 2, 10, 20, 10, true},
		{"window just fits", 19, 5, 2, 10, 30, 20, true},
		{"exactly on a boundary needs the next", 20, 5, 2, 10, 40, 30, true},
		{"production-like", 71, 10, 7, 100, 210, 110, true},
		{"epoch length zero", 0, 0, 7, 100, 0, 0, false},
		{"period overflows int64", 5, 1 << 63, 7, 100, 0, 0, false},
		{"height near max", math.MaxInt64 - 50, 10, 7, 100, 0, 0, false},
		{"negative height", -1, 5, 2, 10, 0, 0, false},
		{"zero window", 0, 5, 2, 0, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, o, ok := types.ScheduleRotation(tc.h, tc.epochL, tc.rotE, tc.window)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.r, r)
			require.Equal(t, tc.o, o)
			if ok {
				require.Equal(t, int64(0), r%int64(tc.epochL*uint64(tc.rotE)))
				require.GreaterOrEqual(t, o, tc.h+1)
			}
		})
	}
}

func TestDecodeHex(t *testing.T) {
	b, err := types.DecodeHex("0aff", 2)
	require.NoError(t, err)
	require.Equal(t, []byte{0x0a, 0xff}, b)
	for _, bad := range []string{"0AFF", "0af", "0aff00", "zzzz", ""} {
		_, err := types.DecodeHex(bad, 2)
		require.ErrorIs(t, err, types.ErrInvalidRandomness, bad)
	}
}

func TestDefaultInitialInput(t *testing.T) {
	h := sha256.Sum256([]byte("saicon/randomness/initial-input/v1"))
	want := ""
	for _, c := range h {
		want += string("0123456789abcdef"[c>>4]) + string("0123456789abcdef"[c&15])
	}
	require.Equal(t, want, types.DefaultInitialInput())
}

func TestFirstRound(t *testing.T) {
	in := types.DefaultInitialInput()
	r := types.FirstRound(in)
	require.Equal(t, types.Round{Number: 1, Input: in, Status: types.RoundStatus_ROUND_STATUS_SCHEDULED}, r)
}
