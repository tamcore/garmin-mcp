package auth

import (
	"net/url"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// A base that cannot be parsed contributes no origin, and a zero Hosts therefore
// permits nothing at all: the guard fails closed.
func TestOriginAllowlistFailsClosed(t *testing.T) {
	zero := newOriginAllowlist(protocol.Hosts{})
	if len(zero.origins) != 0 {
		t.Fatalf("a zero Hosts yielded %d origins, want none", len(zero.origins))
	}

	hosts, err := protocol.NewHosts(protocol.DomainGlobal)
	if err != nil {
		t.Fatalf("NewHosts: %v", err)
	}
	broken := newOriginAllowlist(hosts.WithOverrides(protocol.Overrides{
		SSO: "https://sso.example.invalid/%zz",
	}))

	if _, ok := broken.origins["https://sso.example.invalid"]; ok {
		t.Error("an unparsable override contributed an origin")
	}
	if _, ok := broken.origins["https://connectapi.garmin.com"]; !ok {
		t.Error("a sound base was dropped because a sibling override was unparsable")
	}
}

// The comparison is case-insensitive on scheme and host, and a port is part of the
// host, so an explicit port never matches a base that has none.
func TestOriginAllowlistNormalizesCaseAndKeepsThePort(t *testing.T) {
	hosts, err := protocol.NewHosts(protocol.DomainGlobal)
	if err != nil {
		t.Fatalf("NewHosts: %v", err)
	}
	allowed := newOriginAllowlist(hosts)

	cases := map[string]bool{
		"https://CONNECTAPI.Garmin.COM/x":     true,
		"HTTPS://connectapi.garmin.com/x":     true,
		"https://connectapi.garmin.com:443/x": false,
		"https://connectapi.garmin.com":       true,
		"/relative/path":                      false,
		"mailto:someone@example.invalid":      false,
	}

	for raw, want := range cases {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if got := allowed.permits(parsed); got != want {
			t.Errorf("permits(%q) = %v, want %v", raw, got, want)
		}
	}
}
