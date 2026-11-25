// Package server is the faucet's HTTP surface.
package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/saicon-network/saicon/faucet/internal/bech32"
	"github.com/saicon-network/saicon/faucet/internal/clientip"
	"github.com/saicon-network/saicon/faucet/internal/limiter"
	"github.com/saicon-network/saicon/faucet/internal/sender"
)

//go:embed static/index.html
var indexHTML []byte

type Options struct {
	Sender        sender.Sender
	Limiter       *limiter.Limiter
	IPs           *clientip.Resolver
	ChainID       string
	Denom         string
	Prefix        string
	Amount        uint64
	Exponent      int
	ExplorerTxURL string
	// PublicOrigin is the exact Origin ("https://faucet.example") browsers send
	// from the faucet page. Empty means: accept only an Origin whose host equals
	// the request's Host.
	PublicOrigin string
	// SendTimeout bounds how long a request may wait for its turn to send
	// (default 30 s). A send that has started is not cut short by it.
	SendTimeout time.Duration
}

type Server struct {
	o Options

	balMu   sync.Mutex
	balAt   time.Time
	balVal  string
	balErr  bool
	balInit bool
}

// balanceTTL caches the faucet balance so /status cannot be used to hammer the
// node (which is the validator on the single-node devnet).
const balanceTTL = 10 * time.Second

func New(o Options) *Server {
	if o.SendTimeout == 0 {
		o.SendTimeout = 30 * time.Second
	}
	return &Server{o: o}
}

// cleanupCtx returns a context for bookkeeping that must finish even when the
// request's own context is done.
func cleanupCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// crossSite reports whether a browser request came from a page that is not the
// faucet's own. Non-browser clients send neither header and pass.
func (s *Server) crossSite(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	if s.o.PublicOrigin != "" {
		return origin != s.o.PublicOrigin
	}
	u, err := url.Parse(origin)
	return err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("POST /request", s.request)
	mux.HandleFunc("GET /status", s.status)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, code int, kind, msg string, extra map[string]any) {
	body := map[string]any{"error": kind, "message": msg}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, code, body)
}

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'")
	_, _ = w.Write(indexHTML)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	left, err := s.o.Limiter.BudgetLeft(ctx)
	if err != nil {
		apiErr(w, 500, "internal", "could not read the daily budget", nil)
		return
	}
	out := map[string]any{
		"chain_id":           s.o.ChainID,
		"address":            s.o.Sender.Address(),
		"denom":              s.o.Denom,
		"display_exponent":   s.o.Exponent,
		"amount_per_request": strconv.FormatUint(s.o.Amount, 10),
		"budget_left":        strconv.FormatUint(left, 10),
		"daily_budget":       strconv.FormatUint(s.o.Limiter.Budget(), 10),
		"window_seconds":     int64(s.o.Limiter.Window() / time.Second),
		"explorer_tx_url":    s.o.ExplorerTxURL,
	}
	if bal, ok := s.balance(ctx); ok {
		out["balance"] = bal
	} else {
		out["balance"] = nil
		out["balance_error"] = "chain unreachable"
	}
	writeJSON(w, 200, out)
}

// balance returns the faucet balance, at most one node query per balanceTTL.
func (s *Server) balance(ctx context.Context) (string, bool) {
	s.balMu.Lock()
	defer s.balMu.Unlock()
	if s.balInit && time.Since(s.balAt) < balanceTTL {
		return s.balVal, !s.balErr
	}
	bal, err := s.o.Sender.Balance(ctx)
	s.balInit, s.balAt, s.balErr = true, time.Now(), err != nil
	if err == nil {
		s.balVal = strconv.FormatUint(bal, 10)
	}
	return s.balVal, err == nil
}

