package ratelimit_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

// Synthetic values shared by the HTTP gate tests.
const (
	peerAddress    = "203.0.113.9"
	otherAddress   = "203.0.113.10"
	forgedAddress  = "198.51.100.200"
	clientOne      = "client-one"
	clientTwo      = "client-two"
	clientSecret   = "a-client-secret"
	formType       = "application/x-www-form-urlencoded"
	tokenPath      = "/oauth/token"
	rateLimitError = "slow_down"
)

// spyHandler records whether the guarded handler was reached and echoes the body
// it was given, so a test can prove both reachability and body preservation.
type spyHandler struct {
	calls int
	body  string
}

func (s *spyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.calls++
	read, _ := io.ReadAll(r.Body)
	s.body = string(read)
	w.WriteHeader(http.StatusOK)
}

// tinyGateConfig gives both budgets a burst of two, so the third call is refused.
func tinyGateConfig() ratelimit.HTTPGateConfig {
	small := ratelimit.GateConfig{PerMinute: 60, Burst: 2, MaxKeys: 8}
	return ratelimit.HTTPGateConfig{PerAddress: small, PerClient: small}
}

// wideAddressConfig keeps the address budget out of the way so a test can watch
// the client budget on its own.
func wideAddressConfig() ratelimit.HTTPGateConfig {
	return ratelimit.HTTPGateConfig{
		PerAddress: ratelimit.GateConfig{PerMinute: 600, Burst: 100, MaxKeys: 8},
		PerClient:  ratelimit.GateConfig{PerMinute: 60, Burst: 2, MaxKeys: 8},
	}
}

// fixedAddress is an address source that ignores the request entirely, which is
// how a test states that no header may influence the key.
func fixedAddress(address string) ratelimit.AddressFunc {
	return func(*http.Request) string { return address }
}

func newTestHTTPGate(t *testing.T, cfg ratelimit.HTTPGateConfig) *ratelimit.HTTPGate {
	t.Helper()

	gate, err := ratelimit.NewHTTPGate(cfg, fixedAddress(peerAddress), nil)
	if err != nil {
		t.Fatalf("NewHTTPGate returned error: %v", err)
	}
	return gate
}

// tokenPOST builds a client_secret_post token request for one client.
func tokenPOST(clientID string) *http.Request {
	body := "grant_type=refresh_token&client_id=" + clientID + "&refresh_token=r"
	req := httptest.NewRequest(http.MethodPost, tokenPath, strings.NewReader(body))
	req.Header.Set("Content-Type", formType)
	return req
}

func TestNewHTTPGateRefusesAMissingAddressSource(t *testing.T) {
	// Act
	_, err := ratelimit.NewHTTPGate(tinyGateConfig(), nil, nil)

	// Assert
	if !errors.Is(err, ratelimit.ErrMissingAddressSource) {
		t.Fatalf("NewHTTPGate error = %v, want ErrMissingAddressSource", err)
	}
}

func TestHTTPGateRefusesTheCallerOnceTheAddressBudgetIsExhausted(t *testing.T) {
	// Arrange
	spy := &spyHandler{}
	guarded := newTestHTTPGate(t, tinyGateConfig()).Middleware()(spy)

	// Act
	var last *httptest.ResponseRecorder
	for range 3 {
		last = httptest.NewRecorder()
		guarded.ServeHTTP(last, httptest.NewRequest(http.MethodGet, tokenPath, nil))
	}

	// Assert
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", last.Code)
	}
	if spy.calls != 2 {
		t.Fatalf("the guarded handler was reached %d times, want 2", spy.calls)
	}
	if retry := last.Header().Get("Retry-After"); retry == "" {
		t.Fatalf("a refusal carried no Retry-After header")
	}
}

func TestHTTPGateBudgetsClientIdentifiersIndependently(t *testing.T) {
	// Arrange
	spy := &spyHandler{}
	guarded := newTestHTTPGate(t, wideAddressConfig()).Middleware()(spy)

	// Act
	for range 3 {
		guarded.ServeHTTP(httptest.NewRecorder(), tokenPOST(clientOne))
	}
	fresh := httptest.NewRecorder()
	guarded.ServeHTTP(fresh, tokenPOST(clientTwo))

	// Assert
	if fresh.Code != http.StatusOK {
		t.Fatalf("the second client got status %d, want 200", fresh.Code)
	}
	if spy.calls != 3 {
		t.Fatalf("the guarded handler was reached %d times, want 3", spy.calls)
	}
}

