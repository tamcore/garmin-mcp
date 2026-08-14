package protocol

import (
	"net/http"
	"testing"
	"time"
)

func TestStatusOutcomeLoginPOST(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   Outcome
		wantOK bool
	}{
		{name: "ok has no status verdict", status: http.StatusOK, want: OutcomeUnknown},
		{name: "login 429", status: http.StatusTooManyRequests, want: OutcomeRateLimited, wantOK: true},
		{name: "login 403 is a bot challenge", status: http.StatusForbidden, want: OutcomeBotChallenge, wantOK: true},
		{name: "login 401 is not a credential verdict", status: http.StatusUnauthorized, want: OutcomeUnknown},
		{name: "bad request has no verdict", status: http.StatusBadRequest, want: OutcomeUnknown},
		{name: "not found has no verdict", status: http.StatusNotFound, want: OutcomeUnknown},
		{name: "request timeout", status: http.StatusRequestTimeout, want: OutcomeTemporaryFailure, wantOK: true},
		{name: "too early", status: http.StatusTooEarly, want: OutcomeTemporaryFailure, wantOK: true},
		{name: "internal error", status: http.StatusInternalServerError, want: OutcomeTemporaryFailure, wantOK: true},
		{name: "bad gateway", status: http.StatusBadGateway, want: OutcomeTemporaryFailure, wantOK: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertStatusOutcome(t, contextLoginPOST, tc.status, tc.want, tc.wantOK)
		})
	}
}

func TestStatusOutcomeWidgetPage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   Outcome
		wantOK bool
	}{
		{name: "ok has no status verdict", status: http.StatusOK, want: OutcomeUnknown},
		{name: "widget 429", status: http.StatusTooManyRequests, want: OutcomeRateLimited, wantOK: true},
		{name: "widget 403 is a bot challenge", status: http.StatusForbidden, want: OutcomeBotChallenge, wantOK: true},
		// A widget GET 401 is a session or CSRF problem, never a bad password.
		{name: "widget 401 is not a credential verdict", status: http.StatusUnauthorized, want: OutcomeUnknown},
		{name: "gone has no verdict", status: http.StatusGone, want: OutcomeUnknown},
		{name: "request timeout", status: http.StatusRequestTimeout, want: OutcomeTemporaryFailure, wantOK: true},
		{
			name: "service unavailable", status: http.StatusServiceUnavailable,
			want: OutcomeTemporaryFailure, wantOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertStatusOutcome(t, contextWidgetPage, tc.status, tc.want, tc.wantOK)
		})
	}
}

func TestStatusOutcomeSessionValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   Outcome
		wantOK bool
	}{
		{
			name: "unauthorized rejects the session", status: http.StatusUnauthorized,
			want: OutcomeSessionRejected, wantOK: true,
		},
		// 403 from the profile endpoint is a rejected token, not a WAF challenge.
		{
			name: "forbidden rejects the session", status: http.StatusForbidden,
			want: OutcomeSessionRejected, wantOK: true,
		},
		{name: "validation 429", status: http.StatusTooManyRequests, want: OutcomeRateLimited, wantOK: true},
		{
			name: "request timeout is temporary", status: http.StatusRequestTimeout,
			want: OutcomeTemporaryFailure, wantOK: true,
		},
		{name: "internal error", status: http.StatusInternalServerError, want: OutcomeTemporaryFailure, wantOK: true},
		{name: "not found is inconclusive", status: http.StatusNotFound, want: OutcomeUnknown},
		{name: "ok has no failure verdict", status: http.StatusOK, want: OutcomeUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertStatusOutcome(t, contextSessionValidation, tc.status, tc.want, tc.wantOK)
		})
	}
}

func TestClassifySessionValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		response    Response
		wantOutcome Outcome
		wantRetry   time.Duration
	}{
		{
			name:        "accepted session",
			response:    NewResponseFromParts(http.StatusOK, contentTypeJSON, nil, []byte(`{"id":1}`)),
			wantOutcome: OutcomeSuccess,
		},
		{
			name:        "no content is still accepted",
			response:    NewResponseFromParts(http.StatusNoContent, "", nil, nil),
			wantOutcome: OutcomeSuccess,
		},
		{
			name:        "401 rejects the session without blaming the password",
			response:    NewResponseFromParts(http.StatusUnauthorized, "", nil, []byte(`{"message":"Token is not active"}`)),
			wantOutcome: OutcomeSessionRejected,
		},
		{
			name:        "403 rejects the session",
			response:    NewResponseFromParts(http.StatusForbidden, "", nil, nil),
			wantOutcome: OutcomeSessionRejected,
		},
		{
			name: "429 stays a rate limit",
			response: NewResponseFromParts(
				http.StatusTooManyRequests,
				"",
				http.Header{HeaderRetryAfter: []string{"12"}},
				nil,
			),
			wantOutcome: OutcomeRateLimited,
			wantRetry:   12 * time.Second,
		},
		{
			name:        "5xx is temporary",
			response:    NewResponseFromParts(http.StatusBadGateway, "", nil, nil),
			wantOutcome: OutcomeTemporaryFailure,
		},
		{
			name:        "unexpected 404 is inconclusive",
			response:    NewResponseFromParts(http.StatusNotFound, "", nil, nil),
			wantOutcome: OutcomeUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := ClassifySessionValidation(tc.response)
			if got.Outcome() != tc.wantOutcome {
				t.Fatalf("Outcome = %v, want %v", got.Outcome(), tc.wantOutcome)
			}
			if got.RetryAfter() != tc.wantRetry {
				t.Fatalf("RetryAfter = %v, want %v", got.RetryAfter(), tc.wantRetry)
			}
			if got.Status() != tc.response.Status() {
				t.Fatalf("Status = %d, want %d", got.Status(), tc.response.Status())
			}
		})
	}
}

// Regression guard for the misreporting described by garmin_mcp issue #109: a
// rejected session must never be reported as a credential failure, and it must
// never stop the login strategy chain.
func TestSessionRejectionNeverStopsFallback(t *testing.T) {
	t.Parallel()

	c := ClassifySessionValidation(NewResponseFromParts(http.StatusUnauthorized, "", nil, nil))
	if c.Outcome() == OutcomeInvalidCredentials {
		t.Fatal("session rejection must not classify as invalid credentials")
	}
	if c.Outcome().StopsFallback() {
		t.Fatal("session rejection must not stop strategy fallback")
	}
	if c.Outcome().Retryable() {
		t.Fatal("session rejection must not be retried without a new login")
	}
}

func assertStatusOutcome(t *testing.T, ctx statusContext, status int, want Outcome, wantOK bool) {
	t.Helper()

	got, ok := statusOutcomeFor(ctx, status)
	if ok != wantOK {
		t.Fatalf("statusOutcomeFor(%v, %d) ok = %v, want %v", ctx, status, ok, wantOK)
	}
	if got != want {
		t.Fatalf("statusOutcomeFor(%v, %d) = %v, want %v", ctx, status, got, want)
	}
}
