package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
)

// noRecordError separates "the chain has no record" from "the request failed".
// The callers turn the first into NULL and the second into a retry.
type noRecordError struct{ url string }

func (e noRecordError) Error() string { return "no record: " + e.url }

type REST struct {
	base  string
	c     *http.Client
	calls *countingTransport
}

func NewREST(baseURL string) *REST {
	c, ct := newCountingClient()
	return &REST{base: baseURL, c: c, calls: ct}
}

// Calls is the number of HTTP requests this client has sent.
func (r *REST) Calls() uint64 { return r.calls.n.Load() }

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
	// Warp is the validator's WARP after this epoch's update, exactly as the
	// chain wrote it (an 18-decimal LegacyDec string). nil when the snapshot
	// carries none: a snapshot written before the WARP engine answers
	// "warp": null, and a node that predates the field omits it. Both mean
	// unknown, never zero. A value that is not a decimal string from 0 to 100
	// is not kept either: Warp is nil and InvalidWarp holds what the node sent,
	// for the caller to log.
	Warp        *string
	InvalidWarp string
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

// stakingValidatorsBody is the x/staking validators list as the gateway writes it.
type stakingValidatorsBody struct {
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

func (b stakingValidatorsBody) validators() []Validator {
	out := make([]Validator, 0, len(b.Validators))
	for _, v := range b.Validators {
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
	return out
}

const stakingValidatorsPath = "/cosmos/staking/v1beta1/validators?pagination.limit=500"

func (r *REST) Validators(ctx context.Context) ([]Validator, error) {
	var body stakingValidatorsBody
	if err := getJSON(ctx, r.c, r.base+stakingValidatorsPath, &body); err != nil {
		return nil, fmt.Errorf("validators: %w", err)
	}
	return body.validators(), nil
}

// ValidatorsAt is Validators read at a pinned height, following pagination.next_key.
func (r *REST) ValidatorsAt(ctx context.Context, height int64) ([]Validator, error) {
	var out []Validator
	err := pagesAt(r.base+stakingValidatorsPath, func(u string) (*string, error) {
		var body struct {
			stakingValidatorsBody
			Pagination pagination `json:"pagination"`
		}
		if err := getJSONAt(ctx, r.c, u, height, &body); err != nil {
			return nil, err
		}
		out = append(out, body.validators()...)
		return body.Pagination.NextKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("validators at %d: %w", height, err)
	}
	return out, nil
}

// pagination is the page trailer of a cosmos list answer; next_key is null or
// empty on the last page.
type pagination struct {
	NextKey *string `json:"next_key"`
}

// pagesAt calls page with the list URL, then with pagination.key set to each
// next_key the previous page returned, until a page returns none. A key that
// comes back twice is an error rather than a loop. The key is opaque state the
// gateway reads at the same pinned height, so the pages are one snapshot.
func pagesAt(listURL string, page func(u string) (*string, error)) error {
	seen := map[string]bool{}
	u := listURL
	for {
		next, err := page(u)
		if err != nil {
			return err
		}
		if next == nil || *next == "" {
			return nil
		}
		if seen[*next] {
			return fmt.Errorf("next_key %q repeats", *next)
		}
		seen[*next] = true
		u = listURL + "&pagination.key=" + url.QueryEscape(*next)
	}
}

// registryValidatorsBody is the x/validatorregistry list as the gateway writes it.
type registryValidatorsBody struct {
	Validators []struct {
		ValidatorAddress   string `json:"validator_address"`
		Status             string `json:"status"`
		BondingStart       string `json:"bonding_start"`
		BondingStartHeight string `json:"bonding_start_height"`
		LockupSeconds      string `json:"lockup_duration_seconds"`
	} `json:"validators"`
}

func (b registryValidatorsBody) entries() ([]RegistryEntry, error) {
	out := make([]RegistryEntry, 0, len(b.Validators))
	for _, v := range b.Validators {
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

const registryValidatorsPath = "/saicon/chain/validatorregistry/v1/validators?pagination.limit=500"

func (r *REST) RegistryValidators(ctx context.Context) ([]RegistryEntry, error) {
	var body registryValidatorsBody
	if err := getJSON(ctx, r.c, r.base+registryValidatorsPath, &body); err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	return body.entries()
}

// RegistryValidatorsAt is RegistryValidators read at a pinned height, following
// pagination.next_key.
func (r *REST) RegistryValidatorsAt(ctx context.Context, height int64) ([]RegistryEntry, error) {
	var out []RegistryEntry
	err := pagesAt(r.base+registryValidatorsPath, func(u string) (*string, error) {
		var body struct {
			registryValidatorsBody
			Pagination *pagination `json:"pagination"`
		}
		if err := getJSONAt(ctx, r.c, u, height, &body); err != nil {
			return nil, err
		}
		es, err := body.entries()
		if err != nil {
			return nil, err
		}
		out = append(out, es...)
		if body.Pagination == nil { // the registry answers "pagination": null for one page
			return nil, nil
		}
		return body.Pagination.NextKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("registry at %d: %w", height, err)
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
			ComputedScore string          `json:"computed_score"`
			ComputedAt    string          `json:"computed_at"`
			BlocksSigned  string          `json:"blocks_signed"`
			BlocksMissed  string          `json:"blocks_missed"`
			Warp          json.RawMessage `json:"warp"`
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
	warp, invalid := parseWarp(body.Snapshot.Warp)
	return &Snapshot{
		ComputedScore: body.Snapshot.ComputedScore,
		ComputedAt:    body.Snapshot.ComputedAt,
		BlocksSigned:  signed,
		BlocksMissed:  missed,
		Warp:          warp,
		InvalidWarp:   invalid,
	}, nil
}

// parseWarp reads the snapshot's warp field. Absent and null are no value. A
// string holding a decimal from 0 to 100 is the value, unchanged. Anything else
// (a number, an empty string, "-1", "1e2", "NaN", 101) is no value either, and
// is returned as invalid so the caller can say so.
func parseWarp(raw json.RawMessage) (warp *string, invalid string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || !ValidWarp(s) {
		return nil, string(raw)
	}
	return &s, ""
}

var warpMax = big.NewRat(100, 1)

// ValidWarp reports whether s is a WARP as the chain writes one: a plain
// decimal string, no sign and no exponent, from 0 to 100.
func ValidWarp(s string) bool {
	if !reDec.MatchString(s) {
		return false
	}
	v, ok := new(big.Rat).SetString(s)
	return ok && v.Cmp(warpMax) <= 0
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

// getJSON reads the latest state of url. The 404 rule and the error shapes are
// fetchJSON's (at.go); a pinned read is getJSONAt.
func getJSON(ctx context.Context, c *http.Client, url string, out any) error {
	return fetchJSON(ctx, c, url, nil, out)
}
