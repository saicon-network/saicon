package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/saicon-network/saicon/faucet/internal/sender"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/saicon-network/saicon/faucet/internal/bech32"
	"github.com/saicon-network/saicon/faucet/internal/clientip"
	"github.com/saicon-network/saicon/faucet/internal/limiter"
)

type fake struct {
	mu    sync.Mutex
	fail  bool
	sends []string
}

func (f *fake) Send(_ context.Context, to string, _ uint64) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return "", false, errors.New("boom")
	}
	f.sends = append(f.sends, to)
	return "TXHASH", false, nil
}
func (f *fake) Address() string                         { return "saicon1faucet" }
func (f *fake) Balance(context.Context) (uint64, error) { return 777, nil }

func addr(n byte) string {
	raw := make([]byte, 32) // 32 five-bit groups = 20 bytes
	for i := range raw {
		raw[i] = n
	}
	return bech32.Encode("saicon", raw[:32])
}

func setup(t *testing.T, budget uint64) (*httptest.Server, *fake) {
	t.Helper()
	l, err := limiter.Open(filepath.Join(t.TempDir(), "f.db"), limiter.Config{Amount: 10, Budget: budget, Window: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	f := &fake{}
	s := New(Options{Sender: f, Limiter: l, IPs: clientip.New(nil), ChainID: "c", Denom: "nscn",
		Prefix: "saicon", Amount: 10, Exponent: 8, ExplorerTxURL: "https://x/tx/"})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, f
}

func post(t *testing.T, ts *httptest.Server, body string) (int, string) {
	t.Helper()
	r, err := http.Post(ts.URL+"/request", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b := make([]byte, 1024)
	n, _ := r.Body.Read(b)
	return r.StatusCode, string(b[:n])
}

func TestFlow(t *testing.T) {
	ts, f := setup(t, 1000)
	a := addr(1)
	code, body := post(t, ts, `{"address":"`+a+`"}`)
	if code != 200 || !strings.Contains(body, `"txhash":"TXHASH"`) || !strings.Contains(body, `"amount":"10nscn"`) {
		t.Fatalf("%d %s", code, body)
	}
	if code, body = post(t, ts, `{"address":"`+a+`"}`); code != 429 || !strings.Contains(body, "address_limited") {
		t.Fatalf("%d %s", code, body)
	}
	if code, body = post(t, ts, `{"address":"`+addr(2)+`"}`); code != 429 || !strings.Contains(body, "ip_limited") {
		t.Fatalf("%d %s", code, body)
	}
	if len(f.sends) != 1 {
		t.Fatalf("sends %v", f.sends)
	}
}

func TestBadInput(t *testing.T) {
	ts, f := setup(t, 1000)
	for _, b := range []string{`{`, `{"address":""}`, `{"address":"cosmos1abc"}`,
		`{"address":"` + bech32.Encode("saiconvaloper", make([]byte, 32)) + `"}`} {
		if code, _ := post(t, ts, b); code != 400 {
			t.Errorf("%s -> %d", b, code)
		}
	}
	if len(f.sends) != 0 {
		t.Fatal("sent on bad input")
	}
}

func TestInvalidAddressMessageIsHuman(t *testing.T) {
	ts, _ := setup(t, 1000)
	good := addr(1)
	valid := bech32.Encode("saicon", make([]byte, 32))
	mixed := strings.ToUpper(valid[:12]) + valid[12:]
	typo := valid[:len(valid)-1] + "q"
	if typo == valid {
		typo = valid[:len(valid)-1] + "p"
	}
	const start = "starts with saicon1"
	const mistyped = "incomplete or mistyped"
	cases := map[string]string{
		`{"address":""}`:           "Enter your address.",
		`{"address":"cosmos1abc"}`: start,
		`{"address":"` + bech32.Encode("cosmos", make([]byte, 32)) + `"}`:        start,
		`{"address":"` + bech32.Encode("saiconvaloper", make([]byte, 32)) + `"}`: start,
		`{"address":"saicon1abc"}`:                 mistyped,
		`{"address":"` + good[:len(good)-6] + `"}`: mistyped,
		`{"address":"` + typo + `"}`:               mistyped,
		`{"address":"` + mixed + `"}`:              mistyped,
	}
	for in, want := range cases {
		code, body := post(t, ts, in)
		if code != 400 || !strings.Contains(body, want) {
			t.Errorf("%s -> %d %s, want %q", in, code, body, want)
		}
		for _, leak := range []string{"bech32", "separator", "checksum", "invalid character"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s: message leaks parser text %q: %s", in, leak, body)
			}
		}
	}
}

func TestFailedSendKeepsAllowance(t *testing.T) {
	ts, f := setup(t, 1000)
	f.fail = true
	if code, body := post(t, ts, `{"address":"`+addr(1)+`"}`); code != 502 || strings.Contains(body, "boom") {
		t.Fatalf("%d %s", code, body)
	}
	f.fail = false
	if code, body := post(t, ts, `{"address":"`+addr(1)+`"}`); code != 200 {
		t.Fatalf("retry after failure: %d %s", code, body)
	}
}

func TestBudgetExhausted(t *testing.T) {
	ts, _ := setup(t, 0)
	code, body := post(t, ts, `{"address":"`+addr(1)+`"}`)
	if code != 503 || !strings.Contains(body, "budget_exhausted") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestStatusAndPages(t *testing.T) {
	ts, _ := setup(t, 1000)
	for path, want := range map[string]string{"/status": `"balance":"777"`, "/healthz": "ok", "/": "SAiCoN devnet faucet"} {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		r.Body.Close()
		if r.StatusCode != 200 || !strings.Contains(string(b[:n]), want) {
			t.Errorf("%s: %d %s", path, r.StatusCode, b[:n])
		}
	}
}

// The page reads the rules from /status instead of hard-coding them.
func TestStatusReportsRules(t *testing.T) {
	ts, _ := setup(t, 1000) // limiter: budget 1000, window 1 h
	r, err := http.Get(ts.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	// Must be the limiter's own configuration (setup builds it with 1000 and 1 h).
	if got["window_seconds"] != float64(3600) || got["daily_budget"] != "1000" {
		t.Errorf("rules: window_seconds=%v daily_budget=%v", got["window_seconds"], got["daily_budget"])
	}
}

func do(t *testing.T, ts *httptest.Server, ctype string, hdr map[string]string, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/request", strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Regression: a cross-site page could make a browser send a text/plain POST
// (no preflight) from the visitor's IP and spend the visitor's allowance.
func TestCSRFRejected(t *testing.T) {
	ts, f := setup(t, 1000)
	body := `{"address":"` + addr(7) + `"}`
	host := strings.TrimPrefix(ts.URL, "http://")
	cases := []struct {
		name  string
		ctype string
		hdr   map[string]string
		want  int
	}{
		{"text/plain cross-origin", "text/plain;charset=UTF-8", map[string]string{"Origin": "https://evil.example"}, 415},
		{"text/plain, no headers", "text/plain", nil, 415},
		{"no content type", "", nil, 415},
		{"json from another origin", "application/json", map[string]string{"Origin": "https://evil.example"}, 403},
		{"json with Origin null", "application/json", map[string]string{"Origin": "null"}, 403},
		{"json cross-site fetch metadata", "application/json", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"json same-site (sibling subdomain)", "application/json", map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
	}
	for _, c := range cases {
		if got := do(t, ts, c.ctype, c.hdr, body); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
	if len(f.sends) != 0 {
		t.Fatalf("paid out for a rejected request: %v", f.sends)
	}
	// The faucet's own page and plain API clients still work.
	if got := do(t, ts, "application/json; charset=utf-8", map[string]string{"Origin": "http://" + host, "Sec-Fetch-Site": "same-origin"}, body); got != 200 {
		t.Errorf("same-origin page request: %d", got)
	}
}

func TestPublicOriginExact(t *testing.T) {
	l, _ := limiter.Open(filepath.Join(t.TempDir(), "f.db"), limiter.Config{Amount: 10, Budget: 1000, Window: time.Hour})
	t.Cleanup(func() { _ = l.Close() })
	s := New(Options{Sender: &fake{}, Limiter: l, IPs: clientip.New(nil), Denom: "nscn", Prefix: "saicon", Amount: 10,
		PublicOrigin: "https://faucet.example"})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	body := `{"address":"` + addr(3) + `"}`
	if got := do(t, ts, "application/json", map[string]string{"Origin": "https://evil.example"}, body); got != 403 {
		t.Errorf("foreign origin: %d", got)
	}
	if got := do(t, ts, "application/json", map[string]string{"Origin": "https://faucet.example"}, body); got != 200 {
		t.Errorf("configured origin: %d", got)
	}
}

// Regression: under a burst, requests queued behind the send lock used to run
// out of time, get a 502 saying "nothing was used", and keep their reservation
// (release ran on the expired context), so address, IP and budget stayed
// charged although nothing was broadcast for them.
func TestQueueBacklogDoesNotLeakAllowance(t *testing.T) {
	var broadcasts int32
	run := func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "keys" {
			return []byte("saicon1faucet\n"), nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		n := atomic.AddInt32(&broadcasts, 1)
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
			return nil, errors.New("signal: killed")
		}
		return []byte(fmt.Sprintf(`{"code":0,"txhash":"H%d"}`, n)), nil
	}
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"result":{"hash":"x","tx_result":{"code":0}}}`)
	}))
	defer rpc.Close()
	snd, err := sender.New(context.Background(), sender.Config{KeyName: "faucet", Run: run, RPC: rpc.URL, Wait: time.Second, Denom: "nscn"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := limiter.Open(filepath.Join(t.TempDir(), "f.db"), limiter.Config{Amount: 10, Budget: 100000, Window: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s := New(Options{Sender: snd, Limiter: l, IPs: clientip.New([]netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}),
		Denom: "nscn", Prefix: "saicon", Amount: 10, SendTimeout: time.Second})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	const N = 12 // 12 x 400 ms of work against a 1 s deadline: most must time out
	codes := make([]int, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, _ := http.NewRequest("POST", ts.URL+"/request", strings.NewReader(`{"address":"`+addr(byte(i+1))+`"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
			if resp, err := http.DefaultClient.Do(req); err == nil {
				codes[i] = resp.StatusCode
				resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()
	failed, leaked := 0, 0
	for i := 0; i < N; i++ {
		if codes[i] == 200 {
			continue
		}
		failed++
		// A user told "nothing was used" must be able to ask again.
		if _, err := l.Reserve(context.Background(), addr(byte(i+1)), fmt.Sprintf("203.0.113.%d", i+1)); err != nil {
			leaked++
		}
	}
	left, _ := l.BudgetLeft(context.Background())
	paid := uint64(0)
	for _, c := range codes {
		if c == 200 {
			paid += 10
		}
	}
	t.Logf("codes=%v failed=%d leaked=%d broadcasts=%d", codes, failed, leaked, broadcasts)
	if failed == 0 {
		t.Fatal("test did not create a backlog")
	}
	if leaked != 0 {
		t.Fatalf("%d of %d failed requests kept their allowance", leaked, failed)
	}
	// Budget spent = paid grants + the re-reservations just made for failed ones.
	if spent := 100000 - left; spent != paid+uint64(failed)*10 {
		t.Fatalf("budget spent %d, want %d", spent, paid+uint64(failed)*10)
	}
}

type ambiguousFake struct{ fake }

func (a *ambiguousFake) Send(context.Context, string, uint64) (string, bool, error) {
	return "", false, &sender.SendError{Err: errors.New("exit 1"), Ambiguous: true, TxHash: "MAYBE"}
}

func TestAmbiguousSendKeepsAllowance(t *testing.T) {
	l, _ := limiter.Open(filepath.Join(t.TempDir(), "f.db"), limiter.Config{Amount: 10, Budget: 1000, Window: time.Hour})
	t.Cleanup(func() { _ = l.Close() })
	s := New(Options{Sender: &ambiguousFake{}, Limiter: l, IPs: clientip.New(nil), Denom: "nscn", Prefix: "saicon", Amount: 10})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	body := `{"address":"` + addr(5) + `"}`
	if got := do(t, ts, "application/json", nil, body); got != 502 {
		t.Fatalf("first: %d", got)
	}
	if got := do(t, ts, "application/json", nil, body); got != 429 {
		t.Fatalf("retry after an ambiguous send must be refused, got %d", got)
	}
	if left, _ := l.BudgetLeft(context.Background()); left != 990 {
		t.Fatalf("budget left %d, want 990", left)
	}
}

type countingFake struct {
	fake
	calls int32
}

func (c *countingFake) Balance(context.Context) (uint64, error) {
	atomic.AddInt32(&c.calls, 1)
	return 5, nil
}

func TestStatusBalanceCached(t *testing.T) {
	l, _ := limiter.Open(filepath.Join(t.TempDir(), "f.db"), limiter.Config{Amount: 10, Budget: 1000, Window: time.Hour})
	t.Cleanup(func() { _ = l.Close() })
	cf := &countingFake{}
	ts := httptest.NewServer(New(Options{Sender: cf, Limiter: l, IPs: clientip.New(nil), Denom: "nscn", Prefix: "saicon", Amount: 10}).Handler())
	t.Cleanup(ts.Close)
	for i := 0; i < 5; i++ {
		r, err := http.Get(ts.URL + "/status")
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
	}
	if n := atomic.LoadInt32(&cf.calls); n != 1 {
		t.Fatalf("balance queried %d times for 5 status calls, want 1", n)
	}
}
