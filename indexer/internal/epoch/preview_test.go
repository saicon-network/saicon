package epoch_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/epoch"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

// Two real, decodable operator addresses (from the localnet; see addr_test.go).
const (
	opA        = "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd"
	opAAccount = "saicon1yh0ceztycl23zyuy67776u5dtfjawnrfuvlyfs"
	opB        = "saiconvaloper1xnkhj9g5hyj5hm80uyvgqeucy83tx7xtcqd6rl"
)

// The concentration pass's re-read rule, as main.go passes it.
const (
	backoff     = 10
	maxFailures = 3
)

func twoValidators() *fakeREST {
	return &fakeREST{
		vals: []chain.Validator{
			{Operator: opA, Moniker: "a", Status: "BOND_STATUS_BONDED", Tokens: "100"},
			{Operator: opB, Moniker: "b", Status: "BOND_STATUS_BONDED", Tokens: "100"},
		},
		ep: map[string]*chain.EP{}, snap: map[string]*chain.Snapshot{},
		reward: map[string]*chain.Reward{}, compl: map[string]*bool{},
	}
}

func preview(height int64, epoch uint64, vs ...chain.PreviewEntry) *chain.Preview {
	return &chain.Preview{Height: height, Epoch: epoch, Threshold: "65.000000000000000000",
		Initial: "80.000000000000000000", Cap: "200000000000000", TotalVP: "85420000000000", Validators: vs}
}

func onePreview(h int64) *chain.Preview {
	return preview(h, uint64(h/100), chain.PreviewEntry{Operator: opA, VP: "1", VPShare: "1.000000",
		StockShare: "1.000000", Warp: "80", Eligible: true})
}

func strp(s string) *string { return &s }

// readEpochs runs the epoch reader over eps with a 100-block epoch, so each
// epoch is finished and on the preview pass's list.
func readEpochs(t *testing.T, s *store.Store, f *fakeREST, eps ...uint64) {
	t.Helper()
	for _, e := range eps {
		require.NoError(t, epoch.New(s, f, 100).ReadEpoch(context.Background(), "c1", e))
	}
}

// newPass returns a preview pass over f that records what it logged.
func newPass(s *store.Store, f epoch.PreviewSource, logs *[]string) *epoch.PreviewPass {
	p := epoch.NewPreviewPass(s, f, "c1", 100, backoff, maxFailures)
	p.SetLogf(func(format string, args ...any) { *logs = append(*logs, fmt.Sprintf(format, args...)) })
	return p
}

func code(t *testing.T, s *store.Store, e uint64) (string, bool) {
	t.Helper()
	var c sql.NullString
	err := s.DB().QueryRowContext(context.Background(),
		`SELECT unavailable FROM vp_preview_epochs WHERE chain_id='c1' AND epoch=?`, e).Scan(&c)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	require.NoError(t, err)
	if !c.Valid {
		return "available", true
	}
	return c.String, true
}

func due(t *testing.T, s *store.Store) []store.DuePreview {
	t.Helper()
	d, err := s.DuePreviews(context.Background(), "c1", 100)
	require.NoError(t, err)
	return d
}

func count(t *testing.T, s *store.Store, q string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, s.DB().QueryRowContext(context.Background(), q, args...).Scan(&n))
	return n
}

// R1: the snapshot's WARP lands in epoch_snapshots.warp exactly as the chain wrote
// it, and a snapshot without one leaves NULL, never 0.
func TestSnapshotWarpIsStoredVerbatimAndNullWhenAbsent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	f := twoValidators()
	f.snap[opA] = &chain.Snapshot{ComputedScore: "1.000000000000000000", Warp: strp("85.420000000000000000")}
	f.snap[opB] = &chain.Snapshot{ComputedScore: "1.000000000000000000"} // a pre-WARP snapshot
	require.NoError(t, epoch.New(s, f, 10).ReadEpoch(ctx, "c1", 3))

	var a, b sql.NullString
	require.NoError(t, s.DB().QueryRowContext(ctx, `SELECT warp FROM epoch_snapshots WHERE epoch=3 AND operator=?`, opA).Scan(&a))
	require.NoError(t, s.DB().QueryRowContext(ctx, `SELECT warp FROM epoch_snapshots WHERE epoch=3 AND operator=?`, opB).Scan(&b))
	require.True(t, a.Valid)
	require.Equal(t, "85.420000000000000000", a.String, "not rounded, not re-rendered")
	require.False(t, b.Valid, "no WARP is NULL, not 0")
}

