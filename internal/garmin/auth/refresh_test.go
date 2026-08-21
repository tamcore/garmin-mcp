package auth_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

const (
	storedToken     = "di-token-stored-0600"
	storedRefresh   = "di-refresh-stored-0601"
	freshToken      = "di-token-fresh-0602"
	freshRefresh    = "di-refresh-fresh-0603"
	testClientID    = "GARMIN_CONNECT_MOBILE_ANDROID_DI"
	testPrincipalID = "principal-refresh"
)

func refreshStart() time.Time { return time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC) }

func TestDefaultRefreshWindowMatchesUpstream(t *testing.T) {
	// Source: the 900-second margin in Client._token_expires_soon (0.3.10).
	if auth.DefaultRefreshWindow != 15*time.Minute {
		t.Fatalf("DefaultRefreshWindow = %v, want 15m", auth.DefaultRefreshWindow)
	}
}

func TestFreshKeepsAValidTokenWithoutRefreshing(t *testing.T) {
	h := newRefreshHarness(t, alwaysRotate)
	h.store.put(testPrincipalID, storedSet(refreshStart().Add(time.Hour)), 3)

	set, err := h.refresher.Fresh(t.Context(), testPrincipalID)
	if err != nil {
		t.Fatalf("Fresh: %v", err)
	}
	if set.Token() != storedToken {
		t.Errorf("Token() = %q, want the stored token", set.Token())
	}
	if len(h.transport.recorded()) != 0 {
		t.Errorf("Fresh refreshed a valid token: %v", h.transport.recorded())
	}
	if h.store.saveCount() != 0 {
		t.Error("Fresh wrote to the store without refreshing")
	}
	if h.store.loadCount() != 1 {
		t.Errorf("%d loads, want a single read of the stored set", h.store.loadCount())
	}
}

func TestFreshRefreshesInsideTheSafetyWindow(t *testing.T) {
	h := newRefreshHarness(t, alwaysRotate)
	// Ten minutes of life left is inside the 15-minute window.
	h.store.put(testPrincipalID, storedSet(refreshStart().Add(10*time.Minute)), 7)

	set, err := h.refresher.Fresh(t.Context(), testPrincipalID)
	if err != nil {
		t.Fatalf("Fresh: %v", err)
	}
	if set.Token() != freshToken || set.RefreshToken() != freshRefresh {
		t.Errorf("Fresh returned %v, want the rotated pair", set)
	}
	if h.transport.countFor(protocol.PathDIToken) != 1 {
		t.Errorf("DI token calls = %d, want 1", h.transport.countFor(protocol.PathDIToken))
	}

	stored, version, ok := h.store.get(testPrincipalID)
	if !ok || version != 8 {
		t.Fatalf("stored version = %d, ok = %v, want 8, true", version, ok)
	}
	if stored.RefreshToken() != freshRefresh {
		t.Errorf("the rotated refresh token was not persisted: %v", stored)
	}
}

func TestFreshRefreshesWhenTheExpiryIsUnknown(t *testing.T) {
	h := newRefreshHarness(t, alwaysRotate)
	h.store.put(testPrincipalID, storedSet(time.Time{}), 1)

	if _, err := h.refresher.Fresh(t.Context(), testPrincipalID); err != nil {
		t.Fatalf("Fresh: %v", err)
	}
	if h.transport.countFor(protocol.PathDIToken) != 1 {
		t.Error("an opaque token with no expiry was trusted indefinitely")
	}
}

func TestRefreshSendsTheStoredRefreshTokenAndClientID(t *testing.T) {
	h := newRefreshHarness(t, alwaysRotate)
	h.store.put(testPrincipalID, storedSet(refreshStart()), 1)

	if _, err := h.refresher.Refresh(t.Context(), testPrincipalID); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	recorded := h.transport.recorded()
	if len(recorded) != 1 {
		t.Fatalf("%d requests, want 1", len(recorded))
	}
	req := recorded[0]
	if req.method != http.MethodPost || req.path != protocol.PathDIToken {
		t.Errorf("refresh used %s %s", req.method, req.path)
	}
	if want := protocol.BasicAuthHeader(testClientID); req.authHeader != want {
		t.Errorf("Authorization = %q, want the client id basic header", req.authHeader)
	}
	for _, needed := range []string{protocol.DIGrantTypeRefreshToken, storedRefresh, testClientID} {
		if !strings.Contains(req.body, needed) {
			t.Errorf("refresh body is missing %q", needed)
		}
	}
}

