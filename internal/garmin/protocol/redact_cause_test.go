package protocol

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

// testInternalHost is a host name a DNS failure carries. It names internal
// topology, so it must never be rendered.
const testInternalHost = "secret-internal.example"

// secretMaterial is the set of strings that must never survive redaction.
var secretMaterial = [...]string{
	"sk-secret123", "Bearer", "SESSIONID", "abc123def", "hunter2",
	"ST-secret-9", testInternalHost, "10.0.0.5", "leaky.example",
}

func assertNoCauseSecrets(t *testing.T, rendered string) {
	t.Helper()
	for _, secret := range secretMaterial {
		if strings.Contains(rendered, secret) {
			t.Fatalf("rendered %q leaked %q", rendered, secret)
		}
	}
}

func ticketURLError(cause error) *url.Error {
	return &url.Error{
		Op:  "Post",
		URL: "https://sso.garmin.com/sso/embed?ticket=ST-secret-9&webhost=leaky.example",
		Err: cause,
	}
}

func TestRedactedCauseNeverRendersWrapperText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		wantContains string
	}{
		{
			name:         "bearer token wrapping a sentinel",
			err:          fmt.Errorf("Authorization: Bearer sk-secret123: %w", ErrTemporary),
			wantContains: "garmin: temporary failure",
		},
		{
			name:         "cookie wrapping a sentinel",
			err:          fmt.Errorf("Cookie: SESSIONID=abc123def: %w", ErrSessionRejected),
			wantContains: "garmin: session rejected",
		},
		{
			name:         "password wrapping a sentinel",
			err:          fmt.Errorf("form password=hunter2: %w", ErrInvalidCredentials),
			wantContains: "garmin: invalid credentials",
		},
		{
			name:         "url error carrying a ticket wrapping a sentinel",
			err:          ticketURLError(ErrTemporary),
			wantContains: "garmin: temporary failure",
		},
		{
			name:         "url error wrapped in a secret bearing message",
			err:          fmt.Errorf("Bearer sk-secret123: %w", ticketURLError(ErrRateLimited)),
			wantContains: "garmin: rate limited",
		},
		{
			name: "three layers of wrapping around a url error",
			err: fmt.Errorf("outer Bearer sk-secret123: %w",
				fmt.Errorf("middle Cookie: SESSIONID=abc123def: %w",
					fmt.Errorf("inner password=hunter2: %w", ticketURLError(ErrBotChallenge)))),
			wantContains: "garmin: bot challenge",
		},
		{
			name:         "context deadline wrapped in a secret bearing message",
			err:          fmt.Errorf("Bearer sk-secret123: %w", context.DeadlineExceeded),
			wantContains: "context deadline exceeded",
		},
		{
			name:         "context cancellation wrapped in a secret bearing message",
			err:          fmt.Errorf("Cookie: SESSIONID=abc123def: %w", context.Canceled),
			wantContains: "context canceled",
		},
		{
			name:         "unsupported domain sentinel wrapped in a secret bearing message",
			err:          fmt.Errorf("host leaky.example rejected: %w", ErrUnsupportedDomain),
			wantContains: "garmin: unsupported domain",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := redactedCause(tc.err)
			assertNoCauseSecrets(t, got)
			if !strings.Contains(got, tc.wantContains) {
				t.Fatalf("redactedCause() = %q, want it to contain %q", got, tc.wantContains)
			}
		})
	}
}

func TestRedactedCauseRedactsTicketBearingURL(t *testing.T) {
	t.Parallel()

	got := redactedCause(ticketURLError(ErrTemporary))
	assertNoCauseSecrets(t, got)
	for _, want := range []string{"Post", "https://sso.garmin.com/sso/embed", "ticket=" + redactedValue} {
		if !strings.Contains(got, want) {
			t.Fatalf("redactedCause() = %q, want it to contain %q", got, want)
		}
	}
}

func TestErrorMessageNeverRendersWrapperText(t *testing.T) {
	t.Parallel()

	err := &Error{
		Op:       OpMobileLogin,
		Endpoint: EndpointMobileLogin,
		Status:   502,
		Outcome:  OutcomeTemporaryFailure,
		Err:      fmt.Errorf("Bearer sk-secret123: %w", ticketURLError(ErrTemporary)),
	}

	got := err.Error()
	assertNoCauseSecrets(t, got)
	if !strings.Contains(got, "garmin: temporary failure") {
		t.Fatalf("Error() = %q, want it to name the sentinel outcome", got)
	}
}

