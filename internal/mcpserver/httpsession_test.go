package mcpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

// mcpGET builds a standalone SSE GET, optionally resuming from an event id.
func mcpGET(t *testing.T, token, sessionID, lastEventID string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, testPublicURL, nil)
	req.Header.Set("Accept", "text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	return req
}

func mcpDELETE(t *testing.T, token, sessionID string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodDelete, testPublicURL, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	return req
}

func TestSessionIDDoesNotAuthenticate(t *testing.T) {
	// A session id is a routing label, not a credential. Presenting one without a
	// token must be refused exactly as a bare request is.

	// Arrange
	transport := newBoundTransport(t)
	sessionID := initSession(t, transport, tokenAlice)

	tests := map[string]*http.Request{
		"POST":   mcpPOST(t, callToolBody(2), "", sessionID),
		"GET":    mcpGET(t, "", sessionID, ""),
		"DELETE": mcpDELETE(t, "", sessionID),
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			// Act
			recorder := httptest.NewRecorder()
			transport.ServeHTTP(recorder, req)

			// Assert
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: a session id authenticated a request", recorder.Code)
			}
		})
	}
}

func TestSessionRefusesACrossPrincipalRequest(t *testing.T) {
	// Bob holds a perfectly valid token. It does not make Alice's session his.

	// Arrange
	transport := newBoundTransport(t)
	sessionID := initSession(t, transport, tokenAlice)

	tests := map[string]*http.Request{
		"read":   mcpPOST(t, callToolBody(2), tokenBob, sessionID),
		"resume": mcpGET(t, tokenBob, sessionID, ""),
		"delete": mcpDELETE(t, tokenBob, sessionID),
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			// Act
			recorder := httptest.NewRecorder()
			transport.ServeHTTP(recorder, req)

			// Assert
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: another principal used the session", recorder.Code)
			}
			if strings.Contains(recorder.Body.String(), sessionID) {
				t.Fatalf("the refusal reflected the session id")
			}
		})
	}
}

func TestSessionRefusesACrossClientRequest(t *testing.T) {
	// Same principal, different OAuth client. Consent is bound per client, so one
	// client's session must not be usable by another.

	// Arrange
	transport := newBoundTransport(t)
	sessionID := initSession(t, transport, tokenAlice)

	tests := map[string]*http.Request{
		"read":   mcpPOST(t, callToolBody(2), tokenOther, sessionID),
		"resume": mcpGET(t, tokenOther, sessionID, ""),
		"delete": mcpDELETE(t, tokenOther, sessionID),
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			// Act
			recorder := httptest.NewRecorder()
			transport.ServeHTTP(recorder, req)

			// Assert
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: another client used the session", recorder.Code)
			}
		})
	}
}

func TestSessionRefusesACrossPrincipalEventResumption(t *testing.T) {
	// Last-Event-ID is not a credential either, and it cannot reach another
	// principal's event buffer.

	// Arrange
	transport := newBoundTransport(t)
	sessionID := initSession(t, transport, tokenAlice)
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, mcpGET(t, tokenBob, sessionID, "stream-1_1"))

	// Assert
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: another principal resumed the event stream", recorder.Code)
	}
}

func TestSessionAllowsTheOwningPrincipalToDelete(t *testing.T) {
	// Arrange
	transport := newBoundTransport(t)
	sessionID := initSession(t, transport, tokenAlice)
	recorder := httptest.NewRecorder()

	// Act
	transport.ServeHTTP(recorder, mcpDELETE(t, tokenAlice, sessionID))

	// Assert
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %q)", recorder.Code, recorder.Body.String())
	}

	// And the session is gone, so a later request cannot use it.
	after := httptest.NewRecorder()
	transport.ServeHTTP(after, mcpPOST(t, callToolBody(3), tokenAlice, sessionID))
	if after.Code == http.StatusOK {
		t.Fatalf("a deleted session still served a request")
	}
}

func TestSessionIsTerminatedByRevocation(t *testing.T) {
	// Revoking the token family must terminate the session, not merely refuse the
	// next request: an SSE stream held open would otherwise keep delivering.

	// Arrange
	authorizer := newFakeAuthorizer(t)
	revocations := newFakeRevocations()
	opts := testHTTPOptions(authorizer)
	opts.Revocations = revocations
	transport := newTestTransport(t, opts)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- transport.Run(ctx) }()

	sessionID := initSession(t, transport, tokenAlice)

	// Act
	revocations.send(mcpserver.Revocation{
		Principal: mustPrincipalID(t, principalAlice),
		ClientID:  clientA,
		Family:    familyAlice,
	})

	// Assert: the session is closed, so it can no longer be addressed at all.
	deadline := time.Now().Add(2 * time.Second)
	for {
		recorder := httptest.NewRecorder()
		transport.ServeHTTP(recorder, mcpPOST(t, callToolBody(2), tokenAlice, sessionID))
		if recorder.Code == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session survived revocation; last status = %d", recorder.Code)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunIgnoresAnEmptyRevocation(t *testing.T) {
	// A revocation that names nothing selects everything. Acting on it would let
	// one buggy producer close every session in the process.

	// Arrange
	authorizer := newFakeAuthorizer(t)
	revocations := newFakeRevocations()
	opts := testHTTPOptions(authorizer)
	opts.Revocations = revocations
	transport := newTestTransport(t, opts)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = transport.Run(ctx) }()

	sessionID := initSession(t, transport, tokenAlice)

	// Act
	revocations.send(mcpserver.Revocation{})
	time.Sleep(50 * time.Millisecond)

	// Assert
	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder, mcpPOST(t, callToolBody(2), tokenAlice, sessionID))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: an empty revocation closed a session", recorder.Code)
	}
}

