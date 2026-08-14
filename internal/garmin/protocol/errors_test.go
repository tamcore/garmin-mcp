package protocol

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestErrorSentinelMatching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome Outcome
		want    error
	}{
		{name: "mfa", outcome: OutcomeMFARequired, want: ErrMFARequired},
		{name: "invalid credentials", outcome: OutcomeInvalidCredentials, want: ErrInvalidCredentials},
		{name: "locked", outcome: OutcomeAccountLocked, want: ErrAccountLocked},
		{name: "restricted", outcome: OutcomeAccountRestricted, want: ErrAccountRestricted},
		{name: "bot challenge", outcome: OutcomeBotChallenge, want: ErrBotChallenge},
		{name: "rate limited", outcome: OutcomeRateLimited, want: ErrRateLimited},
		{name: "temporary", outcome: OutcomeTemporaryFailure, want: ErrTemporary},
		{name: "unknown", outcome: OutcomeUnknown, want: ErrUnknownResponse},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := &Error{Op: "login", Endpoint: EndpointMobileLogin, Status: http.StatusOK, Outcome: tc.outcome}
			if !errors.Is(err, tc.want) {
				t.Fatalf("errors.Is(%v, %v) = false, want true", err, tc.want)
			}
			if tc.want != ErrAccountLocked && errors.Is(err, ErrAccountLocked) {
				t.Fatalf("outcome %v wrongly matched ErrAccountLocked", tc.outcome)
			}
		})
	}
}

func TestErrorWrapsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("synthetic dial failure")
	err := &Error{Op: "login", Endpoint: EndpointMobileLogin, Outcome: OutcomeTemporaryFailure, Err: cause}

	if !errors.Is(err, cause) {
		t.Fatal("errors.Is did not reach the wrapped cause")
	}

	var target *Error
	if !errors.As(fmt.Errorf("outer: %w", err), &target) {
		t.Fatal("errors.As failed through an outer wrapper")
	}
	if target.Endpoint != EndpointMobileLogin {
		t.Fatalf("Endpoint = %q, want %q", target.Endpoint, EndpointMobileLogin)
	}
	if !target.Retryable() {
		t.Fatal("temporary failure must be retryable")
	}
}

func TestErrorMessageIsSanitized(t *testing.T) {
	t.Parallel()

	const secret = "S3cr3t-Passw0rd"
	err := &Error{
		Op:         "verify_mfa",
		Endpoint:   EndpointMobileMFAVerifyCode,
		Status:     http.StatusTooManyRequests,
		Outcome:    OutcomeRateLimited,
		RetryAfter: 30 * time.Second,
		Err:        errors.New("upstream throttled"),
	}

	msg := err.Error()
	for _, forbidden := range []string{secret, "Cookie", "Bearer", "<html"} {
		if strings.Contains(msg, forbidden) {
			t.Fatalf("error message %q leaked %q", msg, forbidden)
		}
	}
	wantParts := []string{"verify_mfa", EndpointMobileMFAVerifyCode, "429", "rate_limited", "30s", "upstream throttled"}
	for _, want := range wantParts {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message %q missing %q", msg, want)
		}
	}
}

func TestErrorNilSafety(t *testing.T) {
	t.Parallel()

	var err *Error
	if err.Retryable() {
		t.Fatal("nil *Error must not be retryable")
	}
	if got := err.Error(); got != "protocol: <nil>" {
		t.Fatalf("Error() = %q, want %q", got, "protocol: <nil>")
	}
	if err.Unwrap() != nil {
		t.Fatal("nil *Error must unwrap to nil")
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)

	tests := []struct {
		name   string
		value  string
		want   time.Duration
		wantOK bool
	}{
		{name: "empty", value: "", want: 0, wantOK: false},
		{name: "delta seconds", value: "42", want: 42 * time.Second, wantOK: true},
		{name: "padded delta seconds", value: "  7 ", want: 7 * time.Second, wantOK: true},
		{name: "zero delta", value: "0", want: 0, wantOK: true},
		{name: "negative delta clamped", value: "-5", want: 0, wantOK: true},
		{name: "http date in future", value: "Fri, 02 Jan 2026 15:05:05 GMT", want: time.Minute, wantOK: true},
		{name: "http date in past clamped", value: "Fri, 02 Jan 2026 15:00:05 GMT", want: 0, wantOK: true},
		{name: "garbage", value: "soon-ish", want: 0, wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ParseRetryAfter(tc.value, now)
			if ok != tc.wantOK {
				t.Fatalf("ParseRetryAfter(%q) ok = %v, want %v", tc.value, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("ParseRetryAfter(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
