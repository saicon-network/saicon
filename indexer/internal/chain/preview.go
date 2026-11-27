package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
)

// The voting-power preview (x/rewards, S10; ADR-0008 "Implementation (S10): the
// preview") is what ADR-0008 §1's voting power would be from the state at a
// height. It is read once per epoch, pinned at the epoch's last height.

// Codes stored when the node does not give a preview. The first four are the
// node's definitive answer for the height, and VotingPowerPreviewAt returns them
// as a *PreviewUnavailable. PreviewError is never an answer: it is what the
// caller stores when transient failures have used up its retries.
const (
	// PreviewRouteMissing: the node does not serve the route (HTTP 501, gRPC
	// Unimplemented, or the gateway's own NotFound for a path it has no route for).
	PreviewRouteMissing = "route_missing"
	// PreviewParamsInvalid: gRPC FailedPrecondition, x/participation's WARP
	// parameters at that height do not validate.
	PreviewParamsInvalid = "params_invalid"
	// PreviewCapsMissing: gRPC NotFound, x/stakeengine had not computed its caps.
	PreviewCapsMissing = "caps_missing"
	// PreviewPruned: the node no longer holds the height.
	PreviewPruned = "pruned"
	// PreviewError: the read kept failing until the caller gave up on it. The
	// last failure is logged, not stored.
	PreviewError = "error"
)

// PreviewUnavailable is the node's definitive answer that there is no preview at
// the height: the caller records Code and does not ask again. An error of any
// other type is transient (a timeout, a dropped connection, a 5xx, a body that
// does not decode, a height the node has not reached): the caller asks again.
type PreviewUnavailable struct {
	Code string
	Err  error
}

func (e *PreviewUnavailable) Error() string {
	return "voting-power preview unavailable (" + e.Code + "): " + e.Err.Error()
}

func (e *PreviewUnavailable) Unwrap() error { return e.Err }

// PreviewEntry is one bonded validator of the preview. Every amount, share and
// WARP is the chain's own string, unchanged.
type PreviewEntry struct {
	Operator     string
	VP           string
	VPShare      string
	StockShare   string
	Warp         string
	WarpMeasured bool
	Eligible     bool
	Reason       string
}

// Preview is the answer of Query/VotingPowerPreview.
type Preview struct {
	Height     int64
	Epoch      uint64
	Threshold  string // params.warp_threshold, 0-100
	Initial    string // params.warp_initial
	Cap        string // params.validator_cap, nscn
	TotalVP    string
	Validators []PreviewEntry
}

const previewPath = "/saicon/chain/rewards/v1/voting_power_preview"

// The gRPC status codes the gateway writes into an error body.
const (
	grpcNotFound           = 5
	grpcFailedPrecondition = 9
	grpcUnimplemented      = 12
)

// grpcGatewayNotFound is the message a gateway writes for a path it has no
// route for, when it answers that with NotFound rather than Unimplemented. The
// chain's own NotFound carries the keeper's text instead.
const grpcGatewayNotFound = "Not Found"

// previewBodyLimit bounds the answer read. A preview of a few thousand
// validators is well under it; an answer cut short fails to decode and is
// recorded as an error, not trusted.
const previewBodyLimit = 16 << 20

