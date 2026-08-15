package mcpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

const (
	untrustedPeer  = "203.0.113.44:9000"
	secondForwards = "198.51.100.8"
	oauthTokenPath = "/oauth/token"
)

// countingHandler stands in for the authorization server and counts how many
// requests reached it.
type countingHandler struct{ calls int }

func (c *countingHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	c.calls++
	w.WriteHeader(http.StatusOK)
}

// addressOnlyConfig limits by address with a burst of two and leaves the client
// budget wide, so a test observes address keying alone.
func addressOnlyConfig() ratelimit.HTTPGateConfig {
	return ratelimit.HTTPGateConfig{
		PerAddress: ratelimit.GateConfig{PerMinute: 60, Burst: 2, MaxKeys: 16},
		PerClient:  ratelimit.GateConfig{PerMinute: 600, Burst: 100, MaxKeys: 16},
	}
}

// proxiedTransport trusts proxyCIDR, which is what makes X-Forwarded-For mean
// anything at all.
func proxiedTransport(t *testing.T) *mcpserver.HTTPTransport {
	t.Helper()

	opts := testHTTPOptions(newFakeAuthorizer(t))
	opts.TrustedProxyCIDRs = []string{proxyCIDR}
	return newTestTransport(t, opts)
}

// guard builds the authorization rate-limit middleware around an upstream.
func guard(t *testing.T, transport *mcpserver.HTTPTransport, upstream http.Handler) http.Handler {
	t.Helper()

	middleware, err := transport.AuthorizationRateLimit(addressOnlyConfig())
	if err != nil {
		t.Fatalf("AuthorizationRateLimit returned error: %v", err)
	}
	return middleware(upstream)
}

// forwardedRequest is a token POST arriving through the trusted proxy on behalf
// of the named client address.
func forwardedRequest(peer, forwarded string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, oauthTokenPath, http.NoBody)
	req.RemoteAddr = peer
	req.Header.Set("X-Forwarded-For", forwarded)
	return req
}

// TestAuthorizationRateLimitSeparatesClientsBehindATrustedProxy shows the
// forwarded address is honored where it is evidence: distinct clients arriving
// through the configured proxy get distinct budgets.
func TestAuthorizationRateLimitSeparatesClientsBehindATrustedProxy(t *testing.T) {
	// Arrange
	upstream := &countingHandler{}
	guarded := guard(t, proxiedTransport(t), upstream)

	// Act
	for range 3 {
		guarded.ServeHTTP(httptest.NewRecorder(), forwardedRequest(proxyPeer, forwardedClient))
	}
	fresh := httptest.NewRecorder()
	guarded.ServeHTTP(fresh, forwardedRequest(proxyPeer, secondForwards))

	// Assert
	if fresh.Code != http.StatusOK {
		t.Fatalf("the second forwarded client got status %d, want 200", fresh.Code)
	}
	if upstream.calls != 3 {
		t.Fatalf("the authorization server was reached %d times, want 3", upstream.calls)
	}
}

// TestAuthorizationRateLimitIgnoresAForgedForwardedHeader is the security case:
// a caller that is not a trusted proxy cannot mint a fresh budget per request by
// setting a header.
func TestAuthorizationRateLimitIgnoresAForgedForwardedHeader(t *testing.T) {
	// Arrange
	upstream := &countingHandler{}
	guarded := guard(t, proxiedTransport(t), upstream)

	// Act
	var last *httptest.ResponseRecorder
	for i := range 4 {
		last = httptest.NewRecorder()
		guarded.ServeHTTP(last,
			forwardedRequest(untrustedPeer, forwardedClient+string(rune('0'+i))))
	}

	// Assert
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; a forged header bypassed the limit", last.Code)
	}
	if upstream.calls != 2 {
		t.Fatalf("the authorization server was reached %d times, want 2", upstream.calls)
	}
}

// TestAuthorizationRateLimitRefusesBeforeTheAuthorizationServer states the
// requirement plainly: a limited request must not reach the upstream at all.
func TestAuthorizationRateLimitRefusesBeforeTheAuthorizationServer(t *testing.T) {
	// Arrange
	upstream := &countingHandler{}
	guarded := guard(t, proxiedTransport(t), upstream)

	// Act
	var last *httptest.ResponseRecorder
	for range 5 {
		last = httptest.NewRecorder()
		guarded.ServeHTTP(last, forwardedRequest(untrustedPeer, forwardedClient))
	}

	// Assert
	if upstream.calls != 2 {
		t.Fatalf("the authorization server was reached %d times, want 2", upstream.calls)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Fatalf("the refusal carried no Retry-After header")
	}
}

func TestAuthorizationRateLimitRejectsAnInvalidBudget(t *testing.T) {
	// Arrange
	cfg := ratelimit.HTTPGateConfig{
		PerAddress: ratelimit.GateConfig{PerMinute: 0, Burst: 1, MaxKeys: 1},
		PerClient:  ratelimit.GateConfig{PerMinute: 60, Burst: 1, MaxKeys: 1},
	}

	// Act
	_, err := proxiedTransport(t).AuthorizationRateLimit(cfg)

	// Assert
	if err == nil {
		t.Fatalf("AuthorizationRateLimit accepted a zero rate")
	}
}
