//go:build fakegarmin

package auth_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Synthetic material the fake Garmin server hands out. Nothing here resembles a
// real account, ticket or token.
const (
	testPrincipal   = "principal-fake"
	testEmail       = "fake-user@example.invalid"
	testPassword    = "fake-password-0500"
	testTicket      = "ST-fake-0501"
	testAccessToken = "di-access-fake-0502"
	testRefresh     = "di-refresh-fake-0503"
	testCSRF        = "csrf-fake-0504"
	testMFACode     = "424242"
)

func fakeStart() time.Time { return time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC) }

// harness wires an Authenticator to the scripted fake Garmin server. The clock is
// fake and the pacing jitter is pinned to the lower bound, so a test observes
// pacing without waiting.
type harness struct {
	t        *testing.T
	server   *testkit.Server
	clock    *testkit.FakeClock
	store    *fakeStore
	registry *auth.Registry
	auth     *auth.Authenticator
}

func newHarness(t *testing.T, script testkit.Script) *harness {
	t.Helper()

	server := testkit.NewServer(t, script)
	clock := testkit.NewFakeClock(fakeStart())
	store := newFakeStore()

	registry, err := auth.NewRegistry(auth.RegistryConfig{Clock: clock})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(auth.Config{
		Hosts:     server.Hosts(protocol.DomainGlobal),
		Transport: server.Doer(),
		Store:     store,
		Registry:  registry,
		Clock:     clock,
		Sleeper:   clock,
		// Pin the jitter to the lower bound so the recorded sleeps are exact.
		Jitter: func(minDelay, _ time.Duration) time.Duration { return minDelay },
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}

	return &harness{
		t:        t,
		server:   server,
		clock:    clock,
		store:    store,
		registry: registry,
		auth:     authenticator,
	}
}

// login runs a credential login for the test principal.
func (h *harness) login() (auth.Result, error) {
	h.t.Helper()
	return h.auth.Login(h.t.Context(), testPrincipal, auth.NewCredentials(testEmail, testPassword))
}

// paths returns the request paths the fake server saw, in order.
func (h *harness) paths() []string {
	h.t.Helper()

	requests := h.server.Requests()
	out := make([]string, 0, len(requests))
	for _, req := range requests {
		out = append(out, req.Path)
	}
	return out
}

// requestCount reports how many times path was requested.
func (h *harness) requestCount(path string) int {
	h.t.Helper()

	count := 0
	for _, seen := range h.paths() {
		if seen == path {
			count++
		}
	}
	return count
}

// assertNoCredentialsInQueries is the standing rule that credentials never ride
// in a URL, where a proxy or an access log would capture them.
func (h *harness) assertNoCredentialsInQueries() {
	h.t.Helper()

	for _, req := range h.server.Requests() {
		for key, values := range req.Query {
			for _, value := range values {
				switch value {
				case testPassword, testEmail, testMFACode, testTicket:
					h.t.Fatalf("request %s carried a credential in query key %q", req.Path, key)
				}
			}
		}
	}
}

// baseScript serves the DI token exchange and the session validation call, which
// every successful login needs.
func baseScript() testkit.Script {
	return testkit.NewScript().
		With(protocol.PathDIToken, testkit.JSON(http.StatusOK,
			testkit.DITokenJSON(testAccessToken, testRefresh))).
		With(protocol.PathSocialProfile, testkit.JSON(http.StatusOK,
			testkit.SocialProfileJSON("Fake User")))
}

// mobileSuccessScript is the shortest happy path: the first strategy succeeds.
func mobileSuccessScript() testkit.Script {
	return baseScript().With(protocol.PathMobileLogin, testkit.JSON(http.StatusOK,
		testkit.LoginSuccessJSON(testTicket)))
}

// widgetPages serves the widget embed GET. The sign-in path is scripted per test
// because the widget flow both GETs and POSTs it, and the queue is per path: the
// first behavior answers the CSRF GET, the second the credential POST.
func widgetPages(script testkit.Script) testkit.Script {
	return script.With(protocol.PathWidgetEmbed,
		testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)))
}
