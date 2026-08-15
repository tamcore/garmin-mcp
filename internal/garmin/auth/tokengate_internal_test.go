package auth

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

const (
	gatePrincipal = "principal-gate"
	gateStored    = "di-token-gate-stored-0900"
	gateRefresh   = "di-refresh-gate-stored-0901"
	gateRotated   = "di-token-gate-rotated-0902"
	gateLogin     = "di-token-gate-login-0903"
	gateTicket    = "ST-gate-0904"
	gateService   = "https://sso.example.invalid/gcm"
)

// TestTokenGateSerializesALoginAgainstTheRefreshPath forces the interleaving that
// loses a rotated token: a refresh is in flight for one principal while an
// interactive login for the same principal wants to write.
//
// The refresh is held inside its transport, so it holds the gate. The login is then
// started and the test waits until the gate reports it as queued, which is the
// deterministic seam: no sleep decides the outcome. While it is queued the login must
// not have produced a candidate token set.
func TestTokenGateSerializesALoginAgainstTheRefreshPath(t *testing.T) {
	var (
		gate           = NewTokenGate()
		refreshArrived = make(chan struct{})
		refreshRelease = make(chan struct{})
		loginExchanges atomic.Int64
		refreshCalls   atomic.Int64
		store          = newMemStore()
	)
	store.put(NewTokenSet(gateStored, gateRefresh, flightClientID, internalStart()), 1)

	refresher, err := NewRefresher(RefreshConfig{
		Hosts: internalHosts(t),
		Store: store,
		Clock: fixedClock{at: internalStart()},
		Transport: funcDoer{fn: func(*http.Request) (*http.Response, error) {
			if refreshCalls.Add(1) == 1 {
				close(refreshArrived)
				<-refreshRelease
			}
			return jsonBody(
				`{"access_token":"` + gateRotated + `","refresh_token":"` + gateRefresh + `"}`), nil
		}},
		TokenGate: gate,
	})
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}

	authenticator := gateAuthenticator(t, store, gate, &loginExchanges)

	refreshDone := make(chan error, 1)
	go func() {
		_, err := refresher.Refresh(t.Context(), gatePrincipal)
		refreshDone <- err
	}()
	<-refreshArrived

	loginDone := make(chan error, 1)
	go func() {
		_, err := authenticator.storeTicketTokens(t.Context(), gatePrincipal, gateTicket, gateService)
		loginDone <- err
	}()
	awaitGateWaiters(t, gate, gatePrincipal, 2)

	if got := loginExchanges.Load(); got != 0 {
		t.Fatalf("the login exchanged %d tickets while a refresh held the gate, want 0", got)
	}

	close(refreshRelease)
	if err := <-refreshDone; err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := <-loginDone; err != nil {
		t.Fatalf("login token write: %v", err)
	}

	// The refresh stored version 2 and the login, queued behind it, read that
	// version as its baseline and stored version 3. Neither write was lost.
	set, version, err := store.Load(t.Context(), gatePrincipal)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if version != 3 {
		t.Errorf("stored version = %d, want 3", version)
	}
	if set.Token() != gateLogin {
		t.Errorf("stored token = %q, want the login's token", set.Token())
	}
	if got := loginExchanges.Load(); got != 1 {
		t.Errorf("the login exchanged %d tickets, want 1", got)
	}
}

// A gate keeps no entry for an idle principal, so it cannot grow without bound.
func TestTokenGateForgetsIdlePrincipals(t *testing.T) {
	gate := NewTokenGate()

	release, err := gate.acquire(t.Context(), gatePrincipal)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := gate.waiters(gatePrincipal); got != 1 {
		t.Fatalf("waiters = %d, want 1", got)
	}

	release()

	if got := gate.waiters(gatePrincipal); got != 0 {
		t.Fatalf("waiters = %d, want 0 after release", got)
	}
}

// A caller whose context ends while it waits leaves the gate rather than blocking
// forever, and it does not take the slot.
func TestTokenGateRespectsAContextThatEnds(t *testing.T) {
	gate := NewTokenGate()

	release, err := gate.acquire(t.Context(), gatePrincipal)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := gate.acquire(ctx, gatePrincipal); err == nil {
		t.Fatal("acquire ignored a cancelled context")
	}
	if got := gate.waiters(gatePrincipal); got != 1 {
		t.Fatalf("waiters = %d, want only the holder", got)
	}
}

// gateAuthenticator builds an Authenticator whose transport answers the DI token
// exchange and the session validation, counting the exchanges.
func gateAuthenticator(
	t *testing.T,
	store TokenStore,
	gate *TokenGate,
	exchanges *atomic.Int64,
) *Authenticator {
	t.Helper()

	registry, err := NewRegistry(RegistryConfig{Clock: fixedClock{at: internalStart()}})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	transport := funcDoer{fn: func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, protocol.PathDIToken) {
			exchanges.Add(1)
			return jsonBody(
				`{"access_token":"` + gateLogin + `","refresh_token":"` + gateRefresh + `"}`), nil
		}
		return jsonBody(`{"displayName":"fake"}`), nil
	}}

	authenticator, err := NewAuthenticator(Config{
		Hosts:     internalHosts(t),
		Transport: transport,
		Store:     store,
		Registry:  registry,
		TokenGate: gate,
		Clock:     fixedClock{at: internalStart()},
		Sleeper:   fixedClock{at: internalStart()},
		Jitter:    func(minDelay, _ time.Duration) time.Duration { return minDelay },
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return authenticator
}

// awaitGateWaiters blocks until want callers hold or await the gate for principal.
// It is the deterministic replacement for sleeping and hoping.
func awaitGateWaiters(t *testing.T, gate *TokenGate, principal string, want int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for gate.waiters(principal) < want {
		if time.Now().After(deadline) {
			t.Fatalf("only %d callers reached the gate, want %d", gate.waiters(principal), want)
		}
		time.Sleep(time.Millisecond)
	}
}
