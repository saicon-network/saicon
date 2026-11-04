package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

// maxViolationsShown is how many violations the invariant message spells out;
// the message always carries the full count.
const maxViolationsShown = 5

// RandomnessInvariant implements V-14, the randomness integrity invariant
// (spec §15), on the current round:
//
//	(a) the status is SCHEDULED or OPEN, and a scheduled round has
//	    key_cutoff_height <= window_open_height < rotation_height;
//	(b) Designated is exactly the pool entries that have a validator;
//	(c) every submission belongs to a pool entry whose validator is the
//	    submission's, was made at a height in [window_open, rotation), and its
//	    proof re-verifies against the entry's key and the round alpha to the
//	    stored output;
//	(d) there are at most as many submissions as pool entries.
//
// It is read-only and never panics. Unlike V-12 it is not a halting invariant:
// the rotation EndBlock calls it and, on a break, skips the draw (K12). A read
// error is reported as broken.
func (k Keeper) RandomnessInvariant(ctx context.Context) (string, bool) {
	msg, broken, err := k.checkRandomness(ctx)
	if err != nil {
		return fmt.Sprintf("read error: %v", err), true
	}
	return msg, broken
}

func (k Keeper) checkRandomness(ctx context.Context) (string, bool, error) {
	var violations []string
	add := func(format string, args ...any) { violations = append(violations, fmt.Sprintf(format, args...)) }

	r, err := k.CurrentRound.Get(ctx)
	if err != nil {
		return "", false, fmt.Errorf("current round: %w", err)
	}

	// (a)
	if r.Status != types.RoundStatus_ROUND_STATUS_SCHEDULED && r.Status != types.RoundStatus_ROUND_STATUS_OPEN {
		add("(a) round %d has status %s", r.Number, r.Status)
	}
	if r.RotationHeight > 0 && (r.KeyCutoffHeight > r.WindowOpenHeight || r.WindowOpenHeight >= r.RotationHeight) {
		add("(a) round %d heights: key_cutoff %d, window_open %d, rotation %d",
			r.Number, r.KeyCutoffHeight, r.WindowOpenHeight, r.RotationHeight)
	}

	pool := map[string]types.PoolEntry{}
	var poolIDs []string
	if err := k.Pool.Walk(ctx, nil, func(id string, e types.PoolEntry) (bool, error) {
		pool[id] = e
		poolIDs = append(poolIDs, id)
		return false, nil
	}); err != nil {
		return "", false, fmt.Errorf("pool: %w", err)
	}

	// (b) both directions, in key order.
	for _, id := range poolIDs {
		e := pool[id]
		if e.Validator == "" {
			continue
		}
		v, err := sdk.ValAddressFromBech32(e.Validator)
		if err != nil {
			add("(b) pool entry %s has an undecodable validator %q", id, e.Validator)
			continue
		}
		got, err := k.Designated.Get(ctx, v)
		switch {
		case err != nil && !isNotFound(err):
			return "", false, fmt.Errorf("designated: %w", err)
		case err != nil:
			add("(b) pool entry %s names %s, which is not in Designated", id, e.Validator)
		case got != id:
			add("(b) Designated[%s] is %s, pool entry %s names it", e.Validator, got, id)
		}
	}
	if err := k.Designated.Walk(ctx, nil, func(key []byte, id string) (bool, error) {
		e, ok := pool[id]
		if !ok || e.Validator == "" {
			add("(b) Designated[%s] names %s, which has no designated pool entry", sdk.ValAddress(key), id)
			return false, nil
		}
		if v, err := sdk.ValAddressFromBech32(e.Validator); err != nil || !bytes.Equal(v, key) {
			add("(b) Designated[%s] names %s, whose validator is %s", sdk.ValAddress(key), id, e.Validator)
		}
		return false, nil
	}); err != nil {
		return "", false, fmt.Errorf("designated: %w", err)
	}

	// (c) and (d)
	input, inputErr := types.DecodeHex(r.Input, 32)
	if inputErr != nil {
		add("(c) round %d input does not decode: %v", r.Number, inputErr)
	}
	chainID := sdk.UnwrapSDKContext(ctx).ChainID()
	count := 0
	if err := k.Submissions.Walk(ctx, nil, func(id string, s types.Submission) (bool, error) {
		count++
		e, ok := pool[id]
		switch {
		case !ok:
			add("(c) submission %s has no pool entry", id)
			return false, nil
		case e.Validator == "" || e.Validator != s.Validator:
			add("(c) submission %s is by %q, the pool entry's validator is %q", id, s.Validator, e.Validator)
			return false, nil
		}
		if s.Height < r.WindowOpenHeight || s.Height >= r.RotationHeight {
			add("(c) submission %s at height %d is outside [%d, %d)", id, s.Height, r.WindowOpenHeight, r.RotationHeight)
		}
		if inputErr != nil {
			return false, nil
		}
		pub, err := types.DecodeHex(e.VrfPublicKey, vrf.PublicKeySize)
		if err != nil {
			add("(c) pool entry %s key: %v", id, err)
			return false, nil
		}
		proof, err := types.DecodeHex(s.Proof, vrf.ProofSize)
		if err != nil {
			add("(c) submission %s proof: %v", id, err)
			return false, nil
		}
		out, err := vrf.Verify(pub, proof, types.RoundAlpha(chainID, r.Number, input))
		switch {
		case err != nil:
			add("(c) submission %s proof does not verify: %v", id, err)
		case hex.EncodeToString(out) != s.Output:
			add("(c) submission %s stored output differs from the verified output", id)
		}
		return false, nil
	}); err != nil {
		return "", false, fmt.Errorf("submissions: %w", err)
	}
	if count > len(poolIDs) {
		add("(d) %d submissions for %d pool entries", count, len(poolIDs))
	}

	if len(violations) == 0 {
		return "randomness V-14: round consistent", false, nil
	}
	shown := violations
	if len(shown) > maxViolationsShown {
		shown = shown[:maxViolationsShown]
	}
	msg := fmt.Sprintf("%d violation(s): %s", len(violations), strings.Join(shown, "; "))
	if more := len(violations) - len(shown); more > 0 {
		msg += fmt.Sprintf("; and %d more", more)
	}
	return msg, true, nil
}

func isNotFound(err error) bool { return errors.Is(err, collections.ErrNotFound) }
