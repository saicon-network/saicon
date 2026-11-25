// Package config reads the faucet's settings from the environment.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"time"

	"github.com/saicon-network/saicon/faucet/internal/clientip"
)

type Config struct {
	Listen        string
	RPC, REST     string
	ChainID       string
	Denom         string
	Prefix        string
	Exponent      int    // display exponent: 1 SCN = 10^Exponent base units
	Amount        uint64 // base units per request
	DailyBudget   uint64 // base units per rolling 24 h
	Window        time.Duration
	DataDir       string
	KeyringHome   string
	KeyName       string
	MnemonicFile  string
	Binary        string
	Fees, Gas     string
	TxWait        time.Duration
	QueueTimeout  time.Duration
	RunTimeout    time.Duration
	TrustedProxy  []netip.Prefix
	ExplorerTxURL string
	PublicOrigin  string
}

func str(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func u64(key string, def uint64) (uint64, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: want a whole number of base units, got %q", key, v)
	}
	return n, nil
}

func dur(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s: want a positive duration like 24h, got %q", key, v)
	}
	return d, nil
}

// Load reads FAUCET_* variables. Defaults: 100 SCN per request (the DevNet's minimum delegation), 5,000 SCN/day,
// 24 h window, with 1 SCN = 1e8 nscn.
func Load() (Config, error) {
	c := Config{
		Listen:        str("FAUCET_LISTEN", ":8080"),
		RPC:           str("FAUCET_RPC_URL", "http://saicon-node:26657"),
		REST:          str("FAUCET_REST_URL", "http://saicon-node:1317"),
		ChainID:       str("FAUCET_CHAIN_ID", "saicon-devnet-1"),
		Denom:         str("FAUCET_DENOM", "nscn"),
		Prefix:        str("FAUCET_BECH32_PREFIX", "saicon"),
		DataDir:       str("FAUCET_DATA_DIR", "/data"),
		KeyName:       str("FAUCET_KEY_NAME", "faucet"),
		MnemonicFile:  os.Getenv("FAUCET_MNEMONIC_FILE"),
		Binary:        str("FAUCET_SAICOND_BIN", "saicond"),
		Fees:          str("FAUCET_FEES", "0nscn"),
		Gas:           str("FAUCET_GAS", "200000"),
		PublicOrigin:  os.Getenv("FAUCET_PUBLIC_ORIGIN"),
		ExplorerTxURL: str("FAUCET_EXPLORER_TX_URL", "https://explorer.saicon.world/saicon/tx/"),
	}
	c.KeyringHome = str("FAUCET_KEYRING_DIR", c.DataDir+"/keyring")
	var err error
	if e, err := u64("FAUCET_DENOM_EXPONENT", 8); err != nil || e > 15 {
		// 5,000 display units must fit in a uint64 (5000 * 10^16 overflows).
		return c, fmt.Errorf("FAUCET_DENOM_EXPONENT: want 0..15")
	} else {
		c.Exponent = int(e)
	}
	unit := uint64(1)
	for i := 0; i < c.Exponent; i++ {
		unit *= 10
	}
	if c.Amount, err = u64("FAUCET_AMOUNT", 100*unit); err != nil {
		return c, err
	}
	if c.DailyBudget, err = u64("FAUCET_DAILY_BUDGET", 5000*unit); err != nil {
		return c, err
	}
	if c.Amount == 0 {
		return c, fmt.Errorf("FAUCET_AMOUNT must be positive")
	}
	if c.DailyBudget < c.Amount {
		return c, fmt.Errorf("FAUCET_DAILY_BUDGET (%d) must be at least FAUCET_AMOUNT (%d): every request would be refused", c.DailyBudget, c.Amount)
	}
	if c.Window, err = dur("FAUCET_WINDOW", 24*time.Hour); err != nil {
		return c, err
	}
	// Worst case for one request = QueueTimeout + RunTimeout + TxWait (+ 5 s of
	// slack inside the sender): 30 + 20 + 20 + 5 = 75 s, under Cloudflare's 100 s
	// proxy timeout.
	if c.TxWait, err = dur("FAUCET_TX_WAIT", 20*time.Second); err != nil {
		return c, err
	}
	if c.QueueTimeout, err = dur("FAUCET_QUEUE_TIMEOUT", 30*time.Second); err != nil {
		return c, err
	}
	if c.RunTimeout, err = dur("FAUCET_RUN_TIMEOUT", 20*time.Second); err != nil {
		return c, err
	}
	if c.TrustedProxy, err = clientip.ParseCIDRs(os.Getenv("FAUCET_TRUSTED_PROXIES")); err != nil {
		return c, fmt.Errorf("FAUCET_TRUSTED_PROXIES: %w", err)
	}
	return c, nil
}
