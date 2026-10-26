package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// noRecordError separates "the chain has no record" from "the request failed".
// The callers turn the first into NULL and the second into a retry.
type noRecordError struct{ url string }

func (e noRecordError) Error() string { return "no record: " + e.url }

type REST struct {
	base string
	c    *http.Client
}

func NewREST(baseURL string) *REST {
	return &REST{base: baseURL, c: &http.Client{Timeout: 20 * time.Second}}
}

type Validator struct {
	Operator        string
	Moniker         string
	Status          string
	Jailed          bool
	Tokens          string
	CommissionRate  string
	ConsensusPubKey string
}

type RegistryEntry struct {
	Operator           string
	Status             string
	BondingStart       string
	BondingStartHeight int64
	LockupSeconds      int64
}

type EP struct {
	AccumulatedEP   string
	LastEpochUpdate uint64
}

type Snapshot struct {
	ComputedScore string
	ComputedAt    string
	BlocksSigned  uint64
	BlocksMissed  uint64
}

type Reward struct {
	Amount   string
	Eligible bool
	Reason   string
}

func (r *REST) EpochLength(ctx context.Context) (uint64, error) {
	var body struct {
		Params struct {
			EpochLengthBlocks string `json:"epoch_length_blocks"`
		} `json:"params"`
	}
	if err := getJSON(ctx, r.c, r.base+"/saicon/chain/participation/v1/params", &body); err != nil {
		return 0, fmt.Errorf("epoch length: %w", err)
	}
	n, err := strconv.ParseUint(body.Params.EpochLengthBlocks, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("epoch length %q: %w", body.Params.EpochLengthBlocks, err)
	}
	return n, nil
}

func (r *REST) Validators(ctx context.Context) ([]Validator, error) {
	var body struct {
		Validators []struct {
			OperatorAddress string `json:"operator_address"`
			Jailed          bool   `json:"jailed"`
			Status          string `json:"status"`
			Tokens          string `json:"tokens"`
			Description     struct {
				Moniker string `json:"moniker"`
			} `json:"description"`
			Commission struct {
				CommissionRates struct {
					Rate string `json:"rate"`
				} `json:"commission_rates"`
			} `json:"commission"`
			ConsensusPubkey struct {
				Key string `json:"key"`
			} `json:"consensus_pubkey"`
		} `json:"validators"`
	}
	if err := getJSON(ctx, r.c, r.base+"/cosmos/staking/v1beta1/validators?pagination.limit=500", &body); err != nil {
		return nil, fmt.Errorf("validators: %w", err)
	}
	out := make([]Validator, 0, len(body.Validators))
	for _, v := range body.Validators {
		out = append(out, Validator{
			Operator:        v.OperatorAddress,
			Moniker:         v.Description.Moniker,
			Status:          v.Status,
			Jailed:          v.Jailed,
			Tokens:          v.Tokens,
			CommissionRate:  v.Commission.CommissionRates.Rate,
			ConsensusPubKey: v.ConsensusPubkey.Key,
		})
	}
	return out, nil
}

func (r *REST) RegistryValidators(ctx context.Context) ([]RegistryEntry, error) {
	var body struct {
		Validators []struct {
			ValidatorAddress   string `json:"validator_address"`
			Status             string `json:"status"`
			BondingStart       string `json:"bonding_start"`
			BondingStartHeight string `json:"bonding_start_height"`
			LockupSeconds      string `json:"lockup_duration_seconds"`
		} `json:"validators"`
	}
	if err := getJSON(ctx, r.c, r.base+"/saicon/chain/validatorregistry/v1/validators?pagination.limit=500", &body); err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	out := make([]RegistryEntry, 0, len(body.Validators))
	for _, v := range body.Validators {
		h, err := parseInt64(v.BondingStartHeight)
		if err != nil {
			return nil, fmt.Errorf("registry %s: bonding_start_height %q: %w", v.ValidatorAddress, v.BondingStartHeight, err)
		}
		l, err := parseInt64(v.LockupSeconds)
		if err != nil {
			return nil, fmt.Errorf("registry %s: lockup_duration_seconds %q: %w", v.ValidatorAddress, v.LockupSeconds, err)
		}
		out = append(out, RegistryEntry{
			Operator:           v.ValidatorAddress,
			Status:             v.Status,
			BondingStart:       v.BondingStart,
			BondingStartHeight: h,
			LockupSeconds:      l,
		})
	}
	return out, nil
}

