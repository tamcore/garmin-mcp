package auth_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// stubTransport is an in-memory Doer. It reaches no network: every response is
// produced by the handler the test supplies.
type stubTransport struct {
	mu       sync.Mutex
	requests []stubRequest
	handler  func(req *http.Request, call int) (*http.Response, error)
}

type stubRequest struct {
	method     string
	path       string
	authHeader string
	body       string
}

func (s *stubTransport) Do(req *http.Request) (*http.Response, error) {
	body := ""
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = string(raw)
	}

	s.mu.Lock()
	s.requests = append(s.requests, stubRequest{
		method:     req.Method,
		path:       req.URL.Path,
		authHeader: req.Header.Get("Authorization"),
		body:       body,
	})
	call := len(s.requests)
	s.mu.Unlock()

	return s.handler(req, call)
}

func (s *stubTransport) recorded() []stubRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]stubRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

func (s *stubTransport) countFor(path string) int {
	count := 0
	for _, req := range s.recorded() {
		if req.path == path {
			count++
		}
	}
	return count
}

// jsonResponse builds an in-memory HTTP response.
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// rotatedTokenResponse is the DI token endpoint's rotated answer.
func rotatedTokenResponse() *http.Response {
	return jsonResponse(http.StatusOK, fmt.Sprintf(
		`{"access_token":%q,"refresh_token":%q,"token_type":"Bearer"}`, freshToken, freshRefresh))
}

// refreshHarness wires a Refresher to a stub transport and the fake store.
type refreshHarness struct {
	t         *testing.T
	clock     *testkit.FakeClock
	store     *fakeStore
	transport *stubTransport
	refresher *auth.Refresher
}

// offlineHosts point every Garmin base at a host that is never dialed, because
// the stub transport answers before any connection is made.
func offlineHosts(t *testing.T) protocol.Hosts {
	t.Helper()

	hosts, err := protocol.NewHosts(protocol.DomainGlobal)
	if err != nil {
		t.Fatalf("NewHosts: %v", err)
	}
	return hosts.WithOverrides(protocol.Overrides{
		SSO:               "https://sso.example.invalid",
		Connect:           "https://connect.example.invalid",
		ConnectAPI:        "https://connectapi.example.invalid",
		DIAuth:            "https://diauth.example.invalid",
		MobileIntegration: "https://mobile.example.invalid",
	})
}

func newRefreshHarness(
	t *testing.T,
	handler func(req *http.Request, call int) (*http.Response, error),
) *refreshHarness {
	t.Helper()

	clock := testkit.NewFakeClock(refreshStart())
	store := newFakeStore()
	transport := &stubTransport{handler: handler}

	refresher, err := auth.NewRefresher(auth.RefreshConfig{
		Hosts:     offlineHosts(t),
		Transport: transport,
		Store:     store,
		Clock:     clock,
	})
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}

	return &refreshHarness{t: t, clock: clock, store: store, transport: transport, refresher: refresher}
}

// alwaysRotate answers every DI token call with the same rotated pair.
func alwaysRotate(_ *http.Request, _ int) (*http.Response, error) {
	return rotatedTokenResponse(), nil
}

func storedSet(expiresAt time.Time) auth.TokenSet {
	return auth.NewTokenSet(storedToken, storedRefresh, testClientID, expiresAt)
}