func TestHTTPGateReadsTheClientIdentifierFromBasicAuthentication(t *testing.T) {
	// Arrange
	spy := &spyHandler{}
	guarded := newTestHTTPGate(t, wideAddressConfig()).Middleware()(spy)

	// Act
	var last *httptest.ResponseRecorder
	for range 3 {
		req := httptest.NewRequest(http.MethodPost, tokenPath, strings.NewReader("grant_type=x"))
		req.Header.Set("Content-Type", formType)
		req.SetBasicAuth(clientOne, clientSecret)
		last = httptest.NewRecorder()
		guarded.ServeHTTP(last, req)
	}

	// Assert
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 once the client budget is spent", last.Code)
	}
}

func TestHTTPGateIgnoresForwardedHeadersAndUsesTheInjectedSource(t *testing.T) {
	// Arrange
	spy := &spyHandler{}
	guarded := newTestHTTPGate(t, tinyGateConfig()).Middleware()(spy)

	// Act: every request forges a different X-Forwarded-For.
	var last *httptest.ResponseRecorder
	for i := range 3 {
		req := httptest.NewRequest(http.MethodGet, tokenPath, nil)
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("%s%d", forgedAddress, i))
		req.RemoteAddr = otherAddress + ":40000"
		last = httptest.NewRecorder()
		guarded.ServeHTTP(last, req)
	}

	// Assert
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; a forged header split the address budget", last.Code)
	}
}

func TestHTTPGatePreservesTheRequestBodyForTheGuardedHandler(t *testing.T) {
	// Arrange
	spy := &spyHandler{}
	guarded := newTestHTTPGate(t, tinyGateConfig()).Middleware()(spy)
	body := "grant_type=refresh_token&client_id=" + clientOne + "&refresh_token=r"

	// Act
	req := httptest.NewRequest(http.MethodPost, tokenPath, strings.NewReader(body))
	req.Header.Set("Content-Type", formType)
	guarded.ServeHTTP(httptest.NewRecorder(), req)

	// Assert
	if spy.body != body {
		t.Fatalf("the handler read body %q, want %q", spy.body, body)
	}
}

func TestHTTPGateRefusalIsAWellFormedOAuthErrorWithoutDetail(t *testing.T) {
	// Arrange
	spy := &spyHandler{}
	guarded := newTestHTTPGate(t, tinyGateConfig()).Middleware()(spy)

	// Act
	var last *httptest.ResponseRecorder
	for range 3 {
		last = httptest.NewRecorder()
		guarded.ServeHTTP(last, tokenPOST(clientOne))
	}

	// Assert
	var failure struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(last.Body.Bytes(), &failure); err != nil {
		t.Fatalf("the refusal body is not JSON: %v (%q)", err, last.Body.String())
	}
	if failure.Error != rateLimitError {
		t.Fatalf("error = %q, want %q", failure.Error, rateLimitError)
	}
	if got := last.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	for name, secret := range map[string]string{
		"client identifier": clientOne,
		"client address":    peerAddress,
	} {
		if strings.Contains(last.Body.String(), secret) {
			t.Fatalf("the refusal disclosed the %s: %q", name, last.Body.String())
		}
	}
}

func TestHTTPGateRendersNoClientIdentifierOrAddressUnderAnyVerb(t *testing.T) {
	// Arrange: the alias strips every method, so fmt has to print structurally.
	type strippedGate ratelimit.HTTPGate

	gate := newTestHTTPGate(t, tinyGateConfig())
	guarded := gate.Middleware()(&spyHandler{})
	guarded.ServeHTTP(httptest.NewRecorder(), tokenPOST(clientOne))
	stripped := (*strippedGate)(gate)

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
		t.Run(verb, func(t *testing.T) {
			// Act
			rendered := fmt.Sprintf(verb, stripped) + fmt.Sprintf(verb, *stripped)

			// Assert
			for name, material := range map[string]string{
				"client identifier": clientOne,
				"client address":    peerAddress,
			} {
				if strings.Contains(rendered, material) {
					t.Fatalf("verb %s rendered the %s: %q", verb, name, rendered)
				}
			}
		})
	}
}

func TestNilHTTPGateMiddlewareIsATransparentPassThrough(t *testing.T) {
	// Arrange
	var gate *ratelimit.HTTPGate
	spy := &spyHandler{}

	// Act
	recorder := httptest.NewRecorder()
	gate.Middleware()(spy).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tokenPath, nil))

	// Assert
	if spy.calls != 1 || recorder.Code != http.StatusOK {
		t.Fatalf("a nil gate did not pass the request through (calls %d, status %d)",
			spy.calls, recorder.Code)
	}
}

func TestDefaultHTTPGateConfigIsUsable(t *testing.T) {
	// Act
	_, err := ratelimit.NewHTTPGate(
		ratelimit.DefaultHTTPGateConfig(), fixedAddress(peerAddress), time.Now)

	// Assert
	if err != nil {
		t.Fatalf("the shipped defaults did not build a gate: %v", err)
	}
}