func (r *REST) EP(ctx context.Context, operator string) (*EP, error) {
	var body struct {
		EP struct {
			AccumulatedEP   string `json:"accumulated_ep"`
			LastEpochUpdate string `json:"last_epoch_update"`
		} `json:"ep"`
	}
	url := r.base + "/saicon/chain/participation/v1/ep/" + operator
	if err := getJSON(ctx, r.c, url, &body); err != nil {
		if _, ok := err.(noRecordError); ok {
			return nil, nil
		}
		return nil, fmt.Errorf("ep %s: %w", operator, err)
	}
	last, err := strconv.ParseUint(body.EP.LastEpochUpdate, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("ep %s: last_epoch_update %q: %w", operator, body.EP.LastEpochUpdate, err)
	}
	return &EP{AccumulatedEP: body.EP.AccumulatedEP, LastEpochUpdate: last}, nil
}

func (r *REST) Snapshot(ctx context.Context, operator string, epoch uint64) (*Snapshot, error) {
	var body struct {
		Snapshot struct {
			ComputedScore string `json:"computed_score"`
			ComputedAt    string `json:"computed_at"`
			BlocksSigned  string `json:"blocks_signed"`
			BlocksMissed  string `json:"blocks_missed"`
		} `json:"snapshot"`
	}
	url := fmt.Sprintf("%s/saicon/chain/participation/v1/snapshot/%s/%d", r.base, operator, epoch)
	if err := getJSON(ctx, r.c, url, &body); err != nil {
		if _, ok := err.(noRecordError); ok {
			return nil, nil
		}
		return nil, fmt.Errorf("snapshot %s@%d: %w", operator, epoch, err)
	}
	signed, err := strconv.ParseUint(body.Snapshot.BlocksSigned, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s@%d: blocks_signed %q: %w", operator, epoch, body.Snapshot.BlocksSigned, err)
	}
	missed, err := strconv.ParseUint(body.Snapshot.BlocksMissed, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s@%d: blocks_missed %q: %w", operator, epoch, body.Snapshot.BlocksMissed, err)
	}
	return &Snapshot{
		ComputedScore: body.Snapshot.ComputedScore,
		ComputedAt:    body.Snapshot.ComputedAt,
		BlocksSigned:  signed,
		BlocksMissed:  missed,
	}, nil
}

func (r *REST) RewardSnapshot(ctx context.Context, operator string, epoch uint64) (*Reward, error) {
	var body struct {
		Snapshot struct {
			Amount   string `json:"amount"`
			Eligible bool   `json:"eligible"`
			Reason   string `json:"reason"`
		} `json:"snapshot"`
	}
	url := fmt.Sprintf("%s/saicon/chain/rewards/v1/snapshot/%s/%d", r.base, operator, epoch)
	if err := getJSON(ctx, r.c, url, &body); err != nil {
		if _, ok := err.(noRecordError); ok {
			return nil, nil
		}
		return nil, fmt.Errorf("reward %s@%d: %w", operator, epoch, err)
	}
	return &Reward{Amount: body.Snapshot.Amount, Eligible: body.Snapshot.Eligible, Reason: body.Snapshot.Reason}, nil
}

// Compliance reads x/compliance, which is authoritative. The registry carries a
// compliance_flag too, and it arrives free with the list query — but V-4.1
// records that mirror as able to diverge undetected, so reading it here would
// be trading correctness for one HTTP request.
func (r *REST) Compliance(ctx context.Context, operator string) (*bool, error) {
	// The response wrapper is `state`, not `compliance`: the RPC is Compliance,
	// but QueryComplianceResponse holds `ComplianceState state = 1`.
	var body struct {
		State struct {
			Compliant bool `json:"compliant"`
		} `json:"state"`
	}
	url := r.base + "/saicon/chain/compliance/v1/compliance/" + operator
	if err := getJSON(ctx, r.c, url, &body); err != nil {
		if _, ok := err.(noRecordError); ok {
			return nil, nil
		}
		return nil, fmt.Errorf("compliance %s: %w", operator, err)
	}
	v := body.State.Compliant
	return &v, nil
}

func parseInt64(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func getJSON(ctx context.Context, c *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// The chain answers "no record for this address" with 404 and a gRPC-gateway
	// body carrying codes.NotFound (5): {"code":5,"message":"...","details":[]}.
	// That is data, not a failure, and the caller turns it into NULL — but the
	// status code alone is not proof of that: an operator-configured base URL
	// pointed at a public endpoint behind nginx or an ingress can answer an
	// unmatched route with its own generic 404, which must not be silently
	// read as "no record" or every validator quietly becomes NULL. Only a body
	// that actually parses as that gRPC shape earns the "no record" reading;
	// anything else on a 404 is a real error.
	if resp.StatusCode == http.StatusNotFound {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		var grpcErr struct {
			Code int `json:"code"`
		}
		if jsonErr := json.Unmarshal(b, &grpcErr); jsonErr == nil && grpcErr.Code == 5 {
			return noRecordError{url: url}
		}
		return fmt.Errorf("HTTP 404 from %s: body not a chain \"no record\" response: %s", url, b)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, url, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
