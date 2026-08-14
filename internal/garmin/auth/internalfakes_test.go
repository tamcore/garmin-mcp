package auth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// The in-package tests exercise seams that are deliberately unexported: the
// per-principal token gate and the refresh flight bookkeeping. They need their
// own fakes, because the fakes in package auth_test are not importable here.

// memStore is a compare-and-set TokenStore held in memory.
type memStore struct {
	mu      sync.Mutex
	set     TokenSet
	version int64
	present bool
}

func newMemStore() *memStore { return &memStore{} }

func (s *memStore) Load(_ context.Context, principal string) (TokenSet, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.present {
		return TokenSet{}, 0, fmt.Errorf("mem store: %q: %w", principal, ErrNoTokens)
	}
	return s.set, s.version, nil
}

func (s *memStore) Save(_ context.Context, principal string, set TokenSet, expected int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.version != expected {
		return 0, fmt.Errorf("mem store: %q: %w", principal, ErrVersionConflict)
	}
	s.set, s.version, s.present = set, expected+1, true
	return s.version, nil
}

func (s *memStore) Delete(_ context.Context, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.set, s.version, s.present = TokenSet{}, 0, false
	return nil
}

func (s *memStore) put(set TokenSet, version int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.set, s.version, s.present = set, version, true
}

// funcDoer answers every request from a function, so no test reaches a network.
type funcDoer struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (d funcDoer) Do(req *http.Request) (*http.Response, error) { return d.fn(req) }

// jsonBody builds an in-memory JSON response with a 200 status, which is what the
// in-package tests need: they exercise concurrency, not classification.
func jsonBody(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// internalHosts point every base at a host that is never dialed.
func internalHosts(t *testing.T) protocol.Hosts {
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

// internalStart anchors the in-package tests at a fixed instant.
func internalStart() time.Time { return time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC) }

// fixedClock is a Clock and Sleeper that never advances and never waits.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

func (fixedClock) Sleep(time.Duration) {}
