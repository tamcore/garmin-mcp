package client_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

func TestAPIErrorRendersOnlySanitizedLabels(t *testing.T) {
	t.Parallel()

	err := &client.APIError{
		Op:         client.OpGetDailySleep,
		Endpoint:   client.EndpointDailySleep,
		Status:     http.StatusTooManyRequests,
		Kind:       client.KindRateLimited,
		RetryAfter: 3 * time.Second,
	}

	rendered := err.Error()
	for _, want := range []string{
		client.OpGetDailySleep.String(),
		client.EndpointDailySleep.String(),
		client.KindRateLimited.String(),
		"429",
		"3s",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("Error() = %q, want it to contain %q", rendered, want)
		}
	}
}

func TestAPIErrorMatchesItsKindSentinel(t *testing.T) {
	t.Parallel()

	cases := map[client.Kind]error{
		client.KindNotFound:            client.ErrNotFound,
		client.KindAuthentication:      client.ErrAuthentication,
		client.KindRateLimited:         client.ErrRateLimited,
		client.KindInvalidFile:         client.ErrInvalidFile,
		client.KindValidation:          client.ErrValidation,
		client.KindTemporaryConnection: client.ErrTemporaryConnection,
		client.KindServer:              client.ErrServer,
		client.KindMalformedPayload:    client.ErrMalformedPayload,
		client.KindUnknown:             client.ErrUnexpectedResponse,
	}

	for kind, sentinel := range cases {
		err := error(&client.APIError{Op: client.OpListDevices, Endpoint: client.EndpointDevices, Kind: kind})
		if !errors.Is(err, sentinel) {
			t.Errorf("kind %v does not match its sentinel %v", kind, sentinel)
		}
		for otherKind, other := range cases {
			if otherKind == kind {
				continue
			}
			if errors.Is(err, other) {
				t.Errorf("kind %v also matches the unrelated sentinel %v", kind, other)
			}
		}
	}
}

func TestAPIErrorIsAndAsWorkThroughWrapping(t *testing.T) {
	t.Parallel()

	cause := protocol.ErrTemporary
	err := fmt.Errorf("outer: %w", &client.APIError{
		Op:       client.OpGetSocialProfile,
		Endpoint: client.EndpointSocialProfile,
		Kind:     client.KindTemporaryConnection,
		Err:      cause,
	})

	var target *client.APIError
	if !errors.As(err, &target) {
		t.Fatal("errors.As did not find the *APIError")
	}
	if target.Op != client.OpGetSocialProfile {
		t.Errorf("Op = %q, want %q", target.Op, client.OpGetSocialProfile)
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is did not reach the wrapped cause")
	}
	if !errors.Is(err, client.ErrTemporaryConnection) {
		t.Error("errors.Is did not match the kind sentinel through the wrapper")
	}
}

// TestAPIErrorNeverRendersSecretMaterial is the leak test the brief demands: no
// token, cookie, body or coordinate may appear, no matter what the cause holds.
func TestAPIErrorNeverRendersSecretMaterial(t *testing.T) {
	t.Parallel()

	const (
		token       = "SENTINEL-BEARER-a91f"
		cookie      = "SESSIONID=SENTINEL-COOKIE-33b1"
		body        = `{"sleepStartTimestampGMT":1700000000000,"heartRate":58}`
		coordinates = "48.137154,11.576124"
	)

	causes := []error{
		errors.New("Authorization: Bearer " + token),
		fmt.Errorf("set-cookie %s", cookie),
		errors.New(body),
		fmt.Errorf("gps fix at %s", coordinates),
		&client.APIError{
			Op:       client.OpGetDailySleep,
			Endpoint: client.EndpointDailySleep,
			Kind:     client.KindServer,
			Err:      errors.New("nested " + token),
		},
	}

	for _, cause := range causes {
		err := &client.APIError{
			Op:       client.OpGetDailySleep,
			Endpoint: client.EndpointDailySleep,
			Status:   http.StatusInternalServerError,
			Kind:     client.KindServer,
			Err:      cause,
		}
		rendered := err.Error()
		for _, needle := range []string{token, cookie, body, coordinates, "heartRate", "48.137"} {
			if strings.Contains(rendered, needle) {
				t.Errorf("Error() = %q leaks %q", rendered, needle)
			}
		}
	}
}

func TestAPIErrorRendersRecognizedCauses(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		cause error
		want  string
	}{
		"context deadline": {cause: context.DeadlineExceeded, want: context.DeadlineExceeded.Error()},
		"context cancel":   {cause: context.Canceled, want: context.Canceled.Error()},
		"own sentinel":     {cause: client.ErrResponseTooLarge, want: client.ErrResponseTooLarge.Error()},
		"protocol error": {
			cause: &protocol.Error{
				Op:       protocol.OpValidateSession,
				Endpoint: protocol.EndpointSocialProfile,
				Outcome:  protocol.OutcomeSessionRejected,
			},
			want: protocol.OutcomeSessionRejected.String(),
		},
		// A bare protocol sentinel, not wrapped in a *protocol.Error, must still
		// render its own text rather than degrading to an opaque Go type name.
		"bare mfa rejected sentinel": {
			cause: protocol.ErrMFARejected,
			want:  protocol.ErrMFARejected.Error(),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := &client.APIError{
				Op:       client.OpListDevices,
				Endpoint: client.EndpointDevices,
				Kind:     client.KindTemporaryConnection,
				Err:      tc.cause,
			}
			if got := err.Error(); !strings.Contains(got, tc.want) {
				t.Errorf("Error() = %q, want it to describe the cause as %q", got, tc.want)
			}
		})
	}
}

func TestAPIErrorRetryability(t *testing.T) {
	t.Parallel()

	retryable := []client.Kind{client.KindTemporaryConnection, client.KindServer}
	for _, kind := range retryable {
		err := &client.APIError{Kind: kind}
		if !err.Retryable() {
			t.Errorf("kind %v must be retryable", kind)
		}
	}

	never := []client.Kind{
		client.KindNotFound, client.KindAuthentication, client.KindRateLimited,
		client.KindInvalidFile, client.KindValidation, client.KindMalformedPayload,
		client.KindUnknown,
	}
	for _, kind := range never {
		err := &client.APIError{Kind: kind}
		if err.Retryable() {
			t.Errorf("kind %v must not be retryable on its own", kind)
		}
	}
}

func TestNilAPIErrorIsInert(t *testing.T) {
	t.Parallel()

	var err *client.APIError
	if got := err.Error(); got == "" {
		t.Error("Error() on a nil *APIError must still render something")
	}
	if err.Retryable() {
		t.Error("a nil *APIError must not be retryable")
	}
	if err.Unwrap() != nil {
		t.Error("a nil *APIError must unwrap to nil")
	}
}