func TestRefreshKeepsThePreviousRefreshTokenWhenGarminOmitsIt(t *testing.T) {
	h := newRefreshHarness(t, func(_ *http.Request, _ int) (*http.Response, error) {
		return jsonResponse(http.StatusOK, fmt.Sprintf(`{"access_token":%q}`, freshToken)), nil
	})
	h.store.put(testPrincipalID, storedSet(refreshStart()), 1)

	set, err := h.refresher.Refresh(t.Context(), testPrincipalID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if set.RefreshToken() != storedRefresh {
		t.Errorf("RefreshToken() = %q, want the previous token kept", set.RefreshToken())
	}
}

func TestRefreshRequiresStoredTokens(t *testing.T) {
	h := newRefreshHarness(t, alwaysRotate)

	_, err := h.refresher.Refresh(t.Context(), testPrincipalID)
	if !errors.Is(err, auth.ErrNoTokens) {
		t.Fatalf("err = %v, want ErrNoTokens", err)
	}
	if len(h.transport.recorded()) != 0 {
		t.Error("a refresh was attempted without stored tokens")
	}
}

func TestRefreshRequiresARefreshToken(t *testing.T) {
	h := newRefreshHarness(t, alwaysRotate)
	h.store.put(testPrincipalID, auth.NewTokenSet(storedToken, "", testClientID, refreshStart()), 1)

	_, err := h.refresher.Refresh(t.Context(), testPrincipalID)
	if !errors.Is(err, auth.ErrNoRefreshToken) {
		t.Fatalf("err = %v, want ErrNoRefreshToken", err)
	}
}

func TestRefreshRejectsAnEmptyPrincipal(t *testing.T) {
	h := newRefreshHarness(t, alwaysRotate)

	if _, err := h.refresher.Refresh(t.Context(), ""); !errors.Is(err, auth.ErrMissingPrincipal) {
		t.Fatalf("err = %v, want ErrMissingPrincipal", err)
	}
}

func TestRefreshReportsAFailedTokenEndpoint(t *testing.T) {
	h := newRefreshHarness(t, func(_ *http.Request, _ int) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, `{"error":"later"}`), nil
	})
	h.store.put(testPrincipalID, storedSet(refreshStart()), 1)

	_, err := h.refresher.Refresh(t.Context(), testPrincipalID)
	if !errors.Is(err, protocol.ErrTemporary) {
		t.Fatalf("err = %v, want a temporary failure", err)
	}
	if h.store.saveCount() != 0 {
		t.Error("a failed refresh wrote to the store")
	}
}

func TestRefreshClassifiesRejectedCredentialsWithoutCollapsingOtherFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		want       error
		rejected   bool
		stillClass error
	}{
		{name: "bad request", status: http.StatusBadRequest, rejected: true,
			stillClass: protocol.ErrUnknownResponse},
		{name: "unauthorized", status: http.StatusUnauthorized, rejected: true,
			stillClass: protocol.ErrUnknownResponse},
		{name: "bot challenge", status: http.StatusForbidden, want: protocol.ErrBotChallenge},
		{name: "rate limited", status: http.StatusTooManyRequests, want: protocol.ErrRateLimited},
		{name: "server failure", status: http.StatusInternalServerError, want: protocol.ErrTemporary},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newRefreshHarness(t, func(_ *http.Request, _ int) (*http.Response, error) {
				return jsonResponse(tc.status, `{"error":"refused"}`), nil
			})
			h.store.put(testPrincipalID, storedSet(refreshStart()), 1)

			_, err := h.refresher.Refresh(t.Context(), testPrincipalID)
			if got := errors.Is(err, auth.ErrRefreshRejected); got != tc.rejected {
				t.Errorf("errors.Is(err, ErrRefreshRejected) = %t, want %t: %v",
					got, tc.rejected, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if tc.stillClass != nil && !errors.Is(err, tc.stillClass) {
				t.Errorf("err = %v no longer retains %v", err, tc.stillClass)
			}
		})
	}
}

// TestConcurrentRefreshCollapsesIntoOneFlight is the singleflight requirement.
// Run under -race.
func TestConcurrentRefreshCollapsesIntoOneFlight(t *testing.T) {
	release := make(chan struct{})
	h := newRefreshHarness(t, func(_ *http.Request, _ int) (*http.Response, error) {
		// Hold the flight open until every caller has arrived, so a second
		// request would have to be concurrent to be counted.
		<-release
		return rotatedTokenResponse(), nil
	})
	h.store.put(testPrincipalID, storedSet(refreshStart()), 1)

	const callers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []auth.TokenSet
	)
	wg.Add(callers)

	for range callers {
		go func() {
			defer wg.Done()
			set, err := h.refresher.Refresh(t.Context(), testPrincipalID)
			if err != nil {
				t.Errorf("Refresh: %v", err)
				return
			}
			mu.Lock()
			results = append(results, set)
			mu.Unlock()
		}()
	}

	// Give the callers time to pile up behind the in-flight refresh.
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := h.transport.countFor(protocol.PathDIToken); got != 1 {
		t.Fatalf("%d DI token calls, want exactly 1 in-flight refresh", got)
	}
	if h.store.saveCount() != 1 {
		t.Fatalf("%d saves, want 1", h.store.saveCount())
	}
	if len(results) != callers {
		t.Fatalf("%d callers got a result, want %d", len(results), callers)
	}
	for _, set := range results {
		if set.Token() != freshToken {
			t.Fatalf("a caller received %v, want the rotated token", set)
		}
	}
}

