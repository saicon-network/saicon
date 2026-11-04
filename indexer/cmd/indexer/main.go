// Command indexer follows the SAiCoN chain and serves what it learns.
//
// It must not be pointed at a validator node anywhere past the single-node
// devnet: ADR-0015 separates retention from exposure precisely so continuous
// query load never lands on a node that signs blocks.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/saicon-network/saicon/indexer/internal/api"
	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/concentration"
	"github.com/saicon-network/saicon/indexer/internal/epoch"
	"github.com/saicon-network/saicon/indexer/internal/follow"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// epochLengthMismatch reports whether the chain's current epoch length
// differs from what this database was already built with, and the message to
// die with if so. Extracted from main so the decision — not the log.Fatalf
// that acts on it — is what a test drives: a change here is silent in the
// data (EpochOf and the coverage arithmetic just start producing numbers
// against a length nothing recorded), so the one thing standing between that
// and a wrong count in proposals_raw has to be checked itself.
func epochLengthMismatch(stored uint64, storedKnown bool, current uint64, rpcURL string) (string, bool) {
	if !storedKnown || stored == current {
		return "", false
	}
	return fmt.Sprintf("this database was indexed with epoch_length_blocks=%d, but %s now reports %d. "+
		"Epochs already indexed use the old length and cannot be renumbered; start a new database.",
		stored, rpcURL, current), true
}

// epochPass decides which closed epochs this database has not yet finished
// reading — walking forward one at a time from wherever the walk floor or
// HighestEpochRead leaves off, oldest first — and reads them via readEpoch,
// stopping at the first error so that epoch is retried on the next call
// rather than skipped past.
//
// This exact composition — LastClosedEpoch, EpochOf(firstWalked), "whichever
// of the floor or HighestEpochRead is higher", walk one at a time, stop don't
// skip — produced both of the review's Criticals (a seeded cursor faking
// coverage, a partially written epoch looking finished) and Task 5's
// permanent epoch-skipping bug, and every one of them was caught by a person
// reading the code or by a live run, never by a test. It is deliberately free
// of *store.Store, *epoch.Reader and the chain clients so a test can drive
// its scheduling decisions against fakes, with nothing to fake but two
// integers and a function.
func epochPass(ctx context.Context, indexedHeight, firstWalked int64, epochLen uint64,
	highest uint64, highestKnown bool,
	readEpoch func(context.Context, uint64) error, logf func(string, ...any)) {
	// Read every epoch that has CLOSED and is not yet in the database, oldest
	// first — never the one in progress, which the chain answers 404 for and
	// would otherwise be recorded as a row of NULLs.
	closed := follow.LastClosedEpoch(indexedHeight, epochLen)
	// The floor matters against a pruned node: epochs whose blocks this
	// service never walked are not worth ~300 REST calls each to write a row
	// whose proposal count would be NULL anyway.
	next := follow.EpochOf(firstWalked, epochLen)
	if highestKnown && highest+1 > next {
		next = highest + 1
	}
	for e := next; e <= closed; e++ {
		if err := readEpoch(ctx, e); err != nil {
			logf("epoch %d: %v", e, err)
			return // retry this epoch on the next tick; do not skip past it
		}
		logf("read epoch %d", e)
	}
}

// ConcentrationCallBudget bounds the chain requests one concentration pass
// makes. The pass shares the indexing goroutine with the follower and the S6
// epoch reader, so its cost is what the follower waits for each tick. An epoch
// costs 7 requests plus one per listed entity (revoked ones included, so the
// term grows with the registry's history, not with the seats), one per attested
// entity, one per registry-ACTIVE validator outside an attested entity, and a
// page per 100 validators past the first 100: about 9 on the devnet, 172 at 75
// validators, 75 attested entities and 15 validators outside them. Bounding by
// requests, not epochs, keeps the wait the same whatever the size of the set.
//
// It is not a hard bound: an epoch is read in one piece, the first epoch of
// the forward pass and of the re-read each always run, and the estimate is the
// last epoch's cost. A pass costs at most about max(budget, one epoch) for
// each of the two.
const ConcentrationCallBudget = 200