func TestRedactedCauseNestedProtocolError(t *testing.T) {
	t.Parallel()

	inner := &Error{
		Op:       OpMobileLogin,
		Endpoint: EndpointMobileLogin,
		Outcome:  OutcomeTemporaryFailure,
		Err:      ticketURLError(errors.New("Bearer sk-secret123")),
	}

	got := redactedCause(fmt.Errorf("Cookie: SESSIONID=abc123def: %w", inner))
	assertNoCauseSecrets(t, got)
	if !strings.Contains(got, "garmin ") {
		t.Fatalf("redactedCause() = %q, want the nested protocol error rendering", got)
	}
}

// timeoutError is a net.Error that reports a timeout with a secret-bearing text.
type timeoutError struct{}

func (timeoutError) Error() string   { return "dial tcp 10.0.0.5:443: i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestRedactedCauseNamesNetworkFailureCategory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "dns name not found",
			err:  &net.DNSError{Err: "no such host", Name: testInternalHost, IsNotFound: true},
			want: "dns name not found",
		},
		{
			name: "dns timeout",
			err:  &net.DNSError{Err: "timeout", Name: testInternalHost, IsTimeout: true},
			want: "dns timeout",
		},
		{
			name: "dns failure",
			err:  &net.DNSError{Err: "server misbehaving", Name: testInternalHost},
			want: "dns failure",
		},
		{
			name: "tls certificate verification",
			err:  &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
			want: "tls certificate verification failure",
		},
		{
			name: "tls hostname mismatch",
			err:  x509.HostnameError{Host: "leaky.example"},
			want: "tls certificate verification failure",
		},
		{
			name: "tls record header",
			err:  tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"},
			want: "tls handshake failure",
		},
		{
			name: "tls alert",
			err:  tls.AlertError(42),
			want: "tls handshake failure",
		},
		{
			name: "connection refused",
			err:  dialError(syscall.ECONNREFUSED),
			want: "connection refused",
		},
		{
			name: "connection reset",
			err:  dialError(syscall.ECONNRESET),
			want: "connection reset",
		},
		{
			name: "host unreachable",
			err:  dialError(syscall.EHOSTUNREACH),
			want: "network unreachable",
		},
		{
			name: "timeout",
			err:  timeoutError{},
			want: "network timeout",
		},
		{
			name: "generic network operation",
			err:  dialError(errors.New("Bearer sk-secret123")),
			want: "network failure",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := redactedCause(tc.err)
			assertNoCauseSecrets(t, got)
			if got != tc.want {
				t.Fatalf("redactedCause() = %q, want %q", got, tc.want)
			}
		})
	}
}

func dialError(cause error) error {
	return &net.OpError{
		Op:   "dial",
		Net:  "tcp",
		Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 443},
		Err:  cause,
	}
}

func TestRedactedCauseNetworkCategoryInsideURLError(t *testing.T) {
	t.Parallel()

	got := redactedCause(ticketURLError(dialError(syscall.ECONNREFUSED)))
	assertNoCauseSecrets(t, got)
	if !strings.Contains(got, "connection refused") {
		t.Fatalf("redactedCause() = %q, want it to name the connection failure", got)
	}
}

func TestRedactedCauseUnknownShapeReportsTypeOnly(t *testing.T) {
	t.Parallel()

	got := redactedCause(errors.New("Cookie: SESSIONID=abc123def"))
	assertNoCauseSecrets(t, got)
	if !strings.HasPrefix(got, "cause of type ") {
		t.Fatalf("redactedCause() = %q, want a bare type name", got)
	}
}

func TestRedactedCauseTerminatesOnSelfReferentialError(t *testing.T) {
	t.Parallel()

	// A cause chain that points back at itself must not recurse forever.
	self := &Error{Op: OpMobileLogin, Endpoint: EndpointMobileLogin, Outcome: OutcomeUnknown}
	self.Err = self

	done := make(chan string, 1)
	go func() { done <- self.Error() }()

	select {
	case rendered := <-done:
		assertNoCauseSecrets(t, rendered)
	case <-time.After(5 * time.Second):
		t.Fatal("Error() did not terminate on a self-referential cause")
	}
}
