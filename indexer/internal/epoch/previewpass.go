package epoch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

// The preview pass's limits. It runs in the indexing goroutine, after the epoch
// pass, so what it spends is what the follower waits for each tick.
const (
	// PreviewRequestTimeout bounds one read. A preview is one query; a node
	// that has not answered by then is treated as a transient failure.
	PreviewRequestTimeout = 3 * time.Second
	// PreviewPassTime bounds a pass: a read starts only while a whole
	// PreviewRequestTimeout still fits in it, so a pass ends within this time.
	PreviewPassTime = 5 * time.Second
	// PreviewMaxAttempts is when the pass gives up on an epoch and stores
	// chain.PreviewError: that many transient failures, retried with backoff.
	PreviewMaxAttempts = 5
	// previewDueBatch is how many due epochs one pass lists.
	previewDueBatch = 500
)

// PreviewSource is the slice of the REST client the preview pass needs.
type PreviewSource interface {
	VotingPowerPreviewAt(ctx context.Context, height int64) (*chain.Preview, error)
}

// PreviewPass reads x/rewards' voting-power preview of every epoch the epoch
// reader finished, pinned at the epoch's last height H = epoch·L, and stores it.
//
// A read ends in one of three ways. A preview is stored. A definitive answer
// that there is none (chain.PreviewUnavailable: route_missing, params_invalid,
// caps_missing, pruned) is stored as that code, and the epoch is never asked
// again. Any other failure is transient: the epoch stays due and is read again
// with the concentration pass's re-read rule — it waits BackoffPasses passes,
// and MaxFailures failures in a row end the pass — until PreviewMaxAttempts
// failures, when it is stored as chain.PreviewError.
type PreviewPass struct {
	s        *store.Store
	src      PreviewSource
	chainID  string
	epochLen uint64
	logf     func(string, ...any)
	now      func() time.Time

	// BackoffPasses and MaxFailures are the re-read rule, set by the caller to
	// the concentration pass's own values.
	BackoffPasses int
	MaxFailures   int
	// RequestTimeout, PassTime and MaxAttempts default to the constants above.
	RequestTimeout time.Duration
	PassTime       time.Duration
	MaxAttempts    int

	passes  int
	retryAt map[uint64]int
}

// NewPreviewPass wires a pass to its store and node.
func NewPreviewPass(s *store.Store, src PreviewSource, chainID string, epochLen uint64,
	backoffPasses, maxFailures int) *PreviewPass {
	return &PreviewPass{
		s: s, src: src, chainID: chainID, epochLen: epochLen, logf: log.Printf, now: time.Now,
		BackoffPasses: backoffPasses, MaxFailures: maxFailures,
		RequestTimeout: PreviewRequestTimeout, PassTime: PreviewPassTime, MaxAttempts: PreviewMaxAttempts,
		retryAt: map[uint64]int{},
	}
}

// SetLogf replaces where the pass logs, so a test can read what it said.
func (p *PreviewPass) SetLogf(f func(string, ...any)) { p.logf = f }

// SetClock replaces the pass's clock, so a test can drive its time limit.
func (p *PreviewPass) SetClock(now func() time.Time) { p.now = now }

