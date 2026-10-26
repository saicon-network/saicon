// Package follow walks the chain block by block and records one fact per
// block: who proposed it. That is the only number this service originates —
// everything else it serves is read back from the chain — so the write path is
// deliberately small and its restart behaviour is the thing under test.
package follow

import (
	"context"
	"fmt"

	"github.com/saicon-network/saicon/indexer/internal/store"
)

// Source is the slice of the RPC client the follower needs. An interface, so
// the tests can own the chain without a running node.
type Source interface {
	Tip(ctx context.Context) (int64, error)
	ProposerAt(ctx context.Context, height int64) (string, error)
}

type Follower struct {
	s        *store.Store
	src      Source
	epochLen uint64
}

func New(s *store.Store, src Source, epochLen uint64) *Follower {
	return &Follower{s: s, src: src, epochLen: epochLen}
}

// ResetError reports that the chain is behind the index: a reset that reused the
// chain id, or a URL pointing somewhere else. The operator has to decide which,
// because discarding the index automatically would throw away the proposal counts
// that are the one thing here no query can rebuild.
type ResetError struct {
	ChainID string
	Cursor  int64
	Tip     int64
}

func (e *ResetError) Error() string {
	return fmt.Sprintf("chain %s is at height %d but the index is at %d: the chain was reset or the node URL changed; "+
		"start a fresh database, or keep this one and point it back at the original chain",
		e.ChainID, e.Tip, e.Cursor)
}

// EpochOf maps a height to the epoch it belongs to, using the chain's own
// numbering — not a convenient one.
//
// x/participation closes an epoch in EndBlock (chain/x/participation/module/module.go:187):
//
//	if p.EpochLengthBlocks == 0 || h <= 1 || h%int64(p.EpochLengthBlocks) != 0 { return nil }
//	epochID := uint64(h) / p.EpochLengthBlocks
//
// So with a 100-block epoch, epoch 1 is heights 1..100, epoch 2 is 101..200, and
// there is no epoch 0 — verified on the localnet, which answers 404 for epoch 0
// and 200 for epochs 1 through 4.
func EpochOf(height int64, epochLen uint64) uint64 {
	if epochLen == 0 || height < 1 {
		return 0
	}
	return uint64(height-1)/epochLen + 1
}

// LastClosedEpoch is the most recent epoch the chain has actually computed, which
// is the chain's own expression verbatim: height / epochLen. At height 471 with a
// 100-block epoch that is 4, while EpochOf(471) is 5 — the epoch still in progress.
// Asking the chain about an epoch it has not closed returns 404 for every query,
// which this service would faithfully record as a row of NULLs.
func LastClosedEpoch(height int64, epochLen uint64) uint64 {
	// height <= 1 mirrors the chain's own `h <= 1` guard. It matters only when
	// epochLen is 1, where height/epochLen would otherwise claim epoch 1 had
	// closed at height 1 — a close the chain skips and therefore never performs,
	// leaving that epoch unclosed for ever. At epochLen 100 the division already
	// answers 0 here.
	if epochLen == 0 || height <= 1 {
		return 0
	}
	return uint64(height) / epochLen
}

// Once walks from the stored cursor to the current tip and returns how many
// blocks it counted.
func (f *Follower) Once(ctx context.Context, chainID string) (int64, error) {
	cursor, err := f.s.IndexedHeight(ctx, chainID)
	if err != nil {
		return 0, err
	}
	tip, err := f.src.Tip(ctx)
	if err != nil {
		return 0, fmt.Errorf("tip: %w", err)
	}

	// A tip below the cursor means the chain went backwards, which a chain cannot
	// do: it is a reset that kept its chain id, or the URL now points at a
	// different node. Without this check the loop simply has nothing to do — it
	// returns 0 blocks, no error, for ever, and a stalled index is indistinguishable
	// from a quiet chain. The devnet WILL be reset and it WILL keep its id, so this
	// is the expected path, not a defensive one.
	if tip < cursor {
		return 0, &ResetError{ChainID: chainID, Cursor: cursor, Tip: tip}
	}

	var counted int64
	for h := cursor + 1; h <= tip; h++ {
		proposer, err := f.src.ProposerAt(ctx, h)
		if err != nil {
			// Stop at the first gap rather than skipping it: a hole in the
			// count is invisible afterwards, and the next pass will retry
			// from the same cursor.
			return counted, fmt.Errorf("height %d: %w", h, err)
		}
		if err := f.s.RecordBlock(ctx, chainID, h, EpochOf(h, f.epochLen), proposer); err != nil {
			return counted, err
		}
		counted++
	}
	return counted, nil
}

// There is deliberately no Run here. The binary owns the loop, because the
// follower and the epoch reader have to run in the same goroutine to keep the
// single-writer promise RecordBlock depends on. A second loop living in this
// package would be a copy nothing calls — and the copy this plan first carried
// claimed in its comment to retry a failed pass on the next tick while actually
// returning on the first error.
