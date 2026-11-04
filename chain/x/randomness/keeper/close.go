package keeper

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

// maxSkipReasonBytes bounds a stored skip reason. The V-14 message grows with
// the number of violations; the record, the event and the log line must not.
const maxSkipReasonBytes = 512

// capReason shortens a skip reason to maxSkipReasonBytes, cutting on a rune
// boundary and marking the cut.
func capReason(s string) string {
	if len(s) <= maxSkipReasonBytes {
		return s
	}
	const mark = "..."
	return strings.ToValidUTF8(s[:maxSkipReasonBytes-len(mark)], "") + mark
}

// closeRound closes round r at its rotation height (spec §9): it checks V-14,
// computes the seed (every round, drawn or not), decides DRAWN or SKIPPED,
// writes the round's record, trims the history, clears the pool and the
// submissions, and creates the next round, whose input is this round's seed.
// It returns the next round, still unscheduled; EndBlock schedules it in the
// same block.
//
// Everything runs in a cache context written only on success, so a store error
// leaves the round untouched and the next block tries again.
func (k Keeper) closeRound(ctx context.Context, r types.Round) (types.Round, error) {
	cctx, write := sdk.UnwrapSDKContext(ctx).CacheContext()

	// V-14 first, before anything is read for the outcome.
	invariantMsg, broken := k.RandomnessInvariant(cctx)

	poolIDs, keyed, err := k.poolOf(cctx)
	if err != nil {
		return types.Round{}, err
	}
	hexOutputs, err := k.submittedOutputs(cctx)
	if err != nil {
		return types.Round{}, err
	}
	input, err := types.DecodeHex(r.Input, 32)
	if err != nil {
		return types.Round{}, fmt.Errorf("round %d input: %w", r.Number, err)
	}
	// Only outputs of exactly vrf.OutputSize bytes enter the seed (Seed's
	// encoding is injective only for fixed-size outputs). A malformed stored
	// output is a V-14 break; it is dropped here and the round is skipped.
	var outputs [][]byte
	var outputHex []string
	for _, o := range hexOutputs.values {
		b, err := types.DecodeHex(o, vrf.OutputSize)
		if err != nil {
			continue
		}
		outputs = append(outputs, b)
		outputHex = append(outputHex, o)
	}
	sort.Strings(outputHex)

	seed := types.Seed(r.Number, input, outputs)
	pool := uint32(len(poolIDs))
	submitted := hexOutputs.count
	required := types.Required(pool)

	draw := types.Draw{
		Round:            r.Number,
		Input:            r.Input,
		Seed:             hex.EncodeToString(seed),
		WindowOpenHeight: r.WindowOpenHeight,
		RotationHeight:   r.RotationHeight,
		Pool:             poolIDs,
		Keyed:            keyed,
		Outputs:          outputHex,
		Submitted:        submitted,
		Required:         required,
	}
	// A V-14 break wins over every ordinary reason, a window that never opened
	// included, so the record and the error log carry the violation (spec §15).
	switch {
	case broken:
		draw.SkipReason = "V-14: " + invariantMsg
	case r.Status != types.RoundStatus_ROUND_STATUS_OPEN:
		draw.SkipReason = "window never opened"
	case pool == 0:
		draw.SkipReason = "pool is empty"
	case !types.QuorumMet(pool, submitted):
		draw.SkipReason = fmt.Sprintf("quorum not met: %d of %d submitted, %d required (%d had an eligible key)",
			submitted, pool, required, keyed)
	}
	draw.SkipReason = capReason(draw.SkipReason)
	if draw.SkipReason == "" {
		draw.Outcome = types.DrawOutcome_DRAW_OUTCOME_DRAWN
		draw.Winners = types.Winners(seed, poolIDs)
		draw.CohortRound = r.Number
	} else {
		draw.Outcome = types.DrawOutcome_DRAW_OUTCOME_SKIPPED
		draw.CohortRound, err = k.previousCohort(cctx, r.Number)
		if err != nil {
			return types.Round{}, err
		}
	}

	if err := k.draws.Set(cctx, r.Number, draw); err != nil {
		return types.Round{}, err
	}
	if r.Number > types.MaxDrawHistory {
		if err := k.draws.Remove(cctx, r.Number-types.MaxDrawHistory); err != nil {
			return types.Round{}, err
		}
	}
	for _, wipe := range []func() error{
		func() error { return k.Pool.Clear(cctx, nil) },
		func() error { return k.Designated.Clear(cctx, nil) },
		func() error { return k.Submissions.Clear(cctx, nil) },
	} {
		if err := wipe(); err != nil {
			return types.Round{}, err
		}
	}
	next := types.Round{
		Number:          r.Number + 1,
		Input:           draw.Seed,
		KeyCutoffHeight: r.WindowOpenHeight,
		Status:          types.RoundStatus_ROUND_STATUS_SCHEDULED,
	}
	if err := k.CurrentRound.Set(cctx, next); err != nil {
		return types.Round{}, err
	}

	em := cctx.EventManager()
	em.EmitEvent(sdk.NewEvent(
		types.EventTypeSeedComputed,
		sdk.NewAttribute(types.AttributeKeyRound, strconv.FormatUint(r.Number, 10)),
		sdk.NewAttribute(types.AttributeKeySeed, draw.Seed),
		sdk.NewAttribute(types.AttributeKeySubmitted, strconv.FormatUint(uint64(submitted), 10)),
		sdk.NewAttribute(types.AttributeKeyPoolSize, strconv.FormatUint(uint64(pool), 10)),
	))
	if draw.Outcome == types.DrawOutcome_DRAW_OUTCOME_DRAWN {
		em.EmitEvent(sdk.NewEvent(
			types.EventTypeShadowDraw,
			sdk.NewAttribute(types.AttributeKeyRound, strconv.FormatUint(r.Number, 10)),
			sdk.NewAttribute(types.AttributeKeySeed, draw.Seed),
			sdk.NewAttribute(types.AttributeKeyWinners, strings.Join(draw.Winners, ",")),
		))
	} else {
		// A broken invariant is corrupted state and must stand out (spec §15); a
		// missed quorum, an empty pool or a window that never opened is ordinary.
		logger := sdk.UnwrapSDKContext(ctx).Logger()
		if broken {
			logger.Error("randomness: draw skipped, V-14 broken", "round", r.Number, "reason", draw.SkipReason)
		} else {
			logger.Info("randomness: draw skipped", "round", r.Number, "reason", draw.SkipReason)
		}
		em.EmitEvent(sdk.NewEvent(
			types.EventTypeDrawSkipped,
			sdk.NewAttribute(types.AttributeKeyRound, strconv.FormatUint(r.Number, 10)),
			sdk.NewAttribute(types.AttributeKeyReason, draw.SkipReason),
			sdk.NewAttribute(types.AttributeKeySubmitted, strconv.FormatUint(uint64(submitted), 10)),
			sdk.NewAttribute(types.AttributeKeyRequired, strconv.FormatUint(uint64(required), 10)),
			sdk.NewAttribute(types.AttributeKeyPoolSize, strconv.FormatUint(uint64(pool), 10)),
			sdk.NewAttribute(types.AttributeKeyCohortRound, strconv.FormatUint(draw.CohortRound, 10)),
		))
	}
	write()
	return next, nil
}

