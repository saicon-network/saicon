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

			// epoch_length_blocks is governance-changeable, and a change
			// renumbers the chain's epochs from that height on while
			// everything already in this database keeps the old bucketing.
			// Nothing about that is visible in the data afterwards, so it is
			// treated like a reset: stop, say so, keep serving what is here.
			if now, err := rest.EpochLength(ctx); err != nil {
				log.Printf("epoch length: %v", err)
				continue
			} else if now != epochLen {
				reason := fmt.Sprintf("epoch_length_blocks changed %d -> %d. Epochs already "+
					"indexed use the old length and cannot be renumbered; start a new database.",
					epochLen, now)
				log.Printf("STOPPING: %s", reason)
				stopped.Set(reason)
				return
			}

			n, err := f.Once(ctx, status.ChainID)
			if err != nil {
				// A reset is not a bad tick: the chain is behind the index and
				// no amount of retrying changes that. Say so once, stop
				// following, and keep serving what was already learned — the
				// operator decides whether to discard the proposal counts,
				// because nothing can rebuild them.
				var re *follow.ResetError
				if errors.As(err, &re) {
					log.Printf("STOPPING: %v", re)
					stopped.Set(re.Error())
					return
				}
				log.Printf("follow: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("followed %d block(s)", n)
			}
			// The epoch trigger reads the INDEXED height, never the chain's tip —
			// see epochPass's doc comment for why, and its own tests for the
			// scheduling guarantees this composition has to uphold.
			h, err := s.IndexedHeight(ctx, status.ChainID)
			if err != nil {
				log.Printf("cursor: %v", err)
				continue
			}
			highest, ok, err := s.HighestEpochRead(ctx, status.ChainID)
			if err != nil {
				log.Printf("epoch cursor: %v", err)
				continue
			}
			from, err := s.FirstWalkedHeight(ctx, status.ChainID)
			if err != nil {
				log.Printf("walk floor: %v", err)
				continue
			}
			epochPass(ctx, h, from, epochLen, highest, ok,
				func(ctx context.Context, e uint64) error { return reader.ReadEpoch(ctx, status.ChainID, e) },
				log.Printf)
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
