package chain

import (
	"net/http"
	"sync/atomic"
	"time"
)

// countingTransport counts every HTTP request sent through it, whatever the
// answer. The concentration pass budgets its work by this number, so a page of
// a paged list counts like any other request.
type countingTransport struct {
	n    atomic.Uint64
	base http.RoundTripper
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.n.Add(1)
	return t.base.RoundTrip(req)
}

// newCountingClient is the HTTP client both chain clients use.
func newCountingClient() (*http.Client, *countingTransport) {
	ct := &countingTransport{base: http.DefaultTransport}
	return &http.Client{Timeout: 20 * time.Second, Transport: ct}, ct
}