// poolOf lists the pool's entity ids in key (= sorted) order and counts the
// entries that have a designated submitter.
func (k Keeper) poolOf(ctx context.Context) (ids []string, keyed uint32, err error) {
	err = k.Pool.Walk(ctx, nil, func(id string, e types.PoolEntry) (bool, error) {
		ids = append(ids, id)
		if e.Validator != "" {
			keyed++
		}
		return false, nil
	})
	return ids, keyed, err
}

// submissionOutputs is the stored outputs of a round's submissions, in entity
// id order, with their count.
type submissionOutputs struct {
	values []string
	count  uint32
}

func (k Keeper) submittedOutputs(ctx context.Context) (submissionOutputs, error) {
	var out submissionOutputs
	err := k.Submissions.Walk(ctx, nil, func(_ string, s types.Submission) (bool, error) {
		out.values = append(out.values, s.Output)
		out.count++
		return false, nil
	})
	return out, err
}

// previousCohort is the cohort_round of the preceding round's record, or 0 when
// there is none (round 1, or a record already trimmed from the history).
func (k Keeper) previousCohort(ctx context.Context, round uint64) (uint64, error) {
	if round < 2 {
		return 0, nil
	}
	prev, err := k.draws.Get(ctx, round-1)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return prev.CohortRound, nil
}