// Two principals must not serialize behind each other. Run under -race.
func TestConcurrentRefreshOfDifferentPrincipalsDoesNotSerialize(t *testing.T) {
	var (
		mu      sync.Mutex
		inside  int
		bothIn  = make(chan struct{})
		once    sync.Once
		timeout = time.After(5 * time.Second)
	)

	h := newRefreshHarness(t, func(_ *http.Request, _ int) (*http.Response, error) {
		mu.Lock()
		inside++
		reached := inside >= 2
		mu.Unlock()

		if reached {
			once.Do(func() { close(bothIn) })
		}
		select {
		case <-bothIn:
		case <-timeout:
			return nil, errors.New("the second principal never reached the transport concurrently")
		}
		return rotatedTokenResponse(), nil
	})
	h.store.put("principal-a", storedSet(refreshStart()), 1)
	h.store.put("principal-b", storedSet(refreshStart()), 1)

	var wg sync.WaitGroup
	for _, principal := range []string{"principal-a", "principal-b"} {
		wg.Add(1)
		go func(principal string) {
			defer wg.Done()
			if _, err := h.refresher.Refresh(t.Context(), principal); err != nil {
				t.Errorf("Refresh %s: %v", principal, err)
			}
		}(principal)
	}
	wg.Wait()

	if got := h.transport.countFor(protocol.PathDIToken); got != 2 {
		t.Fatalf("%d DI token calls, want one per principal", got)
	}
}

// A slow writer must not clobber a newer rotated token: the compare-and-set
// fails, and the newer stored set is returned instead.
func TestRefreshYieldsToANewerStoredTokenOnConflict(t *testing.T) {
	h := newRefreshHarness(t, alwaysRotate)
	h.store.put(testPrincipalID, storedSet(refreshStart()), 1)

	competitor := auth.NewTokenSet("di-token-newer-0604", "di-refresh-newer-0605",
		testClientID, refreshStart().Add(time.Hour))
	var once sync.Once
	h.store.beforeSave = func(principal string) {
		once.Do(func() { h.store.bump(principal, competitor) })
	}

	set, err := h.refresher.Refresh(t.Context(), testPrincipalID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if set.Token() != competitor.Token() {
		t.Errorf("Refresh returned %v, want the newer stored token", set)
	}
	if h.store.saveCount() != 1 {
		t.Errorf("%d saves, want exactly one rejected attempt", h.store.saveCount())
	}

	stored, _, _ := h.store.get(testPrincipalID)
	if stored.Token() != competitor.Token() {
		t.Error("the newer stored token was clobbered")
	}
}

func TestRefresherRequiresCompleteConfiguration(t *testing.T) {
	hosts := offlineHosts(t)

	for name, cfg := range map[string]auth.RefreshConfig{
		"no transport":  {Hosts: hosts, Store: newFakeStore()},
		"no store":      {Hosts: hosts, Transport: &stubTransport{handler: alwaysRotate}},
		"unusable host": {Transport: &stubTransport{handler: alwaysRotate}, Store: newFakeStore()},
		"negative window": {
			Hosts: hosts, Transport: &stubTransport{handler: alwaysRotate},
			Store: newFakeStore(), SafetyWindow: -time.Minute,
		},
	} {
		if _, err := auth.NewRefresher(cfg); err == nil {
			t.Errorf("%s: NewRefresher accepted the configuration", name)
		}
	}
}

func TestRefreshErrorsCarryNoSecrets(t *testing.T) {
	leaky := `{"error":"` + storedRefresh + `","access_token":"` + storedToken + `"}`
	h := newRefreshHarness(t, func(_ *http.Request, _ int) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, leaky), nil
	})
	h.store.put(testPrincipalID, storedSet(refreshStart()), 1)

	_, err := h.refresher.Refresh(t.Context(), testPrincipalID)
	if err == nil {
		t.Fatal("Refresh succeeded against a failing endpoint")
	}
	for _, bad := range []string{storedRefresh, storedToken, leaky} {
		if strings.Contains(err.Error(), bad) {
			t.Fatalf("error %q leaked %q", err, bad)
		}
	}
}