// callBudget is one loop's share of ConcentrationCallBudget. An epoch is
// measured in one piece, so the loop decides before each epoch: it starts the
// next one only if the requests spent so far plus what the last epoch cost
// still fit. The first epoch always runs, so an epoch that alone costs more
// than the budget still gets read, one per pass.
type callBudget struct {
	limit int
	// spent is the number of requests made since the pass began.
	spent func() int
	// est is what the last epoch read cost: the estimate for the next one. It
	// carries over from pass to pass.
	est int
	ran int
}

// allow reports whether one more epoch fits.
func (b *callBudget) allow() bool { return b.ran == 0 || b.spent()+b.est <= b.limit }

// run reads one epoch and records what it cost.
func (b *callBudget) run(ctx context.Context, e uint64, read func(context.Context, uint64) error) error {
	before := b.spent()
	err := read(ctx, e)
	b.est = b.spent() - before
	b.ran++
	return err
}

// concentrationPass reads closed epochs not yet measured, oldest first, while
// the budget allows, stopping at the first error so that epoch is retried
// rather than skipped. It trails the follower by the same rule as epochPass:
// only an epoch whose last block the follower has already walked is asked for,
// so the node has the height too. Like epochPass it takes its cursors as
// parameters, so a test drives the scheduling with integers and functions.
func concentrationPass(ctx context.Context, indexedHeight, firstWalked int64, epochLen uint64,
	highest uint64, highestKnown bool, b *callBudget,
	read func(context.Context, uint64) error, logf func(string, ...any)) {
	closed := follow.LastClosedEpoch(indexedHeight, epochLen)
	next := follow.EpochOf(firstWalked, epochLen)
	if highestKnown && highest+1 > next {
		next = highest + 1
	}
	for e := next; e <= closed && b.allow(); e++ {
		if err := b.run(ctx, e, read); err != nil {
			logf("concentration epoch %d: %v", e, err)
			return // retry this epoch on the next tick; do not skip past it
		}
		logf("measured epoch %d", e)
	}
}

// entityLayer is what this process knows about the chain's entity layer.
type entityLayer struct {
	// known is false until the chain has answered once without an error.
	known bool
	// present and from are EntityLayerFrom's answer. A present layer is final;
	// an absent one is asked about again every pass.
	present bool
	from    int64
}

// resolve asks the chain for the entity layer unless it is already known to be
// present. ok is false when the chain answered with an error: the layer is
// unknown for this pass, and the caller skips the concentration pass rather
// than label epochs with a guess. An absent layer is an answer, and ok is true.
func (l *entityLayer) resolve(ctx context.Context, layerFrom func(context.Context) (int64, bool, error),
	logf func(string, ...any)) (ok bool) {
	if l.present {
		return true
	}
	from, present, err := layerFrom(ctx)
	if err != nil {
		logf("entity layer: %v", err)
		return false
	}
	switch {
	case present:
		logf("entity layer from height %d", from)
	case !l.known:
		logf("no entity registry on this chain")
	}
	l.known, l.present, l.from = true, present, from
	return true
}

// rereadBatch is how many no_entity_layer epochs one store query lists for re-reading.
const rereadBatch = 100

// rereadBackoffPasses is how many passes an epoch whose re-read failed waits
// before it is tried again, so one that keeps failing cannot take every pass.
const rereadBackoffPasses = 10

// rereadMaxFailures ends the re-read for a pass after that many failures in a
// row. A failed attempt may cost a single request but wait out a whole HTTP
// timeout, so a gateway that hangs on historical heights would otherwise hold
// the pass, and the follower behind it, for one timeout per waiting epoch.
const rereadMaxFailures = 3

// concRunner is the concentration side of a tick: it resolves the entity layer
// lazily, re-reads the epochs it wrote no_entity_layer before the layer
// appeared, and measures new epochs, all under one call budget per pass.
type concRunner struct {
	s         *store.Store
	chainID   string
	epochLen  uint64
	reader    *concentration.Reader
	layerFrom func(context.Context) (int64, bool, error)
	// calls is the running count of requests made by the clients the reader
	// and layerFrom use, and by nothing else.
	calls func() int
	limit int
	logf  func(string, ...any)

	layer      entityLayer
	est        int
	rereadEst  int
	rereadDone bool
	// passes counts concentration passes; rereadAfter is the re-read's cursor,
	// wrapping to 0 at the end of the list; retryAt holds, per epoch whose
	// re-read failed, the first pass it may be tried again in.
	passes      int
	rereadAfter uint64
	retryAt     map[uint64]int
}

