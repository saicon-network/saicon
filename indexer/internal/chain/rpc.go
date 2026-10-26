// Package chain reads the SAiCoN chain over its two public interfaces.
//
// RPC is used for one thing only: the proposer of a block. That fact is in
// every header and in no query — /cosmos/base/tendermint/v1beta1/blocks without
// a height returns 501, and CometBFT's block_search indexes emitted events, not
// header fields. Everything else comes from REST, which carries the chain's own
// computed numbers.
package chain

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type RPC struct {
	base string
	c    *http.Client
}

func NewRPC(baseURL string) *RPC {
	return &RPC{base: baseURL, c: &http.Client{Timeout: 20 * time.Second}}
}

type Status struct {
	ChainID      string
	LatestHeight int64
	// EarliestBlockTime is the timestamp of the earliest block this node still
	// retains, from sync_info.earliest_block_time. It is NOT the chain's
	// genesis time: every non-archive node prunes, so the earliest block it
	// still holds is not the first block it ever had.
	EarliestBlockTime time.Time
	// EarliestHeight is the earliest block this node still retains, from
	// sync_info.earliest_block_height. A fresh database seeds its cursor here
	// rather than at height 1, which a pruned node no longer has.
	EarliestHeight int64
}

func (r *RPC) Status(ctx context.Context) (Status, error) {
	var body struct {
		Result struct {
			NodeInfo struct {
				Network string `json:"network"`
			} `json:"node_info"`
			SyncInfo struct {
				LatestBlockHeight   string    `json:"latest_block_height"`
				EarliestBlockHeight string    `json:"earliest_block_height"`
				EarliestBlockTime   time.Time `json:"earliest_block_time"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := getJSON(ctx, r.c, r.base+"/status", &body); err != nil {
		return Status{}, fmt.Errorf("rpc status: %w", err)
	}
	h, err := parseInt64(body.Result.SyncInfo.LatestBlockHeight)
	if err != nil {
		return Status{}, fmt.Errorf("rpc status: latest_block_height: %w", err)
	}
	eh, err := parseInt64(body.Result.SyncInfo.EarliestBlockHeight)
	if err != nil {
		return Status{}, fmt.Errorf("rpc status: earliest_block_height: %w", err)
	}
	return Status{
		ChainID:           body.Result.NodeInfo.Network,
		LatestHeight:      h,
		EarliestBlockTime: body.Result.SyncInfo.EarliestBlockTime,
		EarliestHeight:    eh,
	}, nil
}

// ProposerAt returns the consensus address that proposed the block at height,
// as the hex string CometBFT puts in the header.
func (r *RPC) ProposerAt(ctx context.Context, height int64) (string, error) {
	var body struct {
		Result struct {
			Block struct {
				Header struct {
					ProposerAddress string `json:"proposer_address"`
				} `json:"header"`
			} `json:"block"`
		} `json:"result"`
	}
	url := fmt.Sprintf("%s/block?height=%d", r.base, height)
	if err := getJSON(ctx, r.c, url, &body); err != nil {
		return "", fmt.Errorf("rpc block %d: %w", height, err)
	}
	p := body.Result.Block.Header.ProposerAddress
	if p == "" {
		return "", fmt.Errorf("rpc block %d: header carries no proposer_address", height)
	}
	return p, nil
}

// Tip satisfies follow.Source.
func (r *RPC) Tip(ctx context.Context) (int64, error) {
	s, err := r.Status(ctx)
	if err != nil {
		return 0, err
	}
	return s.LatestHeight, nil
}
