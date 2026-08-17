package protocol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
		{name: "session rejected", outcome: OutcomeSessionRejected, want: ErrSessionRejected},
		{name: "mfa rejected", outcome: OutcomeMFARejected, want: ErrMFARejected},
		{name: "bot challenge", outcome: OutcomeBotChallenge, want: ErrBotChallenge},
		{name: "rate limited", outcome: OutcomeRateLimited, want: ErrRateLimited},
		{name: "temporary", outcome: OutcomeTemporaryFailure, want: ErrTemporary},
		{name: "unknown", outcome: OutcomeUnknown, want: ErrUnknownResponse},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := &Error{
				Op:       OpMobileLogin,
				Endpoint: EndpointMobileLogin,
				Status:   http.StatusOK,
				Outcome:  tc.outcome,
			}
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
	err := &Error{
		Op:       OpMobileLogin,
		Endpoint: EndpointMobileLogin,
		Outcome:  OutcomeTemporaryFailure,
		Err:      cause,
	}

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

func TestErrorMessageRendersLabelsAndStatus(t *testing.T) {
	t.Parallel()

	err := &Error{
		Op:         OpVerifyMFA,
		Endpoint:   EndpointMobileMFAVerifyCode,
		Status:     http.StatusTooManyRequests,
		Outcome:    OutcomeRateLimited,
		RetryAfter: 30 * time.Second,
	}

	msg := err.Error()
	for _, want := range []string{
		string(OpVerifyMFA), string(EndpointMobileMFAVerifyCode), "429", "rate_limited", "30s",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message %q missing %q", msg, want)
		}
	}
}

// A *url.Error carries the full request URL. On the CAS flow that URL holds the
// service ticket, and the nested cause may hold header material.
func TestErrorMessageRedactsWrappedURLError(t *testing.T) {
	t.Parallel()

	cause := &url.Error{
		Op:  testHTTPVerbPost,
		URL: testSSOEmbedURL + "?ticket=ST-secret-0001&clientId=GCM_IOS_DARK",
		Err: errors.New(testHeaderCookie + ": " + testCookieName + "=abc123; " +
			testHeaderAuthorization + ": Bearer super-secret"),
	}
	err := &Error{
		Op:       OpExchangeServiceTicket,
		Endpoint: EndpointDIToken,
		Status:   http.StatusBadGateway,
		Outcome:  OutcomeTemporaryFailure,
		Err:      cause,
	}

	msg := err.Error()
	forbidden := []string{
		"ST-secret-0001", testCookieName, "abc123", testHeaderAuthorization, "Bearer", "super-secret", testHeaderCookie,
	}
	for _, bad := range forbidden {
		if strings.Contains(msg, bad) {
			t.Fatalf("error message %q leaked %q", msg, bad)
		}
	}
	for _, want := range []string{testSSOEmbedURL, "redacted"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message %q missing %q", msg, want)
		}
	}

	// Unwrap must still reach the real cause for errors.Is/errors.As.
	var target *url.Error
	if !errors.As(err, &target) || target.URL != cause.URL {
		t.Fatal("errors.As must still reach the wrapped *url.Error")
	}
}

// Free-form text in a cause is never rendered: only recognized error shapes get
// a rendered form, everything else degrades to its Go type name.
func TestErrorMessageDoesNotRenderArbitraryCauseText(t *testing.T) {
	t.Parallel()

	cause := errors.New("garmin said: " + testHeaderSetCookie + ": JWT_WEB=eyJhbGciOi.secret")
	err := &Error{
		Op:       OpPortalLogin,
		Endpoint: EndpointPortalLogin,
		Outcome:  OutcomeTemporaryFailure,
		Err:      cause,
	}

	msg := err.Error()
	for _, bad := range []string{testHeaderSetCookie, "JWT_WEB", "eyJhbGciOi.secret", "garmin said"} {
		if strings.Contains(msg, bad) {
			t.Fatalf("error message %q leaked %q", msg, bad)
		}
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is must still reach the wrapped cause")
	}
}

func TestErrorMessageRendersRecognizedCauses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cause error
		want  string
	}{
		{name: "deadline", cause: context.DeadlineExceeded, want: "deadline exceeded"},
		{name: "canceled", cause: context.Canceled, want: "canceled"},
		{name: "package sentinel", cause: ErrRateLimited, want: ErrRateLimited.Error()},
		{
			name:  "nested protocol error",
			cause: &Error{Op: OpValidateSession, Endpoint: EndpointSocialProfile, Outcome: OutcomeSessionRejected},
			want:  string(EndpointSocialProfile),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := &Error{Op: OpMobileLogin, Endpoint: EndpointMobileLogin, Outcome: OutcomeUnknown, Err: tc.cause}
			if msg := err.Error(); !strings.Contains(msg, tc.want) {
				t.Fatalf("error message %q missing %q", msg, tc.want)
			}
		})
	}
}

// Op and Endpoint are typed labels; a value that is not a package constant is
// rendered as "unknown" instead of being echoed.
func TestErrorMessageRejectsFreeFormLabels(t *testing.T) {
	t.Parallel()

	err := &Error{
		Op:       Op(testSSOEmbedURL + "?ticket=ST-secret-0002"),
		Endpoint: Endpoint(testHeaderCookie + ": " + testCookieName + "=abc123"),
		Outcome:  OutcomeUnknown,
	}

	msg := err.Error()
	for _, bad := range []string{"ST-secret-0002", testCookieName, "abc123", "://", "?"} {
		if strings.Contains(msg, bad) {
			t.Fatalf("error message %q leaked %q", msg, bad)
		}
	}
	if strings.Count(msg, labelUnknown) < 2 {
		t.Fatalf("error message %q must render both labels as %q", msg, labelUnknown)
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
