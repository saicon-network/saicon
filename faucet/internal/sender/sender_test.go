package sender

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseTxOutput(t *testing.T) {
	r, err := parseTxOutput([]byte("gas estimate: 1\n{\"code\":0,\"txhash\":\"ABC\",\"raw_log\":\"\"}\n"))
	if err != nil || r.TxHash != "ABC" {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := parseTxOutput([]byte("boom")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func rig(t *testing.T, run Runner, rpc http.Handler) *Saicond {
	t.Helper()
	srv := httptest.NewServer(rpc)
	t.Cleanup(srv.Close)
	s := &Saicond{address: "saicon1x", sem: make(chan struct{}, 1), cfg: Config{
		KeyName: "faucet", RPC: srv.URL, REST: srv.URL, Denom: "nscn", Fees: "0nscn", Gas: "200000",
		Wait: 300 * time.Millisecond, RunTimeout: 5 * time.Second, Poll: 10 * time.Millisecond, HTTP: srv.Client(), Run: run,
	}}
	return s
}

func TestSendIncluded(t *testing.T) {
	var gotArgs []string
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte(`{"code":0,"txhash":"H1","raw_log":""}`), nil
	}
	var calls int32
	s := rig(t, run, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":{"data":"tx not found"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":{"hash":"H1","tx_result":{"code":0}}}`))
	}))
	h, pending, err := s.Send(context.Background(), "saicon1to", 1000)
	if err != nil || pending || h != "H1" {
		t.Fatalf("h=%s pending=%v err=%v", h, pending, err)
	}
	if !strings.Contains(strings.Join(gotArgs, " "), "1000nscn") {
		t.Fatalf("args %v", gotArgs)
	}
}

func TestSendRejectedAndFailedOnChain(t *testing.T) {
	s := rig(t, func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"code":5,"txhash":"H","raw_log":"insufficient funds"}`), nil
	}, http.NotFoundHandler())
	if _, _, err := s.Send(context.Background(), "x", 1); err == nil || !strings.Contains(err.Error(), "insufficient") {
		t.Fatalf("want rejection, got %v", err)
	}
	s = rig(t, func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"code":0,"txhash":"H","raw_log":""}`), nil
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"hash":"H","tx_result":{"code":11}}}`))
	}))
	if _, _, err := s.Send(context.Background(), "x", 1); err == nil {
		t.Fatal("on-chain failure must be an error")
	}
}

func TestSendPendingOnTimeout(t *testing.T) {
	s := rig(t, func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"code":0,"txhash":"H","raw_log":""}`), nil
	}, http.NotFoundHandler())
	h, pending, err := s.Send(context.Background(), "x", 1)
	if err != nil || !pending || h != "H" {
		t.Fatalf("h=%s pending=%v err=%v", h, pending, err)
	}
}

func TestBalance(t *testing.T) {
	s := rig(t, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "saicon1x") {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"balance":{"denom":"nscn","amount":"12345"}}`))
	}))
	if n, err := s.Balance(context.Background()); err != nil || n != 12345 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestImportKeyUsesStdinOnly(t *testing.T) {
	dir := t.TempDir()
	mf := filepath.Join(dir, "m")
	_ = os.WriteFile(mf, []byte("secret words here\n"), 0o600)
	imported := false
	run := func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "secret") {
			t.Fatal("mnemonic leaked into arguments")
		}
		switch args[1] {
		case "show":
			if !imported {
				return nil, os.ErrNotExist
			}
			return []byte("saicon1abc\n"), nil
		case "add":
			if !strings.Contains(stdin, "secret words here") {
				t.Fatal("mnemonic not on stdin")
			}
			imported = true
		}
		return nil, nil
	}
	s, err := New(context.Background(), Config{KeyName: "faucet", Home: dir, Mnemonic: mf, Run: run})
	if err != nil || s.Address() != "saicon1abc" {
		t.Fatalf("%v", err)
	}
}

func isAmb(err error) (bool, bool) {
	var se *SendError
	if !errors.As(err, &se) {
		return false, false
	}
	return se.Ambiguous, true
}

func TestAmbiguityClassification(t *testing.T) {
	cases := []struct {
		name string
		out  string
		err  error
		amb  bool
	}{
		{"connection error, empty stdout (may be after the node accepted the tx)", "", errors.New("exit 1: post failed: EOF"), true},
		{"connection refused, empty stdout", "", errors.New("exit 1: dial tcp: connection refused"), true},
		{"key not found", "", errors.New("exit 1: faucet.info: key not found"), false},
		{"invalid address", "", errors.New("exit 1: decoding bech32 failed: invalid checksum"), false},
		{"insufficient funds from CheckTx on stderr", "", errors.New("exit 1: spendable balance is smaller: insufficient funds"), false},
		{"cli exits non-zero after printing a hash", `{"code":0,"txhash":"H"}`, errors.New("exit 1"), true},
		{"cli exits non-zero with a CheckTx refusal", `{"code":13,"txhash":"H","raw_log":"fee"}`, errors.New("exit 1"), false},
		{"exit 0 but unreadable output", "multi\nline", nil, true},
		{"CheckTx refusal", `{"code":5,"txhash":"H","raw_log":"x"}`, nil, false},
	}
	for _, c := range cases {
		s := rig(t, func(context.Context, string, ...string) ([]byte, error) { return []byte(c.out), c.err }, http.NotFoundHandler())
		_, _, err := s.Send(context.Background(), "x", 1)
		amb, ok := isAmb(err)
		if !ok || amb != c.amb {
			t.Errorf("%s: ambiguous=%v ok=%v err=%v, want %v", c.name, amb, ok, err, c.amb)
		}
	}
}

func TestKilledByRunTimeoutIsAmbiguous(t *testing.T) {
	s := rig(t, func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, errors.New("signal: killed")
	}, http.NotFoundHandler())
	s.cfg.RunTimeout = 30 * time.Millisecond
	_, _, err := s.Send(context.Background(), "x", 1)
	if amb, _ := isAmb(err); !amb {
		t.Fatalf("a send killed mid-run must be ambiguous, got %v", err)
	}
}

func TestQueueTimeoutIsNotSent(t *testing.T) {
	release := make(chan struct{})
	s := rig(t, func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-release
		return []byte(`{"code":0,"txhash":"H"}`), nil
	}, http.NotFoundHandler())
	s.sem = make(chan struct{}, 1)
	go func() { _, _, _ = s.Send(context.Background(), "a", 1) }()
	time.Sleep(50 * time.Millisecond) // first send holds the lock
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := s.Send(ctx, "b", 1)
	close(release)
	if amb, ok := isAmb(err); !ok || amb {
		t.Fatalf("waiting past the deadline must be a clear not-sent error, got %v", err)
	}
}