// L3: a WARP the client rejected is stored as NULL, and the reader says so.
func TestARejectedSnapshotWarpIsNullAndLogged(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	f := twoValidators()
	f.snap[opA] = &chain.Snapshot{ComputedScore: "1.000000000000000000", InvalidWarp: `"101"`}
	var logs []string
	r := epoch.New(s, f, 10)
	r.SetLogf(func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) })
	require.NoError(t, r.ReadEpoch(ctx, "c1", 3))

	var a sql.NullString
	require.NoError(t, s.DB().QueryRowContext(ctx, `SELECT warp FROM epoch_snapshots WHERE epoch=3 AND operator=?`, opA).Scan(&a))
	require.False(t, a.Valid)
	require.Len(t, logs, 1)
	require.Contains(t, logs[0], `snapshot warp "101" is not a decimal from 0 to 100`)
}

// The epoch reader does not read the preview: it puts the finished epoch on the
// preview pass's list, so a slow or failing preview route never holds it.
func TestTheEpochReaderOnlyListsThePreview(t *testing.T) {
	s := newStore(t)
	f := twoValidators()
	readEpochs(t, s, f, 7)
	require.Empty(t, f.previewHeights)
	require.Equal(t, []store.DuePreview{{Epoch: 7}}, due(t, s))
}