// newConcRunner wires the runner to clients that only it uses, so their
// request counts are its own work.
func newConcRunner(s *store.Store, rpc *chain.RPC, rest *chain.REST, chainID string, epochLen uint64,
	limit int, logf func(string, ...any)) *concRunner {
	return &concRunner{
		s: s, chainID: chainID, epochLen: epochLen,
		reader:    concentration.NewReader(s, node{rpc, rest}, epochLen, 0, false),
		layerFrom: rest.EntityLayerFrom,
		calls:     func() int { return int(rpc.Calls() + rest.Calls()) },
		limit:     limit, logf: logf,
		retryAt: map[uint64]int{},
	}
}

// resolveLayer updates the layer and the reader's gates. ok is false while the
// layer is unknown because the chain answered with an error.
func (c *concRunner) resolveLayer(ctx context.Context) (ok bool) {
	if !c.layer.resolve(ctx, c.layerFrom, c.logf) {
		return false
	}
	c.reader.SetEntityLayer(c.layer.from, c.layer.present)
	return true
}

// pass is one concentration pass: new epochs first, then the re-read of
// no_entity_layer epochs with what is left of the budget. Each has its own
// first-epoch guarantee, so neither can starve the other.
func (c *concRunner) pass(ctx context.Context) {
	c.passes++
	start := c.calls()
	spent := func() int { return c.calls() - start }

	if !c.resolveLayer(ctx) {
		return
	}

	fwd := &callBudget{limit: c.limit, spent: spent, est: c.est}
	c.forward(ctx, fwd)
	c.est = fwd.est

	if c.layer.present && !c.rereadDone {
		rb := &callBudget{limit: c.limit, spent: spent, est: c.rereadEst}
		c.reread(ctx, rb)
		c.rereadEst = rb.est
	}
}

// forward measures the closed epochs not yet stored.
func (c *concRunner) forward(ctx context.Context, b *callBudget) {
	h, err := c.s.IndexedHeight(ctx, c.chainID)
	if err != nil {
		c.logf("cursor: %v", err)
		return
	}
	highest, ok, err := c.s.HighestConcentrationEpoch(ctx, c.chainID)
	if err != nil {
		c.logf("concentration cursor: %v", err)
		return
	}
	from, err := c.s.FirstWalkedHeight(ctx, c.chainID)
	if err != nil {
		c.logf("walk floor: %v", err)
		return
	}
	concentrationPass(ctx, h, from, c.epochLen, highest, ok, b,
		func(ctx context.Context, e uint64) error { return c.reader.ReadEpoch(ctx, c.chainID, e) },
		c.logf)
}

// reread reads again, within the budget, the stored epochs written
// no_entity_layer whose last height is at or after the layer's first height.
// Earlier epochs keep their rows.
//
// It walks a cursor that wraps at the end of the list, so it resumes where the
// last pass stopped. A failed epoch is logged and skipped for
// rereadBackoffPasses passes, and the walk moves on to the next one: an epoch
// that keeps failing is retried after the others and never blocks them.
// rereadMaxFailures failures in a row end the walk for this pass, and a
// cancelled context ends it at once.
func (c *concRunner) reread(ctx context.Context, b *callBudget) {
	wrapped, failures := false, 0
	for {
		if ctx.Err() != nil {
			return
		}
		eps, err := c.s.EpochsWithEntitiesUnavailable(ctx, c.chainID, concentration.NoEntityLayer,
			c.layer.from, c.rereadAfter, rereadBatch)
		if err != nil {
			c.logf("re-read list: %v", err)
			return
		}
		if len(eps) == 0 {
			if c.rereadAfter == 0 {
				c.rereadDone = true // nothing left anywhere
				return
			}
			if wrapped {
				return // every remaining epoch is backing off
			}
			c.rereadAfter, wrapped = 0, true
			continue
		}
		for _, e := range eps {
			if c.retryAt[e] > c.passes {
				c.rereadAfter = e
				continue
			}
			if ctx.Err() != nil {
				return // shutting down: the cursor is still below e
			}
			if !b.allow() {
				return // resume at e next pass: the cursor is still below it
			}
			if err := b.run(ctx, e, func(ctx context.Context, e uint64) error {
				return c.reader.RereadEntities(ctx, c.chainID, e)
			}); err != nil {
				if ctx.Err() != nil {
					return // the shutdown, not the epoch, failed the read
				}
				c.rereadAfter = e
				c.retryAt[e] = c.passes + rereadBackoffPasses
				c.logf("concentration re-read epoch %d: %v (retried in %d passes)", e, err, rereadBackoffPasses)
				if failures++; failures >= rereadMaxFailures {
					c.logf("concentration re-read: %d failures in a row, stopping until the next pass", failures)
					return
				}
				continue
			}
			c.rereadAfter = e
			failures = 0
			delete(c.retryAt, e)
			c.logf("re-read epoch %d with the entity layer", e)
		}
	}
}

