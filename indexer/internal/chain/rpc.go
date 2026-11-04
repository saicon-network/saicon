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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type RPC struct {
	base  string
	c     *http.Client
	calls *countingTransport
}

func NewRPC(baseURL string) *RPC {
	c, ct := newCountingClient()
	return &RPC{base: baseURL, c: c, calls: ct}
}

// Calls is the number of HTTP requests this client has sent.
func (r *RPC) Calls() uint64 { return r.calls.n.Load() }

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

// SetMember is one validator of the CometBFT set at a height.
type SetMember struct {
	Address     string // upper-case hex consensus address, as CometBFT writes it
	PubKey      string // base64 ed25519 key
	VotingPower int64
}

const (
	validatorsPerPage = 100
	rpcBodyLimit      = 8 << 20
)

// getRPC reads a JSON-RPC-over-HTTP URI answer into out (the "result" member). CometBFT
// reports a refused query as an {"error":{…}} envelope, with HTTP 500 or with 200
// depending on the version, so the envelope is looked at before the status; its text is
// classified into ErrHeightUnavailable / ErrHeightAhead where it names one.
func getRPC(ctx context.Context, c *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, rpcBodyLimit))
	if err != nil {
		return err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		} `json:"error"`
	}
	envErr := json.Unmarshal(b, &env)
	if envErr == nil && env.Error != nil {
		text := env.Error.Message + " " + string(env.Error.Data)
		return fmt.Errorf("rpc error from %s: %w", url, classify(text))
	}
	if resp.StatusCode != http.StatusOK {
		if len(b) > errorBodyLimit {
			b = b[:errorBodyLimit]
		}
		return &statusError{url: url, status: resp.StatusCode, body: string(b)}
	}
	if envErr != nil {
		return fmt.Errorf("rpc answer from %s: %w", url, envErr)
	}
	if len(env.Result) == 0 {
		return fmt.Errorf("rpc answer from %s: no result", url)
	}
	return json.Unmarshal(env.Result, out)
}

// ValidatorSetAt returns the CometBFT validator set at height, paging /validators until
// it holds the reported total. The answer must be for the height asked.
func (r *RPC) ValidatorSetAt(ctx context.Context, height int64) ([]SetMember, error) {
	var out []SetMember
	seen := map[string]bool{}
	for page := 1; ; page++ {
		var body struct {
			BlockHeight string `json:"block_height"`
			Validators  []struct {
				Address string `json:"address"`
				PubKey  struct {
					Value string `json:"value"`
				} `json:"pub_key"`
				VotingPower string `json:"voting_power"`
			} `json:"validators"`
			Total string `json:"total"`
		}
		url := fmt.Sprintf("%s/validators?height=%d&page=%d&per_page=%d", r.base, height, page, validatorsPerPage)
		if err := getRPC(ctx, r.c, url, &body); err != nil {
			return nil, fmt.Errorf("rpc validators at %d: %w", height, err)
		}
		if body.BlockHeight != strconv.FormatInt(height, 10) {
			return nil, fmt.Errorf("rpc validators at %d: answer is for height %q", height, body.BlockHeight)
		}
		total, err := strconv.Atoi(body.Total)
		if err != nil || total <= 0 {
			return nil, fmt.Errorf("rpc validators at %d: total %q", height, body.Total)
		}
		if len(body.Validators) == 0 {
			return nil, fmt.Errorf("rpc validators at %d: page %d is empty with %d of %d members read", height, page, len(out), total)
		}
		for _, v := range body.Validators {
			p, err := strconv.ParseInt(v.VotingPower, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("rpc validators at %d: voting_power %q of %s: %w", height, v.VotingPower, v.Address, err)
			}
			// A node that pages wrongly can repeat a member; counted twice, its
			// power would be inflated and the set size wrong.
			if seen[strings.ToUpper(v.Address)] {
				return nil, fmt.Errorf("rpc validators at %d: %s appears twice", height, v.Address)
			}
			seen[strings.ToUpper(v.Address)] = true
			out = append(out, SetMember{Address: v.Address, PubKey: v.PubKey.Value, VotingPower: p})
		}
		if len(out) >= total {
			if len(out) > total {
				return nil, fmt.Errorf("rpc validators at %d: %d members read, total is %d", height, len(out), total)
			}
			return out, nil
		}
	}
}

var blockHashPattern = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)

// BlockHashAt returns the hash of the block at height, from its commit.
func (r *RPC) BlockHashAt(ctx context.Context, height int64) (string, error) {
	var body struct {
		SignedHeader struct {
			Header struct {
				Height string `json:"height"`
			} `json:"header"`
			Commit struct {
				BlockID struct {
					Hash string `json:"hash"`
				} `json:"block_id"`
			} `json:"commit"`
		} `json:"signed_header"`
	}
	if err := getRPC(ctx, r.c, fmt.Sprintf("%s/commit?height=%d", r.base, height), &body); err != nil {
		return "", fmt.Errorf("rpc commit at %d: %w", height, err)
	}
	if got := body.SignedHeader.Header.Height; got != strconv.FormatInt(height, 10) {
		return "", fmt.Errorf("rpc commit at %d: answer is for height %q", height, got)
	}
	h := body.SignedHeader.Commit.BlockID.Hash
	if !blockHashPattern.MatchString(h) {
		return "", fmt.Errorf("rpc commit at %d: block hash %q", height, h)
	}
	return h, nil
}