// Pass reads the due epochs, newest first, within the pass's time.
func (p *PreviewPass) Pass(ctx context.Context) {
	p.passes++
	start := p.now()
	due, err := p.s.DuePreviews(ctx, p.chainID, previewDueBatch)
	if err != nil {
		p.logf("preview: due list: %v", err)
		return
	}
	failures := 0
	for _, d := range due {
		if p.retryAt[d.Epoch] > p.passes {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if p.now().Sub(start)+p.RequestTimeout > p.PassTime {
			return // the rest wait for the next pass
		}
		timedOut, nodeErr, err := p.read(ctx, d.Epoch)
		if err != nil {
			p.logf("preview epoch %d: %v", d.Epoch, err) // the database, not the node
			return
		}
		if nodeErr == nil {
			failures = 0
			delete(p.retryAt, d.Epoch)
			continue
		}
		if ctx.Err() != nil {
			return // the shutdown, not the epoch, failed the read
		}
		if errors.Is(nodeErr, chain.ErrHeightAhead) {
			// The REST node lags the one the follower reads: it will reach the
			// height, so this is not a failed attempt. Ask again next pass.
			p.retryAt[d.Epoch] = p.passes + 1
			continue
		}
		if err := p.failed(ctx, d.Epoch, nodeErr); err != nil {
			p.logf("preview epoch %d: %v", d.Epoch, err)
			return
		}
		// A read that timed out says the node is not answering: the next would
		// wait out the same timeout, so the pass stops rather than spend it.
		if failures++; timedOut || failures >= p.MaxFailures {
			return
		}
	}
}

// failed counts a transient failure, and gives the epoch up as chain.PreviewError
// once it has failed MaxAttempts times. The error is the database's.
func (p *PreviewPass) failed(ctx context.Context, ep uint64, cause error) error {
	n, err := p.s.PreviewFailed(ctx, p.chainID, ep)
	if err != nil {
		return err
	}
	if n < p.MaxAttempts {
		p.retryAt[ep] = p.passes + p.BackoffPasses
		p.logf("preview epoch %d: %v (attempt %d of %d, read again in %d passes)", ep, cause, n, p.MaxAttempts, p.BackoffPasses)
		return nil
	}
	delete(p.retryAt, ep)
	p.logf("preview epoch %d: %v (attempt %d of %d): stored as %q", ep, cause, n, p.MaxAttempts, chain.PreviewError)
	code := chain.PreviewError
	return p.s.WritePreview(ctx, p.chainID, store.PreviewEpochRow{Epoch: ep, Height: p.height(ep), Unavailable: &code}, nil)
}

func (p *PreviewPass) height(ep uint64) int64 { return int64(ep * p.epochLen) }

// read reads one epoch's preview and stores how it ended. nodeErr is a
// transient failure, and the epoch stays due; timedOut says the read ran out of
// its own time. err is a failure of the database.
func (p *PreviewPass) read(ctx context.Context, ep uint64) (timedOut bool, nodeErr, err error) {
	h := p.height(ep)
	rctx, cancel := context.WithTimeout(ctx, p.RequestTimeout)
	pv, nodeErr := p.src.VotingPowerPreviewAt(rctx, h)
	timedOut = errors.Is(rctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	cancel()

	row := store.PreviewEpochRow{Epoch: ep, Height: h}
	var un *chain.PreviewUnavailable
	switch {
	case errors.As(nodeErr, &un):
		code := un.Code
		row.Unavailable = &code
		return false, nil, p.s.WritePreview(ctx, p.chainID, row, nil)
	case nodeErr != nil:
		return timedOut, nodeErr, nil
	}
	ops, err := p.s.Operators(ctx, p.chainID)
	if err != nil {
		return false, nil, err
	}
	// A preview that does not fit the epoch (another epoch, a validator twice,
	// an address that does not decode) is a bad answer, so transient too.
	rows, nodeErr := previewRows(pv, ep, ops)
	if nodeErr != nil {
		return false, nodeErr, nil
	}
	total, threshold, valCap := pv.TotalVP, pv.Threshold, pv.Cap
	row.TotalVP, row.Threshold, row.Cap = &total, &threshold, &valCap
	return false, nil, p.s.WritePreview(ctx, p.chainID, row, rows)
}

// previewRows turns the chain's preview into rows, unchanged but for the
// operator, which is written the way x/staking names the validator (ops) so the
// scoreboard's join on the operator string finds it. The two are matched on
// address bytes: the preview could name the validator with another prefix.
func previewRows(p *chain.Preview, ep uint64, ops []string) ([]store.PreviewRow, error) {
	if p.Epoch != ep {
		return nil, fmt.Errorf("the preview read at height %d names epoch %d, not %d", p.Height, p.Epoch, ep)
	}
	staking := make(map[string]string, len(ops))
	for _, op := range ops {
		k, err := chain.AddrKey(op)
		if err != nil {
			return nil, fmt.Errorf("operator address %q: %w", op, err)
		}
		staking[k] = op
	}
	seen := make(map[string]bool, len(p.Validators))
	rows := make([]store.PreviewRow, 0, len(p.Validators))
	for _, v := range p.Validators {
		k, err := chain.AddrKey(v.Operator)
		if err != nil {
			return nil, fmt.Errorf("preview operator %q: %w", v.Operator, err)
		}
		if seen[k] {
			return nil, fmt.Errorf("preview lists validator %s twice", v.Operator)
		}
		seen[k] = true
		op := v.Operator
		if name, ok := staking[k]; ok {
			op = name
		}
		row := store.PreviewRow{Operator: op, VP: v.VP, VPShare: v.VPShare, StockShare: v.StockShare,
			Eligible: v.Eligible, Warp: v.Warp, WarpMeasured: v.WarpMeasured}
		if v.Reason != "" {
			reason := v.Reason
			row.Reason = &reason
		}
		rows = append(rows, row)
	}
	return rows, nil
}
