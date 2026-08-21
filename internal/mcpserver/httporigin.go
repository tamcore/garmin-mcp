package mcpserver

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
)

// corsMaxAge is how long a browser may cache a preflight answer. Ten minutes is
// long enough to keep preflights off the hot path and short enough that removing
// an origin from the allowlist takes effect while an operator is still watching.
const corsMaxAge = "600"

// allowedRequestHeaders are the request headers a browser client may send. The
// list is closed on purpose: a header this transport does not read is a header
// no browser needs permission to send.
const allowedRequestHeaders = "Authorization, Content-Type, Accept, Mcp-Session-Id, " +
	"Mcp-Protocol-Version, Last-Event-ID"

// exposedResponseHeaders are the response headers a browser client may read.
const exposedResponseHeaders = "Mcp-Session-Id, WWW-Authenticate"

// An originGuard enforces the Origin allowlist and is the whole of this server's
// CORS policy.
//
// Two rules, both from the security brief:
//
//   - A request that carries Origin must match the allowlist. Origin is set by
//     the browser and cannot be forged by page script, so it is the one
//     cross-site signal worth checking.
//   - A request without Origin is fine. A standards-compliant non-browser client
//     does not send one, and demanding it would break every CLI and SDK.
//
// CORS defaults to deny: with no configured origin nothing is allowed, and no
// response ever carries an Access-Control-Allow-Origin header.
type originGuard struct {
	allowed map[string]struct{}
}

// newOriginGuard validates and freezes the allowlist.
//
// Each entry must be a bare origin — scheme and host, optionally a port, nothing
// else. A wildcard is not accepted in any form: "*" with credentials is refused
// by every browser anyway, and a suffix match is how an allowlist quietly starts
// permitting evil-example.test.
func newOriginGuard(origins []string) (originGuard, error) {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		canonical, err := canonicalOrigin(origin)
		if err != nil {
			return originGuard{}, err
		}
		allowed[canonical] = struct{}{}
	}
	return originGuard{allowed: allowed}, nil
}

// canonicalOrigin normalizes one origin.
func canonicalOrigin(origin string) (string, error) {
	trimmed := strings.TrimSpace(origin)
	if trimmed == "" || trimmed == "*" || trimmed == "null" {
		return "", fmt.Errorf("origin %q is not a concrete origin: %w",
			origin, ErrInvalidHTTPOptions)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("origin %q is not a URL: %w", origin, ErrInvalidHTTPOptions)
	}
	switch {
	case parsed.Scheme != schemeHTTP && parsed.Scheme != schemeHTTPS:
		return "", fmt.Errorf("origin %q has scheme %q, want http or https: %w",
			origin, parsed.Scheme, ErrInvalidHTTPOptions)
	case parsed.Host == "":
		return "", fmt.Errorf("origin %q names no host: %w", origin, ErrInvalidHTTPOptions)
	case parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil:
		return "", fmt.Errorf("origin %q must be a bare scheme and host: %w",
			origin, ErrInvalidHTTPOptions)
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host), nil
}

// permits reports whether an Origin header value is allowed. The empty string is
// the absent header, which is permitted.
func (g originGuard) permits(origin string) bool {
	if origin == "" {
		return true
	}
	canonical, err := canonicalOrigin(origin)
	if err != nil {
		return false
	}
	_, ok := g.allowed[canonical]
	return ok
}

// wrap applies the guard, answers preflights, and grants CORS to an allowed
// origin.
//
// A preflight is answered before anything else and is never authenticated: a
// browser sends it without credentials by definition, so requiring one would
// make every browser client fail its first request. It creates no session and
// reaches no MCP code.
func (g originGuard) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !g.permits(origin) {
			http.Error(w, "origin is not allowed", http.StatusForbidden)
			return
		}
		if origin != "" {
			g.grant(w, origin)
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// grant writes the CORS headers for an allowed origin.
//
// The origin is echoed rather than answered with "*", because the exact origin
// is the only form that works with credentials and the only one that keeps the
// allowlist meaningful. Vary is mandatory whenever the answer depends on Origin,
// or a shared cache serves one origin's response to another.
func (g originGuard) grant(w http.ResponseWriter, origin string) {
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", origin)
	header.Add("Vary", "Origin")
	header.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	header.Set("Access-Control-Allow-Headers", allowedRequestHeaders)
	header.Set("Access-Control-Expose-Headers", exposedResponseHeaders)
	header.Set("Access-Control-Max-Age", corsMaxAge)
}

// A forwardedTrust decides whether a request's forwarded headers mean anything.
//
// X-Forwarded-For is a claim made by whoever is talking to this process. It is
// worth something only when that peer is a reverse proxy the operator named, and
// worth nothing otherwise — which is the default, because the zero value trusts
// nobody.
//
// The forwarded address is NOT merely a log label: it keys the per-address budget
// of the rate limiter in front of the token, revocation and metadata endpoints,
// which is the only limit on this server's unauthenticated OAuth surface. That is
// why the walk below goes right to left. The issuer, the resource, and every URL
// this server publishes still come from configuration, never from a header.
type forwardedTrust struct {
	proxies []netip.Prefix
}

// newForwardedTrust parses the configured proxy CIDRs.
func newForwardedTrust(cidrs []string) (forwardedTrust, error) {
	proxies := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return forwardedTrust{}, fmt.Errorf("trusted proxy %q is not a CIDR: %w",
				cidr, ErrInvalidHTTPOptions)
		}
		proxies = append(proxies, prefix)
	}
	return forwardedTrust{proxies: proxies}, nil
}

// clientIP returns the address to attribute the request to.
//
// It is the peer address unless the peer is a trusted proxy, in which case it is
// the RIGHT-MOST X-Forwarded-For entry that is not itself a trusted proxy. A
// malformed entry is discarded rather than repaired: an unparseable address is not
// evidence of anything.
//
// The direction is the security property, not a style choice. A proxy APPENDS to
// this header and preserves whatever the client sent, so with ingress-nginx's
// use-forwarded-headers, HAProxy, or most cloud load balancers, a caller sending
// "X-Forwarded-For: 1.2.3.4" produces "1.2.3.4, <real client>". Reading the
// client-most entry therefore returns a string the caller chose. Since this value
// keys the per-address rate-limit budget on the unauthenticated OAuth endpoints,
// that let a caller mint a fresh budget per request by rotating the header, which
// is exactly the limit that is supposed to bound credential stuffing.
//
// Walking from the right, every entry a trusted proxy appended is skipped, and the
// first entry outside the trusted set is the nearest address this deployment has
// any reason to believe. Anything further left was supplied by something upstream
// of the trust boundary and is ignored.
func (t forwardedTrust) clientIP(r *http.Request) string {
	peer := peerIP(r.RemoteAddr)
	if !t.trusts(peer) {
		return peer
	}

	entries := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for _, entry := range slices.Backward(entries) {
		candidate, err := netip.ParseAddr(strings.TrimSpace(entry))
		if err != nil {
			// A malformed entry is not evidence, and it is also not a reason to
			// keep walking left past it into caller-controlled territory.
			return peer
		}
		if !t.trusts(candidate.String()) {
			return candidate.String()
		}
	}
	// Every entry was a trusted proxy, so the header names no client.
	return peer
}

// trusts reports whether an address is inside a configured proxy range.
func (t forwardedTrust) trusts(address string) bool {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	for _, prefix := range t.proxies {
		if prefix.Contains(parsed) {
			return true
		}
	}
	return false
}

// peerIP strips the port from a RemoteAddr, tolerating one that has none.
func peerIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
