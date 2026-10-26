package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// EscrowInvariant is V-10, queue escrow conservation: the stakeengine module
// balance equals the sum of the queue's amounts, exactly. The escrow is a
// blocked address, so nothing but Enqueue, Cancel and a fill moves its coins.
func (k Keeper) EscrowInvariant(ctx context.Context) (string, bool) {
	held := k.bankKeeper.GetBalance(ctx, k.EscrowAddress(), types.BondDenom).Amount
	owed := math.ZeroInt()
	want := map[[2]string]uint64{} // (delegator, validator) → entries
	if err := k.Queue.Walk(ctx, nil, func(_ uint64, e types.QueueEntry) (bool, error) {
		owed = owed.Add(e.Amount)
		want[[2]string{e.Delegator, e.Validator}]++
		return false, nil
	}); err != nil {
		return fmt.Sprintf("V-10: cannot read the stake queue: %v", err), true
	}
	if !held.Equal(owed) {
		return fmt.Sprintf("V-10 broken: the stakeengine escrow holds %s nscn, the queue is owed %s nscn", held, owed), true
	}
	// The per-delegator index must match the queue, both ways.
	have := 0
	var msg string
	if err := k.QueueByDelegator.Walk(ctx, nil, func(key collections.Pair[string, string], n uint64) (bool, error) {
		have++
		if w := want[[2]string{key.K1(), key.K2()}]; w != n {
			msg = fmt.Sprintf("V-10 broken: the queue index holds %d for (%s, %s), the queue has %d", n, key.K1(), key.K2(), w)
			return true, nil
		}
		return false, nil
	}); err != nil {
		return fmt.Sprintf("V-10: cannot read the queue index: %v", err), true
	}
	if msg != "" {
		return msg, true
	}
	if have != len(want) {
		return fmt.Sprintf("V-10 broken: the queue index has %d rows, the queue has %d (delegator, validator) pairs", have, len(want)), true
	}
	return "", false
}
