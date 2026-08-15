//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// The probe and rate-limit features were built and unit-tested before anything
// mounted them, so for a while both behaved perfectly in their own package and
// answered 404 in a real deployment. These tests drive the shipped binary, which
// is the only place that gap was visible.

func TestLivenessAndReadinessAnswerOnTheRunningServer(t *testing.T) {
	server := startRemoteServer(t)

	for path, name := range map[string]string{"/livez": "liveness", "/readyz": "readiness"} {
		response, err := server.client.Get(server.origin + path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()

		if response.StatusCode != http.StatusOK {
			t.Errorf("%s status = %d, want 200 on a healthy deployment", name, response.StatusCode)
		}
		if got := response.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", name, got)
		}

		// A probe is reachable without a token, so everything it says is public.
		text := strings.ToLower(string(body))
		for _, disclosure := range []string{"garmin", "client", "sqlite", "principal", "127.0.0.1"} {
			if strings.Contains(text, disclosure) {
				t.Errorf("%s body %q discloses %q", name, string(body), disclosure)
			}
		}
		if len(body) > 64 {
			t.Errorf("%s body is %d bytes; a probe answers a word", name, len(body))
		}
	}
}

// TestTheAuthorizationEndpointsAreRateLimited proves the limiter is mounted, not
// merely implemented. The token endpoint is reachable by anyone who can open a
// socket, so an unbounded one is a credential-stuffing target however good the
// token checks behind it are.
func TestTheAuthorizationEndpointsAreRateLimited(t *testing.T) {
	server := startRemoteServer(t)

	var limited *http.Response
	for range 200 {
		response, err := server.client.Post(server.origin+"/token",
			"application/x-www-form-urlencoded",
			strings.NewReader("grant_type=authorization_code&code=synthetic&client_id=probe"))
		if err != nil {
			t.Fatalf("post to the token endpoint: %v", err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()

		if response.StatusCode == http.StatusTooManyRequests {
			limited = response
			break
		}
	}

	if limited == nil {
		t.Fatal("200 token requests from one address were never limited")
	}

	retryAfter := limited.Header.Get("Retry-After")
	if seconds, err := strconv.Atoi(retryAfter); err != nil || seconds < 1 {
		t.Errorf("Retry-After = %q, want a positive whole number of seconds", retryAfter)
	}
	if got := limited.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}
