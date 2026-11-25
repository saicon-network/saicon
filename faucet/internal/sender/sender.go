// Package sender talks to the chain. The faucet signs by running the saicond
// binary; see README.md for why.
package sender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Sender sends a transfer from the faucet account and reports its balance.
type Sender interface {
	// Send pays amount (base denom) to the recipient and returns the tx hash.
	// pending is true when the tx passed CheckTx but was not seen in a block
	// before the wait ran out.
	Send(ctx context.Context, to string, amount uint64) (txhash string, pending bool, err error)
	Address() string
	Balance(ctx context.Context) (uint64, error)
}

// Runner executes the saicond binary; replaced in tests.
type Runner func(ctx context.Context, stdin string, args ...string) ([]byte, error)

// Config for the saicond-backed sender.
type Config struct {
	Binary     string
	Home       string // saicond home holding keyring-test/
	KeyName    string
	RPC, REST  string
	ChainID    string
	Denom      string
	Fees       string        // e.g. "0nscn"
	Gas        string        // fixed gas limit
	Wait       time.Duration // how long to wait for inclusion after broadcast
	RunTimeout time.Duration // cap on one saicond run (default 20 s)
	Poll       time.Duration
	Mnemonic   string // path to a mnemonic FILE; imported once if the key is missing
	HTTP       *http.Client
	Run        Runner
}

type Saicond struct {
	cfg     Config
	address string
	// sem is a one-slot lock: one in-flight send at a time, because the account
	// sequence must not collide. A channel, not a sync.Mutex, so that waiting
	// for it can give up when the request's deadline passes.
	sem chan struct{}
}

// SendError says whether a failed send may have reached the chain.
type SendError struct {
	Err error
	// Ambiguous is true when the transaction may have been broadcast: the
	// caller must keep the allowance consumed, because paying twice is worse
	// than refusing a retry. It is false only for a clear pre-broadcast failure
	// or a transaction the chain rejected (CheckTx code != 0, or failed
	// on-chain), where no transfer happened.
	Ambiguous bool
	TxHash    string // known hash, if any
}

func (e *SendError) Error() string { return e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }

// IsAmbiguous reports whether err is a SendError that may have paid out.
func IsAmbiguous(err error) bool {
	var se *SendError
	return errors.As(err, &se) && se.Ambiguous
}

// HashOf returns the tx hash carried by err, if any.
func HashOf(err error) string {
	var se *SendError
	if errors.As(err, &se) {
		return se.TxHash
	}
	return ""
}

func execRunner(bin string) Runner {
	return func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			// stderr can echo arguments but never the mnemonic (it goes via stdin).
			return out.Bytes(), fmt.Errorf("%s %s: %w: %s", bin, args[0], err, strings.TrimSpace(errb.String()))
		}
		return out.Bytes(), nil
	}
}

