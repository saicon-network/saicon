package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/bits"
	"sort"
)

// RoundAlpha is the VRF input of round N (spec §5.3):
// AlphaDomain ‖ len16(chainID) ‖ chainID ‖ u64(N) ‖ input_N.
func RoundAlpha(chainID string, round uint64, input []byte) []byte {
	b := make([]byte, 0, len(AlphaDomain)+2+len(chainID)+8+len(input))
	b = append(b, AlphaDomain...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(chainID)))
	b = append(b, chainID...)
	b = binary.BigEndian.AppendUint64(b, round)
	return append(b, input...)
}

// Seed is seed_N = SHA-256(SeedDomain ‖ u64(N) ‖ input ‖ u32(n) ‖ sorted outputs),
// computed every round, drawn or not (spec §9.1). It sorts a copy: neither
// input nor outputs is modified. Every output must be vrf.OutputSize bytes:
// the encoding is injective only for fixed-size outputs, so callers decode
// them with DecodeHex(s, vrf.OutputSize) first.
func Seed(round uint64, input []byte, outputs [][]byte) []byte {
	sorted := make([][]byte, len(outputs))
	copy(sorted, outputs)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })

	h := sha256.New()
	h.Write([]byte(SeedDomain))
	h.Write(binary.BigEndian.AppendUint64(nil, round))
	h.Write(input)
	h.Write(binary.BigEndian.AppendUint32(nil, uint32(len(sorted))))
	for _, o := range sorted {
		h.Write(o)
	}
	return h.Sum(nil)
}

// DrawOrder sorts entity ids by SHA-256(DrawDomain ‖ seed ‖ id) ascending, ties
// by id (spec §10.1). It returns a new slice and leaves ids as it was.
func DrawOrder(seed []byte, ids []string) []string {
	type scored struct {
		id    string
		score [sha256.Size]byte
	}
	items := make([]scored, len(ids))
	for i, id := range ids {
		buf := make([]byte, 0, len(DrawDomain)+len(seed)+len(id))
		buf = append(buf, DrawDomain...)
		buf = append(buf, seed...)
		buf = append(buf, id...)
		items[i] = scored{id: id, score: sha256.Sum256(buf)}
	}
	sort.Slice(items, func(i, j int) bool {
		if c := bytes.Compare(items[i].score[:], items[j].score[:]); c != 0 {
			return c < 0
		}
		return items[i].id < items[j].id
	})
	order := make([]string, len(items))
	for i, it := range items {
		order[i] = it.id
	}
	return order
}

// Winners is the first min(ShadowLotterySeats, len(ids)) of DrawOrder.
func Winners(seed []byte, ids []string) []string {
	order := DrawOrder(seed, ids)
	if uint64(len(order)) > uint64(ShadowLotterySeats) {
		order = order[:ShadowLotterySeats]
	}
	return order
}

// Required is ⌈2·pool/3⌉ in integer arithmetic (spec §9.2).
func Required(pool uint32) uint32 {
	p := uint64(pool)
	return uint32((QuorumNumerator*p + QuorumDenominator - 1) / QuorumDenominator)
}

// QuorumMet reports pool ≥ 1 ∧ submitted ≥ Required(pool) (spec §9.2).
func QuorumMet(pool, submitted uint32) bool {
	return pool >= 1 && submitted >= Required(pool)
}

// ScheduleRotation returns the rotation height R — the smallest multiple of
// P = epochLen × rotationEpochs with R ≥ h + window + 1 — and the window open
// height O = R − window (spec §7.2). ok is false when P or window is 0, h is
// negative, or anything overflows int64.
func ScheduleRotation(h int64, epochLen uint64, rotationEpochs uint32, window uint64) (rotation, open int64, ok bool) {
	if h < 0 || epochLen == 0 || rotationEpochs == 0 || window == 0 {
		return 0, 0, false
	}
	hi, period := bits.Mul64(epochLen, uint64(rotationEpochs))
	if hi != 0 || period > math.MaxInt64 {
		return 0, 0, false
	}
	if window == math.MaxUint64 {
		return 0, 0, false
	}
	minR, carry := bits.Add64(uint64(h), window+1, 0)
	if carry != 0 {
		return 0, 0, false
	}
	q := minR / period
	if minR%period != 0 {
		q++
	}
	hi, r := bits.Mul64(q, period)
	if hi != 0 || r > math.MaxInt64 {
		return 0, 0, false
	}
	return int64(r), int64(r - window), true
}

// DefaultInitialInput is the hex of SHA-256(InitialInputDomain): the published
// constant that seeds round 1 when a genesis names no input (spec §14).
func DefaultInitialInput() string {
	h := sha256.Sum256([]byte(InitialInputDomain))
	return hex.EncodeToString(h[:])
}

// FirstRound is round 1: key cutoff 0, no heights, SCHEDULED.
func FirstRound(input string) Round {
	return Round{
		Number: 1,
		Input:  input,
		Status: RoundStatus_ROUND_STATUS_SCHEDULED,
	}
}
