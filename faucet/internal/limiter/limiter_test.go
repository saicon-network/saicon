package limiter

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

var ctx = context.Background()

func open(t *testing.T, path string, cfg Config, clock *time.Time) *Limiter {
	t.Helper()
	l, err := Open(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	l.now = func() time.Time { return *clock }
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func cfg() Config {
	return Config{Amount: 10, Budget: 30, Window: 24 * time.Hour, BudgetWindow: 24 * time.Hour}
}

func reason(t *testing.T, err error) string {
	t.Helper()
	le, ok := IsLimit(err)
	if !ok {
		t.Fatalf("want LimitError, got %v", err)
	}
	return le.Reason
}

func TestAddressAndIPLimits(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := open(t, filepath.Join(t.TempDir(), "f.db"), cfg(), &now)
	if _, err := l.Reserve(ctx, "a1", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	_, err := l.Reserve(ctx, "a1", "2.2.2.2")
	if reason(t, err) != ReasonAddress {
		t.Fatal("same address must be limited")
	}
	_, err = l.Reserve(ctx, "a2", "1.1.1.1")
	if reason(t, err) != ReasonIP {
		t.Fatal("same ip must be limited")
	}
	if _, err := l.Reserve(ctx, "a2", "2.2.2.2"); err != nil {
		t.Fatalf("other address+ip must pass: %v", err)
	}
}

func TestWindowExpiry(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := open(t, filepath.Join(t.TempDir(), "f.db"), cfg(), &now)
	if _, err := l.Reserve(ctx, "a1", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24*time.Hour - time.Second)
	_, err := l.Reserve(ctx, "a1", "1.1.1.1")
	le, _ := IsLimit(err)
	if le == nil || le.RetryAfter != time.Second {
		t.Fatalf("want retry in 1s, got %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, err := l.Reserve(ctx, "a1", "1.1.1.1"); err != nil {
		t.Fatalf("window expired, must pass: %v", err)
	}
}

func TestFailedSendDoesNotConsume(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := open(t, filepath.Join(t.TempDir(), "f.db"), cfg(), &now)
	id, err := l.Reserve(ctx, "a1", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Reserve(ctx, "a1", "1.1.1.1"); err != nil {
		t.Fatalf("released allowance must be reusable: %v", err)
	}
	left, _ := l.BudgetLeft(ctx)
	if left != 20 {
		t.Fatalf("budget left %d, want 20", left)
	}
}

func TestBudget(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := open(t, filepath.Join(t.TempDir(), "f.db"), cfg(), &now)
	for i, a := range []string{"a1", "a2", "a3"} {
		if _, err := l.Reserve(ctx, a, a); err != nil {
			t.Fatalf("grant %d: %v", i, err)
		}
	}
	_, err := l.Reserve(ctx, "a4", "a4")
	if reason(t, err) != ReasonBudget {
		t.Fatal("budget must be exhausted")
	}
	if left, _ := l.BudgetLeft(ctx); left != 0 {
		t.Fatalf("left %d", left)
	}
	now = now.Add(24*time.Hour + time.Second)
	if _, err := l.Reserve(ctx, "a4", "a4"); err != nil {
		t.Fatalf("budget must roll over: %v", err)
	}
}

func TestPersistsAcrossRestart(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	path := filepath.Join(t.TempDir(), "f.db")
	l, err := Open(path, cfg())
	if err != nil {
		t.Fatal(err)
	}
	l.now = func() time.Time { return now }
	id, err := l.Reserve(ctx, "a1", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(ctx, id, "ABC"); err != nil {
		t.Fatal(err)
	}
	// A reservation never committed nor released (crash mid-send) also survives.
	if _, err := l.Reserve(ctx, "a2", "2.2.2.2"); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()

	l2 := open(t, path, cfg(), &now)
	if reason(t, func() error { _, e := l2.Reserve(ctx, "a1", "9.9.9.9"); return e }()) != ReasonAddress {
		t.Fatal("address limit lost on restart")
	}
	if reason(t, func() error { _, e := l2.Reserve(ctx, "a3", "2.2.2.2"); return e }()) != ReasonIP {
		t.Fatal("pending reservation lost on restart")
	}
	if left, _ := l2.BudgetLeft(ctx); left != 10 {
		t.Fatalf("budget left %d, want 10", left)
	}
}

func TestZeroAmountRejected(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "f.db"), Config{}); err == nil {
		t.Fatal("zero amount accepted")
	}
}