// continuity is the verdict of the same-chain-id reset check.
type continuity int

const (
	// continuityOK: the node holds the block this database recorded.
	continuityOK continuity = iota
	// continuitySkip: the check could not be made this tick.
	continuitySkip
	// continuityStop: the node holds a different block at that height.
	continuityStop
)

// continuityVerdict compares the block hash this database stored at height with
// the node's. A height the node has pruned or not reached yet skips only the
// check, with an empty message: the passes carry on, and a chain that is merely
// shorter is the follower's to stop. Any other failure skips the check with a
// message, and the caller holds back the concentration pass for the tick. A
// different hash means the chain under this chain id is not the one this
// database holds.
func continuityVerdict(ctx context.Context, height int64, stored string,
	hashAt func(context.Context, int64) (string, error)) (continuity, string) {
	got, err := hashAt(ctx, height)
	switch {
	case errors.Is(err, chain.ErrHeightUnavailable), errors.Is(err, chain.ErrHeightAhead):
		return continuitySkip, ""
	case err != nil:
		return continuitySkip, "continuity check: " + err.Error()
	case got != stored:
		return continuityStop, fmt.Sprintf("the block at height %d is %s on the node but %s in this database: "+
			"the chain was reset under the same chain id. Start a new database.", height, got, stored)
	}
	return continuityOK, ""
}

// tickDeps is what one tick of the indexing loop calls, as functions, so a test
// can record the order and feed each step a verdict.
type tickDeps struct {
	// epochLen is the epoch length this database was built with.
	epochLen          uint64
	epochLength       func(context.Context) (uint64, error)
	newestHashed      func(context.Context) (height int64, hash string, ok bool, err error)
	hashAt            func(context.Context, int64) (string, error)
	followOnce        func(context.Context) (int64, error)
	epochPass         func(context.Context)
	concentrationPass func(context.Context)
	logf              func(string, ...any)
}

// tick runs one pass of the loop: epoch-length check, continuity check,
// follower, epochPass, concentrationPass. A non-empty stop is a reason to stop
// the loop for good; the caller logs it, records it for /v1/status and returns.
//
// The continuity check runs before the follower so a reset that kept the chain
// id is caught before the new chain's blocks are counted into the proposal
// history. An error in the concentration pass is logged and retried next tick;
// it never reaches the passes that run before it.
func tick(ctx context.Context, d tickDeps) (stop string) {
	// epoch_length_blocks is governance-changeable, and a change renumbers the
	// chain's epochs from that height on while everything already in this
	// database keeps the old bucketing. Nothing about that is visible in the
	// data afterwards, so it is treated like a reset: stop, say so, keep serving
	// what is here.
	if now, err := d.epochLength(ctx); err != nil {
		d.logf("epoch length: %v", err)
		return ""
	} else if now != d.epochLen {
		return fmt.Sprintf("epoch_length_blocks changed %d -> %d. Epochs already "+
			"indexed use the old length and cannot be renumbered; start a new database.",
			d.epochLen, now)
	}

	measure := true
	height, hash, ok, err := d.newestHashed(ctx)
	if err != nil {
		d.logf("continuity: %v", err)
		measure = false
	} else if ok {
		switch v, msg := continuityVerdict(ctx, height, hash, d.hashAt); v {
		case continuityStop:
			return msg
		case continuitySkip:
			if msg != "" {
				d.logf("%s", msg)
				measure = false
			}
		}
	}

	n, err := d.followOnce(ctx)
	if err != nil {
		// A reset is not a bad tick: the chain is behind the index and no amount
		// of retrying changes that. Say so once, stop following, and keep serving
		// what was already learned — the operator decides whether to discard the
		// proposal counts, because nothing can rebuild them.
		var re *follow.ResetError
		if errors.As(err, &re) {
			return re.Error()
		}
		d.logf("follow: %v", err)
		return ""
	}
	if n > 0 {
		d.logf("followed %d block(s)", n)
	}
	d.epochPass(ctx)
	if measure {
		d.concentrationPass(ctx)
	}
	return ""
}

