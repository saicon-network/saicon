package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ErrHeightUnavailable is wrapped by every read at a height the node no longer holds (pruned).
var ErrHeightUnavailable = errors.New("height not available on this node")

// ErrHeightAhead is wrapped by a read at a height the node has not reached yet.
var ErrHeightAhead = errors.New("height not reached by this node")

// EntityLayerUpgrade is the upgrade that added x/entityregistry (chain/app/upgrades.go UpgradeV0100).
const EntityLayerUpgrade = "v0.10.0"

const (
	// heightRequestHeader pins a gateway read to a block.
	heightRequestHeader = "x-cosmos-block-height"
	// heightEchoHeader is the plain response header the gateway answers a 200 with.
	heightEchoHeader = "X-Cosmos-Block-Height"
	// heightEchoGRPCHeader is the same fact as gRPC metadata; a 404 carries only this one.
	heightEchoGRPCHeader = "Grpc-Metadata-X-Cosmos-Block-Height"

	// errorBodyLimit bounds how much of a failed answer is read for the message.
	errorBodyLimit = 2048
)

// Texts the SDK and CometBFT use for a height outside what the node holds. The REST
// gateway answers both of its cases with HTTP 500 and no height header, so the body is
// the only thing that tells them apart from a real fault.
const (
	textPrunedREST   = "failed to load state at height"
	textPrunedRPC    = "is not available, lowest height is"
	textFutureREST   = "invalid height: cannot query with height in the future"
	textFutureRPC    = "must be less than or equal to the current blockchain height"
	textModuleAbsent = "x/upgrade: QueryModuleVersions module " + entityLayerModule + " not found"
)

// entityLayerModule is the module whose presence marks a chain with an entity layer.
const entityLayerModule = "entityregistry"

// classify turns the text of a failed answer into one of the two height errors, wrapping
// the sentinel around the original text; any other text yields a plain error.
func classify(body string) error {
	switch {
	case strings.Contains(body, textPrunedREST), strings.Contains(body, textPrunedRPC):
		return fmt.Errorf("%w: %s", ErrHeightUnavailable, body)
	case strings.Contains(body, textFutureREST), strings.Contains(body, textFutureRPC):
		return fmt.Errorf("%w: %s", ErrHeightAhead, body)
	}
	return errors.New(body)
}

// statusError is a non-200 answer that is not a recognised "no record".
type statusError struct {
	url    string
	status int
	body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("HTTP %d from %s: %s", e.status, e.url, e.body)
}

// getJSONAt reads url pinned to height and decodes the answer into out.
//
// A 200 must echo the pinned height in X-Cosmos-Block-Height: a node or proxy that ignored
// the request header would otherwise hand back current state labelled as history. A gRPC
// NotFound (404, {"code":5}) is "no record" and is returned as noRecordError; the gateway
// echoes the height on it only as Grpc-Metadata-X-Cosmos-Block-Height, which is not
// required, but must agree when present. Every other status is classified by its body,
// never by the status code: pruned and future heights are both HTTP 500.
func getJSONAt(ctx context.Context, c *http.Client, url string, height int64, out any) error {
	err := fetchJSON(ctx, c, url, &height, out)
	var se *statusError
	if errors.As(err, &se) {
		cls := classify(se.body)
		if errors.Is(cls, ErrHeightUnavailable) || errors.Is(cls, ErrHeightAhead) {
			return fmt.Errorf("HTTP %d from %s at height %d: %w", se.status, url, height, cls)
		}
	}
	return err
}

// fetchJSON is the shared GET: pin is nil for a read of the latest state.
func fetchJSON(ctx context.Context, c *http.Client, url string, pin *int64, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	want := ""
	if pin != nil {
		want = strconv.FormatInt(*pin, 10)
		req.Header.Set(heightRequestHeader, want)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		if pin != nil {
			got := strings.TrimSpace(resp.Header.Get(heightEchoHeader))
			if got != want {
				return fmt.Errorf("%s answered for height %q, asked for %s (header %s)", url, got, want, heightEchoHeader)
			}
			if g := strings.TrimSpace(resp.Header.Get(heightEchoGRPCHeader)); g != "" && g != want {
				return fmt.Errorf("%s answered for height %q, asked for %s (header %s)", url, g, want, heightEchoGRPCHeader)
			}
		}
		return json.NewDecoder(resp.Body).Decode(out)
	case http.StatusNotFound:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		var grpcErr struct {
			Code int `json:"code"`
		}
		if json.Unmarshal(b, &grpcErr) != nil || grpcErr.Code != 5 {
			return fmt.Errorf("HTTP 404 from %s: body not a chain \"no record\" response: %s", url, b)
		}
		if g := strings.TrimSpace(resp.Header.Get(heightEchoGRPCHeader)); pin != nil && g != "" && g != want {
			return fmt.Errorf("%s answered no-record for height %q, asked for %s (header %s)", url, g, want, heightEchoGRPCHeader)
		}
		return noRecordError{url: url}
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		return &statusError{url: url, status: resp.StatusCode, body: string(b)}
	}
}

// EntityLayerFrom reports the first height at which the entity stores exist. present is
// false for a chain without x/entityregistry (the module-versions query answers HTTP 500
// with exactly one text for that, or an empty list); every other failure is an error, so a
// transient fault is never read as an absent module. The answer is read at the tip.
//
// An applied plan at height 0 means the chain started from a genesis that already had
// the module, so its stores exist from height 1; otherwise they exist from the upgrade.
func (r *REST) EntityLayerFrom(ctx context.Context) (from int64, present bool, err error) {
	var versions struct {
		ModuleVersions *[]struct {
			Name string `json:"name"`
		} `json:"module_versions"`
	}
	url := r.base + "/cosmos/upgrade/v1beta1/module_versions?module_name=" + entityLayerModule
	if err := fetchJSON(ctx, r.c, url, nil, &versions); err != nil {
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusInternalServerError && strings.Contains(se.body, textModuleAbsent) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("entity layer: module versions: %w", err)
	}
	if versions.ModuleVersions == nil {
		return 0, false, fmt.Errorf("entity layer: module versions answered without a module_versions list")
	}
	if len(*versions.ModuleVersions) == 0 {
		return 0, false, nil
	}
	found := false
	for _, m := range *versions.ModuleVersions {
		found = found || m.Name == entityLayerModule
	}
	if !found {
		return 0, false, fmt.Errorf("entity layer: module versions answered without %s", entityLayerModule)
	}

	var plan struct {
		Height string `json:"height"`
	}
	url = r.base + "/cosmos/upgrade/v1beta1/applied_plan/" + EntityLayerUpgrade
	if err := fetchJSON(ctx, r.c, url, nil, &plan); err != nil {
		return 0, false, fmt.Errorf("entity layer: applied plan: %w", err)
	}
	h, err := strconv.ParseInt(plan.Height, 10, 64)
	if err != nil || h < 0 {
		return 0, false, fmt.Errorf("entity layer: applied plan height %q", plan.Height)
	}
	if h == 0 {
		h = 1
	}
	return h, true, nil
}
