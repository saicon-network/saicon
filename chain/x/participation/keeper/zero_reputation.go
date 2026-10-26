package keeper

import (
	"context"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// ZeroReputation is the single write path by which a PUNISHMENT may touch the
// participation layer (ADR-0010 §4, the revised invariant V-8).
//
// The revised V-8 reads:
//
//	EP changes only via participation (ComputeEpoch); punishment never mutates
//	EP. Punishment MAY zero WARP and the in-flight epoch's PS snapshot — never
//	EP, never a closed epoch's snapshot.
//
// Having exactly one named method for this is the point. V-8's enforcement is
// "by construction plus test": the property holds because no punishment path can
// reach SetEP, and that is only checkable if punishment has one door. A second
// door added later would make the invariant a property of every caller instead
// of a property of this file.
//
// # Why EP is untouched
//
// Zeroing EP would add no deterrence for a double-signer — C=0, STATUS_JAILED
// and a permanent tombstone already make the validator permanently
// reward-ineligible, and every route back to ACTIVE is closed to it (MsgUnjail
// refuses a tombstoned validator, MsgClearFlag refuses a JAILED one, and the
// bond hook skips JAILED). What it would cost is real: EP is the only long-run
// participation quantity the chain keeps, so rewriting it makes "how much did
// this validator actually participate?" unanswerable afterwards, and it would
// be the single code path breaching decayEP's floor of 1. It also buys nothing
// against Sybil re-entry, since EP is keyed by operator address and a fresh
// valoper starts at zero regardless — that is B3's job, not slashing's.
//
// # Why the snapshot is zeroed rather than deleted
//
// A PS snapshot is an immutable per-epoch record, so "PS → 0" can only mean the
// IN-FLIGHT epoch's record. Zeroing keeps the row, so the epoch remains
// explicable after the fact — a deleted row is indistinguishable from a
// validator that was never scored. From the following epoch on, invariant V-7
// already guarantees no snapshot exists for a non-ACTIVE validator, so PS is
// zero by absence without anything having to write it.
//
// # WARP
//
// WARP → 0 is the load-bearing half of the Register's rule, because WARP is
// persistent smoothed state that would otherwise recover at 0.10 per epoch. The
// WARP engine does not exist yet (ADR-0009), so there is nothing to zero today;
// when it ships, it is zeroed HERE and nowhere else.
//
// A validator with no snapshot for this epoch is a no-op: this runs inside a
// slashing hook, where returning an error would halt the chain.
func (k Keeper) ZeroReputation(ctx context.Context, addr sdk.ValAddress, epoch uint64) error {
	snap, found, err := k.GetSnapshot(ctx, epoch, addr)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	snap.ComputedScore = math.LegacyZeroDec()
	snap.UptimeFraction = math.LegacyZeroDec()

	// blocks_signed / blocks_missed / votes_participated are left as recorded:
	// they are measurements of what happened, not a score. Rewriting them would
	// falsify the audit trail rather than punish.

	return k.SetSnapshot(ctx, epoch, addr, snap)
}
