package auth

import (
	"errors"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// Package-level failures. Each is comparable with errors.Is and renders no
// credential, cookie, body or URL. Wire verdicts keep their protocol sentinels
// (protocol.ErrInvalidCredentials and friends), so a caller matches those
// directly.
var (
	// ErrLoginExhausted reports that every strategy in the fallback chain failed
	// without a definitive verdict. The last cause is wrapped.
	ErrLoginExhausted = errors.New("garmin auth: every login strategy failed")
	// ErrMissingCredentials reports an empty email or password.
	ErrMissingCredentials = errors.New("garmin auth: email and password are required")
	// ErrMissingMFACode reports an empty one-time code.
	ErrMissingMFACode = errors.New("garmin auth: an MFA code is required")
	// ErrMissingPrincipal reports an empty principal.
	ErrMissingPrincipal = errors.New("garmin auth: principal is required")
	// ErrNotConfigured reports an Authenticator or Refresher configuration that
	// cannot be used: no transport, no store, or unusable hosts.
	ErrNotConfigured = errors.New("garmin auth: incomplete configuration")
	// ErrNoRefreshToken reports a stored set that cannot be refreshed because it
	// carries no refresh token or no client id.
	ErrNoRefreshToken = errors.New("garmin auth: no DI refresh token available")
	// ErrTokenExchangeFailed reports that no candidate DI client id accepted the
	// service ticket.
	ErrTokenExchangeFailed = errors.New("garmin auth: DI token exchange failed for every candidate client id")
	// ErrMalformedTokenResponse reports a DI token response that carried no
	// usable access token.
	ErrMalformedTokenResponse = errors.New("garmin auth: malformed DI token response")
	// ErrMissingServiceTicket reports a success verdict with no CAS service
	// ticket to exchange.
	ErrMissingServiceTicket = errors.New("garmin auth: login succeeded without a service ticket")
	// ErrMissingCSRFToken reports a widget page with no usable _csrf value.
	ErrMissingCSRFToken = errors.New("garmin auth: widget page carried no CSRF token")
	// ErrNotIdempotent reports a request that may not be replayed after a 401.
	ErrNotIdempotent = errors.New("garmin auth: request is not safe to retry")
)

// transportError wraps a transport-level failure in a sanitized protocol error,
// labeled with the operation and endpoint it happened on. The cause is wrapped
// but never rendered verbatim: protocol.Error redacts it.
func transportError(op protocol.Op, endpoint protocol.Endpoint, cause error) error {
	return &protocol.Error{
		Op:       op,
		Endpoint: endpoint,
		Outcome:  protocol.OutcomeTemporaryFailure,
		Err:      cause,
	}
}

// statusError reports a response whose status alone decides the outcome, used for
// the bare page GETs that carry nothing to classify. It returns nil for 2xx.
func statusError(op protocol.Op, endpoint protocol.Endpoint, resp protocol.Response) error {
	outcome := pageOutcome(resp.Status())
	if outcome == protocol.OutcomeSuccess {
		return nil
	}
	return &protocol.Error{
		Op:       op,
		Endpoint: endpoint,
		Status:   resp.Status(),
		Outcome:  outcome,
	}
}

// pageOutcome maps the status of a plain page fetch onto a login outcome, using
// the same distinctions the classifier makes for a login POST. Source: the
// status_code checks upstream performs on the widget embed and portal sign-in
// GETs, which fall through to the next strategy rather than blaming the password.
func pageOutcome(status int) protocol.Outcome {
	switch {
	case status >= 200 && status < 300:
		return protocol.OutcomeSuccess
	case status == http.StatusTooManyRequests:
		return protocol.OutcomeRateLimited
	case status == http.StatusForbidden:
		return protocol.OutcomeBotChallenge
	case status >= 500:
		return protocol.OutcomeTemporaryFailure
	default:
		return protocol.OutcomeUnknown
	}
}