// node is the chain as the concentration reader sees it: block data from RPC,
// module state from REST.
type node struct {
	*chain.RPC
	*chain.REST
}

func main() {
	var (
		dbPath  = env("INDEXER_DB", "/data/indexer.db")
		rpcURL  = env("INDEXER_RPC_URL", "http://localhost:26657")
		restURL = env("INDEXER_REST_URL", "http://localhost:1317")
		listen  = env("INDEXER_LISTEN", ":8080")
	)
	pollSec, err := strconv.Atoi(env("INDEXER_POLL_SECONDS", "5"))
	if err != nil {
		log.Fatalf("INDEXER_POLL_SECONDS: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	s, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer func() { _ = s.Close() }()

	rpc := chain.NewRPC(rpcURL)
	rest := chain.NewREST(restURL)

	status, err := rpc.Status(ctx)
	if err != nil {
		log.Fatalf("reach the chain at %s: %v", rpcURL, err)
	}
	// A different chain id means a reset, not a continuation. Starting a new
	// index is the only honest response: a cursor from the old chain would
	// either stall or splice two histories together.
	known, haveChain, err := s.ChainID(ctx)
	if err != nil {
		log.Fatalf("read stored chain: %v", err)
	}
	// A different chain id is not something to log and continue past. This
	// database holds one chain's heights, epochs and proposal counts; indexing a
	// second chain into it would interleave two histories under one cursor, and
	// the proposal counts are the part nothing can untangle afterwards.
	if haveChain && known != status.ChainID {
		log.Fatalf("this database indexes chain %s, but %s serves %s. "+
			"Point INDEXER_RPC_URL back at %s, or start a new database for %s.",
			known, rpcURL, status.ChainID, known, status.ChainID)
	}
	fresh := !haveChain
	if err := s.SetChain(ctx, status.ChainID, status.EarliestBlockTime); err != nil {
		log.Fatalf("record chain: %v", err)
	}
	// A fresh database would otherwise start at height 1, which a pruned node no
	// longer has — the first request errors and nothing is ever counted. Start at
	// the earliest block this node still holds, and say plainly what that costs:
	// the proposals below it are not missing data, they are data that cannot be
	// gathered from this node at all.
	if fresh && status.EarliestHeight > 1 {
		if err := s.SeedCursor(ctx, status.ChainID, status.EarliestHeight-1); err != nil {
			log.Fatalf("seed cursor: %v", err)
		}
		log.Printf("node retains from height %d, not from genesis: starting there. "+
			"Proposal counts for heights 1-%d will never exist in this database; "+
			"point INDEXER_RPC_URL at a node that retains from genesis if you need them.",
			status.EarliestHeight, status.EarliestHeight-1)
	}

	epochLen, err := rest.EpochLength(ctx)
	if err != nil {
		log.Fatalf("read epoch length: %v", err)
	}
	// x/participation treats 0 as "epoch closing disabled" and governance can set
	// it. Every block would bucket into epoch 0, LastClosedEpoch would stay 0, and
	// nothing would ever be read — while the process looked perfectly healthy.
	if epochLen == 0 {
		log.Fatalf("chain %s reports epoch_length_blocks = 0, so it never closes an epoch: "+
			"there is nothing for this service to index", status.ChainID)
	}
	// The startup mismatch check, not just the in-run one below: comparing the
	// chain's current length only against a value held in this process's memory
	// made the check survive exactly as long as the process did. Stop the
	// service, let governance change the parameter, start it again, and the new
	// length would silently become the baseline while proposals_raw still held
	// rows bucketed under the old one — with nothing in the data to show it.
	storedLen, haveStoredLen, err := s.StoredEpochLength(ctx, status.ChainID)
	if err != nil {
		log.Fatalf("read stored epoch length: %v", err)
	}
	if msg, mismatch := epochLengthMismatch(storedLen, haveStoredLen, epochLen, rpcURL); mismatch {
		log.Fatalf("%s", msg)
	}
	if !haveStoredLen {
		if err := s.RecordEpochLength(ctx, status.ChainID, epochLen); err != nil {
			log.Fatalf("record epoch length: %v", err)
		}
	}
	log.Printf("chain=%s tip=%d epoch_length=%d db=%s", status.ChainID, status.LatestHeight, epochLen, dbPath)

	f := follow.New(s, rpc, epochLen)
	reader := epoch.New(s, rest, epochLen)
	// The concentration pass has clients of its own, so their request counts
	// are its work alone (the API's tip reads go through rpc).
	conc := newConcRunner(s, chain.NewRPC(rpcURL), chain.NewREST(restURL), status.ChainID, epochLen,
		ConcentrationCallBudget, log.Printf)
	// A chain from before the entity layer has no module_versions entry for it:
	// that is "no entity registry", not a failure. Any other error leaves the
	// layer unknown, which is not fatal either: the S6 and S7 passes do not need
	// it, and each concentration pass asks again until the chain answers.
	conc.resolveLayer(ctx)

	// Stopped is read by /v1/status. Without it, the two conditions the loop
	// below itself calls STOPPING — a chain reset and a governance change to
	// the epoch length — leave a process that is alive, answering 200, and
	// frozen: the node is reachable in both cases, that is how the loop got
	// there, so the lag would simply grow while looking like ordinary catch-up.
	stopped := &api.Stopped{}

	// A buffered signal so the loop runs once immediately rather than waiting out
	// the first poll interval: without it /v1/status reports the full startup lag
	// for up to INDEXER_POLL_SECONDS after the process is already up and reachable.
	first := make(chan struct{}, 1)
	first <- struct{}{}

	deps := tickDeps{
		epochLen:    epochLen,
		epochLength: rest.EpochLength,
		newestHashed: func(ctx context.Context) (int64, string, bool, error) {
			return s.NewestHashedEpoch(ctx, status.ChainID)
		},
		hashAt:     rpc.BlockHashAt,
		followOnce: func(ctx context.Context) (int64, error) { return f.Once(ctx, status.ChainID) },
		logf:       log.Printf,
		// The epoch trigger reads the INDEXED height, never the chain's tip —
		// see epochPass's doc comment for why, and its own tests for the
		// scheduling guarantees this composition has to uphold.
		epochPass: func(ctx context.Context) {
			h, err := s.IndexedHeight(ctx, status.ChainID)
			if err != nil {
				log.Printf("cursor: %v", err)
				return
			}
			highest, ok, err := s.HighestEpochRead(ctx, status.ChainID)
			if err != nil {
				log.Printf("epoch cursor: %v", err)
				return
			}
			from, err := s.FirstWalkedHeight(ctx, status.ChainID)
			if err != nil {
				log.Printf("walk floor: %v", err)
				return
			}
			epochPass(ctx, h, from, epochLen, highest, ok,
				func(ctx context.Context, e uint64) error { return reader.ReadEpoch(ctx, status.ChainID, e) },
				log.Printf)
		},
		concentrationPass: conc.pass,
	}

	go func() {
		t := time.NewTicker(time.Duration(pollSec) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-first:
			case <-t.C:
			}

			if reason := tick(ctx, deps); reason != "" {
				log.Printf("STOPPING: %s", reason)
				stopped.Set(reason)
				return
			}
		}
	}()

	srv := &http.Server{
		Addr:              listen,
		Handler:           api.New(s, status.ChainID, rpc.Tip, stopped),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()
	log.Printf("serving on %s", listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}