func (s *Server) request(w http.ResponseWriter, r *http.Request) {
	// A cross-site page can make a browser send a "simple" POST (text/plain, no
	// preflight) from its visitor's IP. Requiring JSON forces a preflight that
	// fails (the faucet sends no CORS headers); the header checks are a second wall.
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		apiErr(w, 415, "unsupported_media_type", "Send the request as application/json.", nil)
		return
	}
	if s.crossSite(r) {
		apiErr(w, 403, "forbidden", "Requests must come from the faucet page itself.", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var in struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		apiErr(w, 400, "bad_request", `send JSON like {"address":"saicon1..."}`, nil)
		return
	}
	addr, err := bech32.ValidateAccount(in.Address, s.o.Prefix)
	if err != nil {
		// The parser's reason is for the log; the visitor gets a plain instruction.
		log.Printf("invalid address %q: %v", truncate(in.Address, 80), err)
		pfx := s.o.Prefix + "1"
		typed := strings.ToLower(strings.TrimSpace(in.Address))
		msg := "Enter a full SAiCoN address: it starts with " + pfx + "."
		switch {
		case typed == "":
			msg = "Enter your address."
		case strings.HasPrefix(typed, pfx) && !errors.Is(err, bech32.ErrWrongPrefix):
			// Right start, bad body: a typo, a truncated or a padded copy.
			msg = "This address is incomplete or mistyped: copy it again from your wallet."
		}
		apiErr(w, 400, "invalid_address", msg, nil)
		return
	}
	ip, ok := s.o.IPs.Key(r)
	if !ok {
		apiErr(w, 400, "bad_request", "could not determine client address", nil)
		return
	}

	id, err := s.o.Limiter.Reserve(r.Context(), addr, ip)
	if err != nil {
		if le, ok := limiter.IsLimit(err); ok {
			s.limited(w, le)
			return
		}
		log.Printf("reserve: %v", err)
		apiErr(w, 500, "internal", "internal error", nil)
		return
	}

	// The send must outlive a client that hangs up: the tx may already be out.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), s.o.SendTimeout)
	defer cancel()
	txhash, pending, err := s.o.Sender.Send(ctx, addr, s.o.Amount)
	if err != nil {
		// Bookkeeping uses a fresh context: the send context may be the very
		// thing that expired, and a failed release would leak the allowance.
		cctx, ccancel := cleanupCtx()
		defer ccancel()
		if sender.IsAmbiguous(err) {
			// The tx may have been broadcast. Keep the allowance and budget
			// consumed: refusing a retry is cheaper than paying twice.
			if h := sender.HashOf(err); h != "" {
				_ = s.o.Limiter.Commit(cctx, id, h)
			}
			log.Printf("AMBIGUOUS send to %s, allowance kept: %v", addr, err)
			apiErr(w, 502, "send_unconfirmed", "The transfer could not be confirmed and may still arrive. Check your balance before asking again; your allowance was kept.", nil)
			return
		}
		if rerr := s.o.Limiter.Release(cctx, id); rerr != nil {
			log.Printf("release reservation %d: %v", id, rerr)
		}
		log.Printf("send to %s failed: %v", addr, err)
		apiErr(w, 502, "send_failed", "The transfer failed. Nothing was used from your allowance, try again in a minute.", nil)
		return
	}
	cctx, ccancel := cleanupCtx()
	defer ccancel()
	if cerr := s.o.Limiter.Commit(cctx, id, txhash); cerr != nil {
		log.Printf("commit %d: %v", id, cerr)
	}
	log.Printf("sent %d%s to %s tx=%s pending=%v", s.o.Amount, s.o.Denom, addr, txhash, pending)
	st := "included"
	if pending {
		st = "pending"
	}
	writeJSON(w, 200, map[string]any{
		"txhash": txhash,
		"amount": fmt.Sprintf("%d%s", s.o.Amount, s.o.Denom),
		"status": st,
	})
}

func (s *Server) limited(w http.ResponseWriter, le *limiter.LimitError) {
	secs := int(math.Ceil(le.RetryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	extra := map[string]any{"retry_after_seconds": secs}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	switch le.Reason {
	case limiter.ReasonAddress:
		apiErr(w, 429, le.Reason, "This address already received tokens recently. Try again later.", extra)
	case limiter.ReasonIP:
		apiErr(w, 429, le.Reason, "This network already received tokens recently. Try again later.", extra)
	default:
		apiErr(w, 503, le.Reason, "The faucet's daily budget is spent. It resets as earlier payouts age past 24 hours.", extra)
	}
}

// truncate caps a user-supplied string before it goes into a log line.
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
