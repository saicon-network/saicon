package config

import "testing"

func TestDefaultsAllowOneDelegation(t *testing.T) {
	for _, k := range []string{"FAUCET_AMOUNT", "FAUCET_DAILY_BUDGET", "FAUCET_DENOM_EXPONENT"} {
		t.Setenv(k, "")
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// 100 SCN = the DevNet minimum delegation (min_delegation 10000000000 nscn).
	if c.Amount != 10_000_000_000 || c.DailyBudget != 500_000_000_000 {
		t.Fatalf("defaults: amount %d, budget %d", c.Amount, c.DailyBudget)
	}
}

func TestRejectsUnsafeSettings(t *testing.T) {
	t.Run("exponent overflow", func(t *testing.T) {
		t.Setenv("FAUCET_DENOM_EXPONENT", "16")
		if _, err := Load(); err == nil {
			t.Fatal("exponent 16 overflows the default budget and must be refused")
		}
	})
	t.Run("budget below amount", func(t *testing.T) {
		t.Setenv("FAUCET_DENOM_EXPONENT", "")
		t.Setenv("FAUCET_AMOUNT", "10000000000")
		t.Setenv("FAUCET_DAILY_BUDGET", "1")
		if _, err := Load(); err == nil {
			t.Fatal("a budget smaller than one payout must be refused")
		}
	})
	t.Run("exponent 15 defaults", func(t *testing.T) {
		t.Setenv("FAUCET_AMOUNT", "")
		t.Setenv("FAUCET_DAILY_BUDGET", "")
		t.Setenv("FAUCET_DENOM_EXPONENT", "15")
		c, err := Load()
		if err != nil || c.DailyBudget != 5_000_000_000_000_000_000 {
			t.Fatalf("%v %d", err, c.DailyBudget)
		}
	})
}
