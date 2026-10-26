// Package api serves the store over HTTP. It holds no business logic: every
// number it returns was either read from the chain or counted by the follower.
package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/saicon-network/saicon/indexer/internal/store"
)

type server struct {
	s       *store.Store
	chainID string
	tip     func(context.Context) (int64, error)
	stopped *Stopped
}

func New(s *store.Store, chainID string, tip func(context.Context) (int64, error), stopped *Stopped) http.Handler {
	srv := &server{s: s, chainID: chainID, tip: tip, stopped: stopped}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", srv.status)
	mux.HandleFunc("GET /v1/scoreboard", srv.scoreboard)
	mux.HandleFunc("GET /v1/validators/{operator}", srv.validator)
	mux.HandleFunc("GET /v1/validators/{operator}/epochs", srv.epochs)
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// status is the honesty endpoint: without it a stalled indexer looks exactly
// like a quiet chain.
func (s *server) status(w http.ResponseWriter, r *http.Request) {
	indexed, err := s.s.IndexedHeight(r.Context(), s.chainID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tip, err := s.tip(r.Context())
	if err != nil {
		// 503, not 200. This endpoint exists to admit when the index is behind,
		// and a monitor that reads only the status code would otherwise see a
		// service that cannot see the chain at all and call it healthy. The body
		// still says what happened.
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"chain_id": s.chainID, "indexed_height": indexed,
			"tip_height": nil, "lag_blocks": nil, "chain_error": err.Error(),
		})
		return
	}
	// lag_blocks is signed on purpose. A negative value means the chain is behind
	// the index, which a chain cannot be: it is a reset that reused the chain id,
	// or the node URL now points somewhere else. Clamping it to zero would hide
	// the one symptom that distinguishes that from a quiet chain.
	body := map[string]any{
		"chain_id": s.chainID, "indexed_height": indexed,
		"tip_height": tip, "lag_blocks": tip - indexed,
	}
	// A stopped follower with a reachable node is the case this endpoint would
	// otherwise report as healthy: the lag simply grows, which looks like catching
	// up. 503 makes it the same signal as an unreachable chain, which is what it is.
	if reason := s.stopped.Reason(); reason != "" {
		body["stopped"] = reason
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) scoreboard(w http.ResponseWriter, r *http.Request) {
	rows, err := s.s.Scoreboard(r.Context(), s.chainID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"validators": rows})
}

func (s *server) validator(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue("operator")
	rows, err := s.s.Scoreboard(r.Context(), s.chainID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// An exact match wins outright. The case-insensitive pass exists only so a
	// client that upper-cased a bech32 address still gets an answer — and if it
	// matches more than one stored operator, the request is refused rather than
	// answered with whichever happened to sort first. The chain emits lower case,
	// so two rows differing only by case mean the data is wrong, and serving one
	// validator's tokens under another's name is the worst available response.
	var fold []store.ScoreRow
	for _, row := range rows {
		if row.Operator == op {
			writeJSON(w, http.StatusOK, row)
			return
		}
		if strings.EqualFold(row.Operator, op) {
			fold = append(fold, row)
		}
	}
	switch len(fold) {
	case 0:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown validator: " + op})
	case 1:
		writeJSON(w, http.StatusOK, fold[0])
	default:
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "several stored validators match " + op + " case-insensitively; the chain emits lower case, so the index holds bad data",
		})
	}
}

func (s *server) epochs(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue("operator")
	from := parseUint(r.URL.Query().Get("from"), 0)
	// The default "to" has to be a uint64 that database/sql can still pass to the
	// driver as a query argument: math.MaxUint64 (^uint64(0)) has its high bit
	// set, and the driver's default converter only accepts uint64 values that fit
	// in an int64 — anything higher fails with "high bit set are not supported",
	// caught live against the localnet rather than by the unit tests, which never
	// call this endpoint without an explicit "to". MaxInt64 is effectively
	// unbounded for an epoch counter and sails through unchanged.
	to := parseUint(r.URL.Query().Get("to"), uint64(math.MaxInt64))
	rows, err := s.s.Epochs(r.Context(), s.chainID, op, from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operator": op, "epochs": rows})
}

// parseUint clamps to MaxInt64 because these values go straight to the SQL driver
// as query arguments, and modernc.org/sqlite refuses a uint64 with the high bit
// set. Fixing only the default left ?from=18446744073709551615 answering 500.
// No real epoch number comes near the cap.
func parseUint(s string, def uint64) uint64 {
	if s == "" {
		return def
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return def
	}
	if n > uint64(math.MaxInt64) {
		return uint64(math.MaxInt64)
	}
	return n
}
