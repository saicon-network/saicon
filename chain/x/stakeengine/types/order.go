package types

import (
	"sort"

	"cosmossdk.io/math"
)

// Quality is what the queue orders a validator by. Register Q6 was decided as
// "by validator quality" (owner, 2026-09-28). WARP does not exist on-chain until
// S11, so today quality is the last closed epoch's participation score, then the
// accumulated empirical points. S11 replaces the score with WARP here and nowhere
// else.
type Quality struct {
	Score math.LegacyDec
	EP    math.Int
}

// SortQueue returns entries in fill order: target validator's score descending,
// then its EP descending, then enqueued height ascending, then id ascending. The
// last two make the order total, so every node fills the same entries. A validator
// with no score yet ranks after every scored one. The input is not modified.
func SortQueue(entries []QueueEntry, quality func(validator string) Quality) []QueueEntry {
	out := append([]QueueEntry(nil), entries...)
	cache := make(map[string]Quality, len(out))
	get := func(v string) Quality {
		if x, ok := cache[v]; ok {
			return x
		}
		x := quality(v)
		if x.Score.IsNil() {
			x.Score = math.LegacyZeroDec()
		}
		if x.EP.IsNil() {
			x.EP = math.ZeroInt()
		}
		cache[v] = x
		return x
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := get(out[i].Validator), get(out[j].Validator)
		if !a.Score.Equal(b.Score) {
			return a.Score.GT(b.Score)
		}
		if !a.EP.Equal(b.EP) {
			return a.EP.GT(b.EP)
		}
		if out[i].EnqueuedHeight != out[j].EnqueuedHeight {
			return out[i].EnqueuedHeight < out[j].EnqueuedHeight
		}
		return out[i].Id < out[j].Id
	})
	return out
}