var (
	reUint = regexp.MustCompile(`^\d+$`)
	reDec  = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

// previewBody is the gateway's JSON. int64 and uint64 fields arrive as strings.
type previewBody struct {
	Height string `json:"height"`
	Epoch  string `json:"epoch"`
	Params struct {
		WarpThreshold string `json:"warp_threshold"`
		WarpInitial   string `json:"warp_initial"`
		ValidatorCap  string `json:"validator_cap"`
	} `json:"params"`
	Validators []struct {
		OperatorAddress string `json:"operator_address"`
		VP              string `json:"vp"`
		VPShare         string `json:"vp_share"`
		StockShare      string `json:"stock_share"`
		Warp            string `json:"warp"`
		WarpMeasured    bool   `json:"warp_measured"`
		Eligible        bool   `json:"eligible"`
		Reason          string `json:"reason"`
	} `json:"validators"`
	TotalVP string `json:"total_vp"`
}

// preview checks the answer's shape and returns it. Nothing is converted: an
// amount must be an integer string and a share or WARP a decimal string, and
// they are kept as the chain wrote them.
func (b previewBody) preview() (*Preview, error) {
	h, err := strconv.ParseInt(b.Height, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("height %q: %w", b.Height, err)
	}
	ep, err := strconv.ParseUint(b.Epoch, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("epoch %q: %w", b.Epoch, err)
	}
	for name, v := range map[string]string{"params.warp_threshold": b.Params.WarpThreshold, "params.warp_initial": b.Params.WarpInitial} {
		if !reDec.MatchString(v) {
			return nil, fmt.Errorf("%s %q is not a decimal", name, v)
		}
	}
	for name, v := range map[string]string{"params.validator_cap": b.Params.ValidatorCap, "total_vp": b.TotalVP} {
		if !reUint.MatchString(v) {
			return nil, fmt.Errorf("%s %q is not an integer", name, v)
		}
	}
	p := &Preview{
		Height: h, Epoch: ep,
		Threshold: b.Params.WarpThreshold, Initial: b.Params.WarpInitial, Cap: b.Params.ValidatorCap,
		TotalVP: b.TotalVP, Validators: make([]PreviewEntry, 0, len(b.Validators)),
	}
	for _, v := range b.Validators {
		switch {
		case v.OperatorAddress == "":
			return nil, errors.New("a validator without operator_address")
		case !reUint.MatchString(v.VP):
			return nil, fmt.Errorf("validator %s: vp %q is not an integer", v.OperatorAddress, v.VP)
		case !reDec.MatchString(v.VPShare):
			return nil, fmt.Errorf("validator %s: vp_share %q is not a decimal", v.OperatorAddress, v.VPShare)
		case !reDec.MatchString(v.StockShare):
			return nil, fmt.Errorf("validator %s: stock_share %q is not a decimal", v.OperatorAddress, v.StockShare)
		case !reDec.MatchString(v.Warp):
			return nil, fmt.Errorf("validator %s: warp %q is not a decimal", v.OperatorAddress, v.Warp)
		}
		p.Validators = append(p.Validators, PreviewEntry{
			Operator: v.OperatorAddress, VP: v.VP, VPShare: v.VPShare, StockShare: v.StockShare,
			Warp: v.Warp, WarpMeasured: v.WarpMeasured, Eligible: v.Eligible, Reason: v.Reason,
		})
	}
	return p, nil
}

func unavailable(code string, err error) *PreviewUnavailable {
	return &PreviewUnavailable{Code: code, Err: err}
}

// VotingPowerPreviewAt reads the preview pinned to height.
//
// It returns the preview; or a *PreviewUnavailable when the node's answer says
// for certain that there is none at the height; or any other error, which is
// transient: the caller reads the height again later.
func (r *REST) VotingPowerPreviewAt(ctx context.Context, height int64) (*Preview, error) {
	url := r.base + previewPath
	want := strconv.FormatInt(height, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(heightRequestHeader, want)
	resp, err := r.c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voting-power preview at %d: %w", height, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		return nil, classifyPreview(url, height, resp.StatusCode, b)
	}
	if err := checkEcho(resp, url, want); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, previewBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read %s at height %d: %w", url, height, err)
	}
	var body previewBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode %s at height %d: %w", url, height, err)
	}
	p, err := body.preview()
	if err != nil {
		return nil, fmt.Errorf("%s at height %d: %w", url, height, err)
	}
	if p.Height != height {
		return nil, fmt.Errorf("%s answered height %d in the body, asked for %d", url, p.Height, height)
	}
	return p, nil
}

// classifyPreview names a non-200 answer. Pruned and future heights are told
// apart by the body text, as for every pinned read (both are HTTP 500). The
// other codes need the gRPC code in the gateway's JSON body, except a 501,
// which is the gateway's own answer for a method it does not have. Anything
// else, a 404 with no gRPC code among it (a proxy's page, not the node's
// answer), is transient.
func classifyPreview(url string, height int64, status int, body []byte) error {
	text := string(body)
	se := &statusError{url: url, status: status, body: text}
	cls := classify(text)
	switch {
	case errors.Is(cls, ErrHeightUnavailable):
		return unavailable(PreviewPruned, fmt.Errorf("HTTP %d from %s at height %d: %w", status, url, height, cls))
	case errors.Is(cls, ErrHeightAhead):
		return fmt.Errorf("HTTP %d from %s at height %d: %w", status, url, height, cls)
	}
	var g struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	grpc := json.Unmarshal(body, &g) == nil && g.Code != nil
	switch {
	case status == http.StatusNotImplemented, grpc && *g.Code == grpcUnimplemented:
		return unavailable(PreviewRouteMissing, se)
	case grpc && *g.Code == grpcFailedPrecondition:
		return unavailable(PreviewParamsInvalid, se)
	case grpc && *g.Code == grpcNotFound && g.Message == grpcGatewayNotFound:
		// The gateway's answer for a path with no route: about the node, not the chain.
		return unavailable(PreviewRouteMissing, se)
	case grpc && *g.Code == grpcNotFound:
		return unavailable(PreviewCapsMissing, se)
	}
	return se
}
