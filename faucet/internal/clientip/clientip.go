// Package clientip decides which IP a request counts against.
package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver extracts the client IP. X-Forwarded-For is honoured only when the
// TCP peer is inside one of the trusted proxy prefixes; anyone else could
// otherwise pick their own IP and dodge the per-IP limit.
type Resolver struct{ trusted []netip.Prefix }

// ParseCIDRs parses a comma-separated list of CIDRs (blank entries skipped).
func ParseCIDRs(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		pf, err := netip.ParsePrefix(p)
		if err != nil {
			return nil, err
		}
		out = append(out, pf)
	}
	return out, nil
}

func New(trusted []netip.Prefix) *Resolver { return &Resolver{trusted: trusted} }

// Key returns the limiter key for the request: the client IP, with IPv6
// collapsed to its /48 (the allocation of one site or one tunnel-broker account;
// per-/64 or /56 keys would let a single actor rotate through hundreds of buckets). ok is
// false when no IP could be determined.
func (r *Resolver) Key(req *http.Request) (string, bool) {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "", false
	}
	peer = peer.Unmap()
	ip := peer
	if r.isTrusted(peer) {
		// First hop only: the address Caddy saw as the connecting client.
		xff := req.Header.Get("X-Forwarded-For")
		if first, _, _ := strings.Cut(xff, ","); strings.TrimSpace(first) != "" {
			if a, err := netip.ParseAddr(strings.TrimSpace(first)); err == nil {
				ip = a.Unmap()
			}
		}
	}
	if ip.Is6() {
		p, _ := ip.Prefix(48)
		return p.String(), true
	}
	return ip.String(), true
}

func (r *Resolver) isTrusted(a netip.Addr) bool {
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
