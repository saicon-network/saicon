package chain_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
)

// getJSON's 404 handling must not trust the status code alone: an operator
// can point REST base URL at a public endpoint behind a proxy that answers
// unmatched routes with its own generic 404, and that must not be read as
// "no record" — it would silently turn every query into NULL. Only a body
// that actually carries the chain's gRPC-gateway NotFound shape
// ({"code":5,...}) earns that reading. Exercised through REST.Compliance,
// which is the thinnest caller of getJSON's 404 branch.
func TestGetJSON404Handling(t *testing.T) {
	t.Run("grpc NotFound body resolves to a nil record, no error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":5,"message":"rpc error: code = NotFound desc = no compliance record","details":[]}`))
		}))
		defer srv.Close()

		r := chain.NewREST(srv.URL)
		v, err := r.Compliance(context.Background(), "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd")
		require.NoError(t, err)
		require.Nil(t, v, "a chain-shaped NotFound body must become a nil record, not an error")
	})

	t.Run("non-chain 404 body is a real error naming the URL", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<html><body><h1>404 Not Found</h1><p>nginx</p></body></html>`))
		}))
		defer srv.Close()

		r := chain.NewREST(srv.URL)
		v, err := r.Compliance(context.Background(), "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd")
		require.Error(t, err)
		require.Nil(t, v)
		require.Contains(t, err.Error(), srv.URL,
			"an unrecognized 404 must say which URL it came from, not be swallowed as a NULL")
	})

	t.Run("200 with a valid body decodes normally", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"state":{"compliant":true}}`))
		}))
		defer srv.Close()

		r := chain.NewREST(srv.URL)
		v, err := r.Compliance(context.Background(), "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd")
		require.NoError(t, err)
		require.NotNil(t, v)
		require.True(t, *v)
	})
}
