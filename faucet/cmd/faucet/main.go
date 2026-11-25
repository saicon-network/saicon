// Command faucet hands out small amounts of SAiCoN devnet tokens.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/saicon-network/saicon/faucet/internal/clientip"
	"github.com/saicon-network/saicon/faucet/internal/config"
	"github.com/saicon-network/saicon/faucet/internal/limiter"
	"github.com/saicon-network/saicon/faucet/internal/sender"
	"github.com/saicon-network/saicon/faucet/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lim, err := limiter.Open(filepath.Join(cfg.DataDir, "faucet.db"), limiter.Config{
		Amount: cfg.Amount, Budget: cfg.DailyBudget, Window: cfg.Window, BudgetWindow: 24 * time.Hour,
	})
	if err != nil {
		log.Fatalf("limiter: %v", err)
	}
	defer lim.Close()

	snd, err := sender.New(ctx, sender.Config{
		Binary: cfg.Binary, Home: cfg.KeyringHome, KeyName: cfg.KeyName,
		RPC: cfg.RPC, REST: cfg.REST, ChainID: cfg.ChainID, Denom: cfg.Denom,
		Fees: cfg.Fees, Gas: cfg.Gas, Wait: cfg.TxWait, RunTimeout: cfg.RunTimeout, Mnemonic: cfg.MnemonicFile,
	})
	if err != nil {
		log.Fatalf("sender: %v", err)
	}
	log.Printf("faucet address %s, %d%s per request, budget %d%s per 24h, window %s",
		snd.Address(), cfg.Amount, cfg.Denom, cfg.DailyBudget, cfg.Denom, cfg.Window)

	h := server.New(server.Options{
		Sender: snd, Limiter: lim, IPs: clientip.New(cfg.TrustedProxy),
		ChainID: cfg.ChainID, Denom: cfg.Denom, Prefix: cfg.Prefix, Amount: cfg.Amount,
		Exponent: cfg.Exponent, ExplorerTxURL: cfg.ExplorerTxURL, PublicOrigin: cfg.PublicOrigin, SendTimeout: cfg.QueueTimeout,
	}).Handler()
	srv := &http.Server{Addr: cfg.Listen, Handler: h, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 100 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		// Longer than the worst-case request (~75 s); compose's stop_grace_period is 90 s.
		sc, c := context.WithTimeout(context.Background(), 80*time.Second)
		defer c()
		_ = srv.Shutdown(sc)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}