func TestSessionRefusalNeverReflectsTheSessionID(t *testing.T) {
	// A session id is capability material for the routing layer. It must not come
	// back out in a body, a header, or an error.

	// Arrange
	transport := newBoundTransport(t)
	sessionID := initSession(t, transport, tokenAlice)

	// Act
	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder, mcpDELETE(t, tokenBob, sessionID))

	// Assert
	rendered := recorder.Body.String() + recorder.Header().Get("WWW-Authenticate")
	if strings.Contains(rendered, sessionID) {
		t.Fatalf("a refusal reflected the session id: %q", rendered)
	}
}

// fakeRevocations is a RevocationSource a test drives by hand.
type fakeRevocations struct {
	events chan mcpserver.Revocation
}

func newFakeRevocations() *fakeRevocations {
	return &fakeRevocations{events: make(chan mcpserver.Revocation, 4)}
}

func (f *fakeRevocations) Revocations(context.Context) <-chan mcpserver.Revocation {
	return f.events
}

func (f *fakeRevocations) send(event mcpserver.Revocation) { f.events <- event }

func TestRevocationSelectorsChooseTheRightSessions(t *testing.T) {
	// Every field of a Revocation is a selector and an empty field matches
	// anything. Getting that wrong either leaves a revoked caller connected or
	// disconnects an unrelated one, so each selector is exercised on its own.

	tests := map[string]struct {
		event    mcpserver.Revocation
		survives []string
		dies     []string
	}{
		"one token family": {
			event:    mcpserver.Revocation{Family: familyAlice},
			dies:     []string{tokenAlice},
			survives: []string{tokenBob, tokenOther},
		},
		"every client of one principal": {
			event:    mcpserver.Revocation{Principal: mustPrincipalID(t, principalAlice)},
			dies:     []string{tokenAlice, tokenOther},
			survives: []string{tokenBob},
		},
		"one client across principals": {
			event:    mcpserver.Revocation{ClientID: clientB},
			dies:     []string{tokenBob, tokenOther},
			survives: []string{tokenAlice},
		},
		"one principal at one client": {
			event: mcpserver.Revocation{
				Principal: mustPrincipalID(t, principalAlice), ClientID: clientB,
			},
			dies:     []string{tokenOther},
			survives: []string{tokenAlice, tokenBob},
		},
		"a principal that has no session": {
			event:    mcpserver.Revocation{Principal: mustPrincipalID(t, "principal-nobody")},
			survives: []string{tokenAlice, tokenBob, tokenOther},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			authorizer := newFakeAuthorizer(t)
			revocations := newFakeRevocations()
			opts := testHTTPOptions(authorizer)
			opts.Revocations = revocations
			transport := newTestTransport(t, opts)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			go func() { _ = transport.Run(ctx) }()

			sessions := map[string]string{}
			for _, token := range []string{tokenAlice, tokenBob, tokenOther} {
				sessions[token] = initSession(t, transport, token)
			}

			// Act
			revocations.send(tc.event)

			// Assert
			for _, token := range tc.dies {
				waitForStatus(t, transport, token, sessions[token], http.StatusNotFound)
			}
			for _, token := range tc.survives {
				assertStatus(t, transport, token, sessions[token], http.StatusOK)
			}
		})
	}
}

// waitForStatus polls until a session reaches want, because termination happens
// on the Run goroutine and is therefore not synchronous with the send.
func waitForStatus(
	t *testing.T, transport *mcpserver.HTTPTransport, token, sessionID string, want int,
) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		recorder := httptest.NewRecorder()
		transport.ServeHTTP(recorder, mcpPOST(t, callToolBody(9), token, sessionID))
		last = recorder.Code
		if last == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session status = %d, want %d", last, want)
}

// assertStatus checks a session that must not have been touched. It waits first,
// so a wrongly broad revocation has had time to land rather than being missed.
func assertStatus(
	t *testing.T, transport *mcpserver.HTTPTransport, token, sessionID string, want int,
) {
	t.Helper()

	time.Sleep(50 * time.Millisecond)
	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder, mcpPOST(t, callToolBody(9), token, sessionID))
	if recorder.Code != want {
		t.Fatalf("session status = %d, want %d (body %q)",
			recorder.Code, want, recorder.Body.String())
	}
}
