package auth_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

const apiPath = "/userprofile-service/socialProfile"

// apiRequest builds a request against the API tier.
func apiRequest(t *testing.T, method, body string) *http.Request {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method,
		"https://connectapi.example.invalid"+apiPath, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return req
}

// seedValidTokens stores a token set that is far from expiry, so Do performs no
// proactive refresh.
func seedValidTokens(h *refreshHarness) {
	h.store.put(testPrincipalID, storedSet(refreshStart().Add(time.Hour)), 1)
}

func TestDoAttachesTheStoredBearerToken(t *testing.T) {
	h := newRefreshHarness(t, func(_ *http.Request, _ int) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"displayName":"fake"}`), nil
	})
	seedValidTokens(h)

	resp, err := h.refresher.Do(t.Context(), testPrincipalID, apiRequest(t, http.MethodGet, ""))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	recorded := h.transport.recorded()
	if len(recorded) != 1 {
		t.Fatalf("%d requests, want 1", len(recorded))
	}
	if want := "Bearer " + storedToken; recorded[0].authHeader != want {
		t.Errorf("Authorization = %q, want %q", recorded[0].authHeader, want)
	}
}

func TestDoRetriesOnceAfterASuccessfulRefresh(t *testing.T) {
	h := newRefreshHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
		switch {
		case req.URL.Path == protocol.PathDIToken:
			return rotatedTokenResponse(), nil
		case req.Header.Get("Authorization") == "Bearer "+storedToken:
			return jsonResponse(http.StatusUnauthorized, `{"message":"expired"}`), nil
		default:
			return jsonResponse(http.StatusOK, `{"displayName":"fake"}`), nil
		}
	})
	seedValidTokens(h)

	resp, err := h.refresher.Do(t.Context(), testPrincipalID, apiRequest(t, http.MethodGet, ""))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after the refresh", resp.StatusCode)
	}

	recorded := h.transport.recorded()
	if len(recorded) != 3 {
		t.Fatalf("%d requests, want the call, the refresh and one replay: %v", len(recorded), recorded)
	}
	if recorded[1].path != protocol.PathDIToken {
		t.Errorf("request 2 was %s, want the DI token endpoint", recorded[1].path)
	}
	if want := "Bearer " + freshToken; recorded[2].authHeader != want {
		t.Errorf("the replay used %q, want %q", recorded[2].authHeader, want)
	}

	stored, version, _ := h.store.get(testPrincipalID)
	if stored.Token() != freshToken || version != 2 {
		t.Errorf("stored set = %v at version %d, want the rotated token at version 2", stored, version)
	}
}

func TestDoRetriesAtMostOnce(t *testing.T) {
	h := newRefreshHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
		if req.URL.Path == protocol.PathDIToken {
			return rotatedTokenResponse(), nil
		}
		return jsonResponse(http.StatusUnauthorized, `{"message":"expired"}`), nil
	})
	seedValidTokens(h)

	resp, err := h.refresher.Do(t.Context(), testPrincipalID, apiRequest(t, http.MethodGet, ""))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want the second 401 to be returned", resp.StatusCode)
	}
	if got := h.transport.countFor(apiPath); got != 2 {
		t.Fatalf("%d API calls, want the original and exactly one replay", got)
	}
	if got := h.transport.countFor(protocol.PathDIToken); got != 1 {
		t.Fatalf("%d refreshes, want 1", got)
	}
}

// A 401 on a call that is not safe or idempotent must be handed back untouched:
// replaying it could apply a mutation twice.
func TestDoDoesNotRetryNonIdempotentCalls(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			h := newRefreshHarness(t, func(_ *http.Request, _ int) (*http.Response, error) {
				return jsonResponse(http.StatusUnauthorized, `{"message":"expired"}`), nil
			})
			seedValidTokens(h)

			resp, err := h.refresher.Do(t.Context(), testPrincipalID, apiRequest(t, method, `{"a":1}`))
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want the 401 passed through", resp.StatusCode)
			}
			if got := h.transport.countFor(apiPath); got != 1 {
				t.Fatalf("%d API calls, want no replay", got)
			}
			if got := h.transport.countFor(protocol.PathDIToken); got != 0 {
				t.Fatalf("%d refreshes, want none for a non-idempotent call", got)
			}
		})
	}
}

// An idempotent call with a body must be replayed with that body intact.
func TestDoReplaysAnIdempotentBody(t *testing.T) {
	const payload = `{"weight":72}`

	h := newRefreshHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
		switch {
		case req.URL.Path == protocol.PathDIToken:
			return rotatedTokenResponse(), nil
		case req.Header.Get("Authorization") == "Bearer "+storedToken:
			return jsonResponse(http.StatusUnauthorized, `{"message":"expired"}`), nil
		default:
			return jsonResponse(http.StatusOK, `{}`), nil
		}
	})
	seedValidTokens(h)

	resp, err := h.refresher.Do(t.Context(), testPrincipalID, apiRequest(t, http.MethodPut, payload))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	recorded := h.transport.recorded()
	if len(recorded) != 3 {
		t.Fatalf("%d requests, want three: %v", len(recorded), recorded)
	}
	if recorded[0].body != payload || recorded[2].body != payload {
		t.Fatalf("bodies = %q and %q, want both to be %q", recorded[0].body, recorded[2].body, payload)
	}
}

// A 401 whose refresh fails must not be replayed at all.
func TestDoDoesNotRetryWhenTheRefreshFails(t *testing.T) {
	h := newRefreshHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
		if req.URL.Path == protocol.PathDIToken {
			return jsonResponse(http.StatusInternalServerError, `{"error":"later"}`), nil
		}
		return jsonResponse(http.StatusUnauthorized, `{"message":"expired"}`), nil
	})
	seedValidTokens(h)

	_, err := h.refresher.Do(t.Context(), testPrincipalID, apiRequest(t, http.MethodGet, ""))
	if !errors.Is(err, protocol.ErrTemporary) {
		t.Fatalf("err = %v, want the refresh failure", err)
	}
	if got := h.transport.countFor(apiPath); got != 1 {
		t.Fatalf("%d API calls, want no replay after a failed refresh", got)
	}
}

func TestDoRejectsBadInput(t *testing.T) {
	h := newRefreshHarness(t, alwaysRotate)

	_, err := h.refresher.Do(t.Context(), "", apiRequest(t, http.MethodGet, ""))
	if !errors.Is(err, auth.ErrMissingPrincipal) {
		t.Errorf("empty principal: err = %v, want ErrMissingPrincipal", err)
	}
	if _, err := h.refresher.Do(t.Context(), testPrincipalID, nil); err == nil {
		t.Error("a nil request was accepted")
	}
	_, err = h.refresher.Do(t.Context(), testPrincipalID, apiRequest(t, http.MethodGet, ""))
	if !errors.Is(err, auth.ErrNoTokens) {
		t.Errorf("no stored tokens: err = %v, want ErrNoTokens", err)
	}
}

// Concurrent 401s for one principal must trigger exactly one refresh. Run under
// -race.
func TestConcurrentDoSharesOneRefresh(t *testing.T) {
	h := newRefreshHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
		switch {
		case req.URL.Path == protocol.PathDIToken:
			// Hold the refresh open long enough for every caller to queue.
			time.Sleep(10 * time.Millisecond)
			return rotatedTokenResponse(), nil
		case req.Header.Get("Authorization") == "Bearer "+storedToken:
			return jsonResponse(http.StatusUnauthorized, `{"message":"expired"}`), nil
		default:
			return jsonResponse(http.StatusOK, `{}`), nil
		}
	})
	seedValidTokens(h)

	const callers = 8
	var wg sync.WaitGroup
	wg.Add(callers)

	for range callers {
		go func() {
			defer wg.Done()

			resp, err := h.refresher.Do(t.Context(), testPrincipalID, apiRequest(t, http.MethodGet, ""))
			if err != nil {
				t.Errorf("Do: %v", err)
				return
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
		}()
	}
	wg.Wait()

	if got := h.transport.countFor(protocol.PathDIToken); got != 1 {
		t.Fatalf("%d refreshes, want exactly 1 shared flight", got)
	}
}