// New prepares the sender: imports the key from the mnemonic file if needed
// and resolves the faucet address.
func New(ctx context.Context, cfg Config) (*Saicond, error) {
	if cfg.Run == nil {
		cfg.Run = execRunner(cfg.Binary)
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.RunTimeout == 0 {
		cfg.RunTimeout = 20 * time.Second
	}
	if cfg.Poll == 0 {
		cfg.Poll = time.Second
	}
	s := &Saicond{cfg: cfg, sem: make(chan struct{}, 1)}
	addr, err := s.showKey(ctx)
	if err != nil {
		if cfg.Mnemonic == "" {
			return nil, fmt.Errorf("key %q not found in %s and no mnemonic file configured: %w", cfg.KeyName, cfg.Home, err)
		}
		if err := s.importKey(ctx); err != nil {
			return nil, err
		}
		if addr, err = s.showKey(ctx); err != nil {
			return nil, err
		}
	}
	s.address = addr
	return s, nil
}

func (s *Saicond) base() []string {
	return []string{"--home", s.cfg.Home, "--keyring-backend", "test"}
}

func (s *Saicond) showKey(ctx context.Context) (string, error) {
	out, err := s.cfg.Run(ctx, "", append([]string{"keys", "show", s.cfg.KeyName, "-a"}, s.base()...)...)
	if err != nil {
		return "", err
	}
	a := strings.TrimSpace(string(out))
	if a == "" {
		return "", errors.New("empty address from keys show")
	}
	return a, nil
}

func (s *Saicond) importKey(ctx context.Context) error {
	m, err := os.ReadFile(s.cfg.Mnemonic)
	if err != nil {
		return fmt.Errorf("read mnemonic file: %w", err)
	}
	// The mnemonic travels on stdin only, never as an argument or env var.
	args := append([]string{"keys", "add", s.cfg.KeyName, "--recover", "--no-backup", "--output", "json"}, s.base()...)
	if _, err := s.cfg.Run(ctx, strings.TrimSpace(string(m))+"\n", args...); err != nil {
		return errors.New("import mnemonic into keyring failed (details withheld)")
	}
	return nil
}

func (s *Saicond) Address() string { return s.address }

func (s *Saicond) Balance(ctx context.Context) (uint64, error) {
	u := fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s/by_denom?denom=%s",
		strings.TrimRight(s.cfg.REST, "/"), url.PathEscape(s.address), url.QueryEscape(s.cfg.Denom))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("balance query: HTTP %d", resp.StatusCode)
	}
	var r struct {
		Balance struct {
			Amount string `json:"amount"`
		} `json:"balance"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return 0, err
	}
	var n uint64
	if _, err := fmt.Sscanf(r.Balance.Amount, "%d", &n); err != nil {
		return 0, fmt.Errorf("bad balance %q", r.Balance.Amount)
	}
	return n, nil
}

func (s *Saicond) Send(ctx context.Context, to string, amount uint64) (string, bool, error) {
	// Wait for the lock only as long as the caller is willing to. A request that
	// never got the lock broadcast nothing, so its allowance is safe to give back.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return "", false, &SendError{Err: fmt.Errorf("queue timeout: %w", ctx.Err())}
	}
	if ctx.Err() != nil {
		return "", false, &SendError{Err: ctx.Err()}
	}
	// ctx bounded the wait for the lock. From here on the send runs detached from
	// it: killing saicond mid-broadcast would leave the outcome unknown, so the
	// broadcast and the inclusion wait get their own, longer deadlines.
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.RunTimeout+s.cfg.Wait+5*time.Second)
	defer rcancel()
	runCtx, runCancel := context.WithTimeout(rctx, s.cfg.RunTimeout)
	defer runCancel()

	args := []string{"tx", "bank", "send", s.cfg.KeyName, to, fmt.Sprintf("%d%s", amount, s.cfg.Denom),
		"--node", s.cfg.RPC, "--chain-id", s.cfg.ChainID,
		"--gas", s.cfg.Gas, "--fees", s.cfg.Fees,
		"--broadcast-mode", "sync", "--output", "json", "--yes"}
	out, err := s.cfg.Run(runCtx, "", append(args, s.base()...)...)
	res, perr := parseTxOutput(out)
	if err != nil {
		// A non-zero exit is ambiguous by default: the CLI can fail after the node
		// accepted the tx (connection reset, EOF while reading the answer), and
		// the tx may still land. Only positive proof that nothing was broadcast
		// lets the allowance go back: a refusal printed as JSON (CheckTx code != 0),
		// or an error that can only occur before broadcast (see provesNotBroadcast).
		if perr == nil && res.Code != 0 && runCtx.Err() == nil {
			return "", false, &SendError{Err: fmt.Errorf("transaction rejected (code %d): %s", res.Code, res.RawLog)}
		}
		if perr != nil && runCtx.Err() == nil && provesNotBroadcast(err.Error()+"\n"+string(out)) {
			return "", false, &SendError{Err: err}
		}
		return "", false, &SendError{Err: err, Ambiguous: true, TxHash: res.TxHash}
	}
	if perr != nil {
		// Exit 0 but unreadable output: the CLI says it finished, we cannot tell how.
		return "", false, &SendError{Err: perr, Ambiguous: true}
	}
	if res.Code != 0 {
		return "", false, &SendError{Err: fmt.Errorf("transaction rejected (code %d): %s", res.Code, res.RawLog)}
	}

	// Wait for inclusion while still holding the lock: the next send reads the
	// committed account sequence, which only advances once this tx is in a block.
	deadline := time.Now().Add(s.cfg.Wait)
	for {
		code, found := s.lookup(rctx, res.TxHash)
		if found {
			if code != 0 {
				return "", false, &SendError{Err: fmt.Errorf("transaction failed on chain (code %d), hash %s", code, res.TxHash)}
			}
			return res.TxHash, false, nil
		}
		if time.Now().After(deadline) || rctx.Err() != nil {
			return res.TxHash, true, nil
		}
		select {
		case <-time.After(s.cfg.Poll):
		case <-rctx.Done():
			return res.TxHash, true, nil
		}
	}
}

// notBroadcast are messages saicond (or the node's CheckTx, relayed by it) prints
// only when the transaction was refused before or at CheckTx. Matching is
// deliberately narrow: a connection error is NOT here, because it can happen
// after the node accepted the tx.
var notBroadcast = []string{
	"key not found",
	"not found in keyring",
	"invalid address",
	"decoding bech32 failed",
	"invalid bech32",
	"invalid coins",
	"invalid decimal coin",
	"insufficient funds",
	"insufficient fee",
	"account sequence mismatch",
}

func provesNotBroadcast(text string) bool {
	text = strings.ToLower(text)
	for _, m := range notBroadcast {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

type txOutput struct {
	Code   uint32 `json:"code"`
	TxHash string `json:"txhash"`
	RawLog string `json:"raw_log"`
}

// parseTxOutput finds the JSON object in the CLI output (the CLI may print
// notices before it).
func parseTxOutput(out []byte) (txOutput, error) {
	var res txOutput
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		if err := json.Unmarshal([]byte(line), &res); err == nil && res.TxHash != "" {
			return res, nil
		}
	}
	return res, fmt.Errorf("could not read broadcast result: %.200s", strings.TrimSpace(string(out)))
}

// lookup asks CometBFT RPC for the tx; found is false until it is in a block.
func (s *Saicond) lookup(ctx context.Context, hash string) (code uint32, found bool) {
	u := strings.TrimRight(s.cfg.RPC, "/") + "/tx?hash=0x" + url.QueryEscape(hash)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	var r struct {
		Result struct {
			TxResult struct {
				Code uint32 `json:"code"`
			} `json:"tx_result"`
			Hash string `json:"hash"`
		} `json:"result"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r) != nil || r.Result.Hash == "" {
		return 0, false
	}
	return r.Result.TxResult.Code, true
}
