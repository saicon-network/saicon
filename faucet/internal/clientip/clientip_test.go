package clientip

import (
	"net/http"
	"testing"
)

func req(remote, xff string) *http.Request {
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestKey(t *testing.T) {
	trusted, err := ParseCIDRs("172.16.0.0/12, 127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	res := New(trusted)
	cases := []struct {
		name, remote, xff, want string
	}{
		{"untrusted peer ignores XFF", "8.8.8.8:1234", "1.2.3.4", "8.8.8.8"},
		{"trusted peer uses first hop", "172.18.0.5:1234", "1.2.3.4, 10.0.0.1", "1.2.3.4"},
		{"trusted peer no XFF", "172.18.0.5:1234", "", "172.18.0.5"},
		{"trusted peer garbage XFF", "172.18.0.5:1234", "nonsense", "172.18.0.5"},
		{"loopback trusted", "127.0.0.1:9", "5.6.7.8", "5.6.7.8"},
		{"ipv4-mapped peer", "[::ffff:172.18.0.5]:1", "1.2.3.4", "1.2.3.4"},
		{"ipv6 client collapsed to /48", "172.18.0.5:1", "2001:db8:1:2:3:4:5:6", "2001:db8:1::/48"},
		{"no trusted list at all", "172.18.0.5:1", "1.2.3.4", "172.18.0.5"},
	}
	for _, c := range cases {
		r := res
		if c.name == "no trusted list at all" {
			r = New(nil)
		}
		got, ok := r.Key(req(c.remote, c.xff))
		if !ok || got != c.want {
			t.Errorf("%s: got %q ok=%v, want %q", c.name, got, ok, c.want)
		}
	}
	if _, ok := res.Key(req("garbage", "")); ok {
		t.Error("garbage peer accepted")
	}
	if _, err := ParseCIDRs("nope"); err == nil {
		t.Error("bad CIDR accepted")
	}
}