// R2: the preview is read once, pinned at H = e·L, and stored row by row as the
// chain wrote it: the shares are its strings, and an eligible validator's empty
// reason is NULL.
func TestPreviewIsReadPinnedAtTheEpochCloseAndStoredVerbatim(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	f := twoValidators()
	f.previewAt = func(h int64) (*chain.Preview, error) {
		return preview(h, 7,
			chain.PreviewEntry{Operator: opB, VP: "85420000000000", VPShare: "0.333333", StockShare: "0.500000",
				Warp: "85.420000000000000000", WarpMeasured: true, Eligible: true},
			// The preview names A with the account prefix: it must still join to x/staking's name.
			chain.PreviewEntry{Operator: opAAccount, VP: "28000000000000", VPShare: "0", StockShare: "0.500000",
				Warp: "28.000000000000000000", WarpMeasured: true, Eligible: false, Reason: "warp below threshold"},
		), nil
	}
	readEpochs(t, s, f, 7)
	var logs []string
	p := newPass(s, f, &logs)
	p.Pass(ctx)
	p.Pass(ctx)
	require.Equal(t, []int64{700}, f.previewHeights, "one read, pinned at the epoch's last height")
	require.Empty(t, due(t, s))
	require.Empty(t, logs)

	var height int64
	var c, total, threshold, valCap sql.NullString
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT height, unavailable, total_vp, threshold, cap FROM vp_preview_epochs WHERE chain_id='c1' AND epoch=7`).
		Scan(&height, &c, &total, &threshold, &valCap))
	require.Equal(t, int64(700), height)
	require.False(t, c.Valid)
	require.Equal(t, "85420000000000", total.String)
	require.Equal(t, "65.000000000000000000", threshold.String)
	require.Equal(t, "200000000000000", valCap.String)

	type row struct {
		vp, share, stock, warp string
		eligible, measured     bool
		reason                 sql.NullString
	}
	read := func(op string) row {
		var r row
		require.NoError(t, s.DB().QueryRowContext(ctx,
			`SELECT vp, vp_share, stock_share, warp, eligible, warp_measured, reason FROM vp_preview
			  WHERE chain_id='c1' AND epoch=7 AND operator=?`, op).
			Scan(&r.vp, &r.share, &r.stock, &r.warp, &r.eligible, &r.measured, &r.reason))
		return r
	}
	b := read(opB)
	require.Equal(t, "0.333333", b.share, "a share is the chain's string, never re-computed")
	require.Equal(t, "0.500000", b.stock)
	require.True(t, b.eligible)
	require.False(t, b.reason.Valid, "an eligible validator has no reason: NULL, not an empty string")

	a := read(opA) // stored under x/staking's name, not the preview's
	require.Equal(t, "0", a.share, "\"0\" stays \"0\"")
	require.Equal(t, "28000000000000", a.vp)
	require.False(t, a.eligible)
	require.Equal(t, "warp below threshold", a.reason.String)
}

// R2: each definitive answer is stored as the epoch's code, with no validator
// rows and nothing logged, and the epoch is never asked again.
func TestEveryDefinitiveCodeIsStoredOnce(t *testing.T) {
	codes := []string{chain.PreviewRouteMissing, chain.PreviewParamsInvalid, chain.PreviewCapsMissing, chain.PreviewPruned}
	for _, c := range codes {
		t.Run(c, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			f := twoValidators()
			f.previewAt = func(int64) (*chain.Preview, error) {
				return nil, &chain.PreviewUnavailable{Code: c, Err: errors.New("the node said " + c)}
			}
			readEpochs(t, s, f, 7)
			var logs []string
			p := newPass(s, f, &logs)
			for i := 0; i < 2*backoff; i++ {
				p.Pass(ctx)
			}
			require.Len(t, f.previewHeights, 1, "asked once")
			got, ok := code(t, s, 7)
			require.True(t, ok)
			require.Equal(t, c, got)
			require.Zero(t, count(t, s, `SELECT COUNT(*) FROM vp_preview`))
			require.Zero(t, count(t, s, `SELECT COUNT(*) FROM vp_preview_epochs WHERE total_vp IS NOT NULL`),
				"an unavailable preview has no total, not a total of 0")
			require.Empty(t, due(t, s))
			require.Empty(t, logs)
		})
	}
}

// M1: one transient failure does not lose the preview. The epoch stays due,
// waits the backoff, and is read again; the node has recovered, so it is stored.
func TestATransientFailureIsReadAgainAfterTheBackoff(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	f := twoValidators()
	fail := true
	f.previewAt = func(h int64) (*chain.Preview, error) {
		if fail {
			return nil, errors.New("HTTP 503 from the proxy")
		}
		return onePreview(h), nil
	}
	readEpochs(t, s, f, 7)
	var logs []string
	p := newPass(s, f, &logs)
	p.Pass(ctx)
	_, ok := code(t, s, 7)
	require.False(t, ok, "nothing stored after a transient failure")
	require.Equal(t, []store.DuePreview{{Epoch: 7, Attempts: 1}}, due(t, s))
	require.Len(t, logs, 1)
	require.Contains(t, logs[0], "attempt 1 of 5")

	fail = false
	for i := 0; i < backoff-1; i++ {
		p.Pass(ctx)
	}
	require.Len(t, f.previewHeights, 1, "not asked again inside the backoff")
	p.Pass(ctx)
	require.Len(t, f.previewHeights, 2)
	got, _ := code(t, s, 7)
	require.Equal(t, "available", got)
	require.Empty(t, due(t, s))
}

// M1: every kind of transient failure leaves the epoch due, never stored.
func TestTransientFailuresAreNeverStoredAtOnce(t *testing.T) {
	cases := map[string]func(h int64) (*chain.Preview, error){
		// A height not reached yet is not stored either, but it is not an
		// attempt at all: TestAHeightNotReachedIsNotAFailedAttempt.
		"a dropped connection": func(int64) (*chain.Preview, error) { return nil, errors.New("connection reset by peer") },
		"another epoch": func(h int64) (*chain.Preview, error) {
			return preview(h, 6, chain.PreviewEntry{Operator: opA, VP: "1", VPShare: "1.000000", StockShare: "1.000000", Warp: "80"}), nil
		},
		"a validator listed twice": func(h int64) (*chain.Preview, error) {
			e := chain.PreviewEntry{Operator: opA, VP: "1", VPShare: "1.000000", StockShare: "1.000000", Warp: "80"}
			dup := e
			dup.Operator = opAAccount // the same validator, under the other prefix
			return preview(h, 7, e, dup), nil
		},
		"an operator that does not decode": func(h int64) (*chain.Preview, error) {
			return preview(h, 7, chain.PreviewEntry{Operator: "saiconvaloper1nope", VP: "1", VPShare: "1.000000", StockShare: "1.000000", Warp: "80"}), nil
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			f := twoValidators()
			f.previewAt = answer
			readEpochs(t, s, f, 7)
			var logs []string
			newPass(s, f, &logs).Pass(context.Background())
			_, ok := code(t, s, 7)
			require.False(t, ok)
			require.Equal(t, []store.DuePreview{{Epoch: 7, Attempts: 1}}, due(t, s))
		})
	}
}

// M1: the pass gives up only after PreviewMaxAttempts failures, each after the
// backoff, and then stores "error" and says why.
func TestThePassGivesUpAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	f := twoValidators()
	f.previewAt = func(int64) (*chain.Preview, error) { return nil, errors.New("HTTP 502 bad gateway") }
	readEpochs(t, s, f, 7)
	var logs []string
	p := newPass(s, f, &logs)
	for i := 0; i < epoch.PreviewMaxAttempts*backoff+1; i++ {
		p.Pass(ctx)
	}
	require.Len(t, f.previewHeights, epoch.PreviewMaxAttempts)
	got, ok := code(t, s, 7)
	require.True(t, ok)
	require.Equal(t, chain.PreviewError, got)
	require.Empty(t, due(t, s), "given up: off the list")
	require.Contains(t, logs[len(logs)-1], `stored as "error"`)
	require.Contains(t, logs[len(logs)-1], "HTTP 502 bad gateway")

	for i := 0; i < 2*backoff; i++ {
		p.Pass(ctx)
	}
	require.Len(t, f.previewHeights, epoch.PreviewMaxAttempts, "never asked again")
}

// A REST node that has not reached the epoch's height yet will reach it: that
// answer is not a failed attempt, however long the node lags.
func TestAHeightNotReachedIsNotAFailedAttempt(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	f := twoValidators()
	behind := true
	f.previewAt = func(h int64) (*chain.Preview, error) {
		if behind {
			return nil, fmt.Errorf("HTTP 500: %w", chain.ErrHeightAhead)
		}
		return onePreview(h), nil
	}
	readEpochs(t, s, f, 7)
	var logs []string
	p := newPass(s, f, &logs)
	for i := 0; i < 3*epoch.PreviewMaxAttempts*backoff; i++ {
		p.Pass(ctx)
	}
	require.Greater(t, len(f.previewHeights), epoch.PreviewMaxAttempts, "asked again every pass")
	_, stored := code(t, s, 7)
	require.False(t, stored, "never given up as error")
	require.Len(t, due(t, s), 1, "still on the list")
	require.Equal(t, 0, count(t, s, `SELECT COALESCE(SUM(attempts),0) FROM vp_preview_due WHERE chain_id='c1'`),
		"no attempt counted")

	behind = false
	p.Pass(ctx)
	got, ok := code(t, s, 7)
	require.True(t, ok)
	require.Equal(t, "available", got)
}

// The attempt count is in the database, so a restart does not reset it.
func TestTheAttemptCountSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	f := twoValidators()
	f.previewAt = func(int64) (*chain.Preview, error) { return nil, errors.New("timeout") }
	readEpochs(t, s, f, 7)
	var logs []string
	for i := 0; i < epoch.PreviewMaxAttempts; i++ {
		newPass(s, f, &logs).Pass(ctx) // a fresh pass each time: a restart, backoff forgotten
	}
	got, _ := code(t, s, 7)
	require.Equal(t, chain.PreviewError, got)
}

// MaxFailures transient failures in a row end the pass; the rest wait.
func TestFailuresInARowEndThePass(t *testing.T) {
	s := newStore(t)
	f := twoValidators()
	f.previewAt = func(int64) (*chain.Preview, error) { return nil, errors.New("HTTP 500") }
	readEpochs(t, s, f, 1, 2, 3, 4, 5)
	var logs []string
	newPass(s, f, &logs).Pass(context.Background())
	require.Equal(t, []int64{500, 400, 300}, f.previewHeights, "newest first, three failures, then the pass ends")
}

// The newest epoch is read first, and a success resets the failure run.
func TestNewestFirstAndASuccessResetsTheRun(t *testing.T) {
	s := newStore(t)
	f := twoValidators()
	f.previewAt = func(h int64) (*chain.Preview, error) {
		if h == 300 {
			return onePreview(h), nil
		}
		return nil, errors.New("HTTP 500")
	}
	readEpochs(t, s, f, 1, 2, 3, 4, 5)
	var logs []string
	newPass(s, f, &logs).Pass(context.Background())
	require.Equal(t, []int64{500, 400, 300, 200, 100}, f.previewHeights)
}

// hanging never answers: it waits for its context.
type hanging struct{ heights []int64 }

func (h *hanging) VotingPowerPreviewAt(ctx context.Context, height int64) (*chain.Preview, error) {
	h.heights = append(h.heights, height)
	<-ctx.Done()
	return nil, ctx.Err()
}

// L2: a route that never answers costs one request timeout, counted as a
// failure, and the pass stops there rather than wait again for the next epoch.
func TestAHangingRouteCostsOneTimeoutPerPass(t *testing.T) {
	s := newStore(t)
	readEpochs(t, s, twoValidators(), 1, 2, 3)
	src := &hanging{}
	var logs []string
	p := newPass(s, src, &logs)
	p.RequestTimeout = 50 * time.Millisecond
	start := time.Now()
	p.Pass(context.Background())
	took := time.Since(start)
	require.Equal(t, []int64{300}, src.heights)
	require.Less(t, took, time.Second)
	require.Equal(t, []store.DuePreview{{Epoch: 3, Attempts: 1}, {Epoch: 2}, {Epoch: 1}}, due(t, s))
	require.True(t, strings.Contains(logs[0], "deadline exceeded"), logs[0])
}

// L2: a read starts only while a whole request timeout still fits in the pass's
// time, so a pass of slow answers ends within PassTime.
func TestAReadStartsOnlyWhileItFitsThePassTime(t *testing.T) {
	s := newStore(t)
	f := twoValidators()
	clock := time.Unix(0, 0)
	f.previewAt = func(h int64) (*chain.Preview, error) {
		clock = clock.Add(time.Second) // each answer takes a second
		return onePreview(h), nil
	}
	readEpochs(t, s, f, 1, 2, 3, 4, 5)
	var logs []string
	p := newPass(s, f, &logs)
	p.SetClock(func() time.Time { return clock })
	p.Pass(context.Background()) // 5 s pass, 3 s timeout: reads start at 0, 1 and 2 s
	require.Equal(t, []int64{500, 400, 300}, f.previewHeights)
	p.Pass(context.Background())
	require.Equal(t, []int64{500, 400, 300, 200, 100}, f.previewHeights)
	require.Empty(t, due(t, s))
}

// A cancelled context is the shutdown, not the epoch: nothing is counted.
func TestAShutdownIsNotCountedAsAFailure(t *testing.T) {
	s := newStore(t)
	readEpochs(t, s, twoValidators(), 1)
	ctx, cancel := context.WithCancel(context.Background())
	src := &hanging{}
	var logs []string
	p := newPass(s, src, &logs)
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	p.Pass(ctx)
	require.Equal(t, []store.DuePreview{{Epoch: 1}}, due(t, s))
}

// L1: an epoch whose preview is stored is not listed again when the epoch is
// read again, and a later answer with a code does not replace the preview.
func TestAStoredPreviewIsNeverDowngraded(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	f := twoValidators()
	f.previewAt = func(h int64) (*chain.Preview, error) { return onePreview(h), nil }
	readEpochs(t, s, f, 7)
	var logs []string
	newPass(s, f, &logs).Pass(ctx)

	// A restart between WritePreview and MarkEpochRead reads the epoch again:
	// it is not listed again, so the preview is not asked again.
	readEpochs(t, s, f, 7)
	require.Empty(t, due(t, s))

	// And a code written over it, as a pass would on a pruned node, keeps it.
	pruned := chain.PreviewPruned
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 7, Height: 700, Unavailable: &pruned}, nil))
	got, _ := code(t, s, 7)
	require.Equal(t, "available", got)
	require.Equal(t, 1, count(t, s, `SELECT COUNT(*) FROM vp_preview WHERE epoch=7`))
}
