package protocol

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors for errors.Is checks. They carry no request detail.
var (
	ErrMFARequired        = errors.New("garmin: multi-factor authentication required")
	ErrInvalidCredentials = errors.New("garmin: invalid credentials")
	ErrAccountLocked      = errors.New("garmin: account locked")
	ErrAccountRestricted  = errors.New("garmin: account not permitted to use this login flow")
	ErrSessionRejected    = errors.New("garmin: session rejected by the API tier")
	ErrBotChallenge       = errors.New("garmin: bot challenge or forbidden")
	ErrRateLimited        = errors.New("garmin: rate limited")
	ErrTemporary          = errors.New("garmin: temporary failure")
	ErrUnknownResponse    = errors.New("garmin: unrecognized response")
)

// sentinels is the set this package renders verbatim inside a redacted cause,
// because the package authored every one of these strings.
var sentinels = [...]error{
	ErrMFARequired, ErrInvalidCredentials, ErrAccountLocked, ErrAccountRestricted,
	ErrSessionRejected, ErrBotChallenge, ErrRateLimited, ErrTemporary, ErrUnknownResponse,
	ErrUnsupportedDomain,
}

// Error is the structured protocol failure. It deliberately holds no body,
// header, cookie, token or credential material: only sanitized Op and Endpoint
// labels, the HTTP status, the classified outcome, any parsed Retry-After hint,
// and a wrapped cause.
//
// The rendered message never contains raw cause text. Only recognized error
// shapes are described (a *url.Error with its query redacted, a context error, a
// nested *Error, one of this package's sentinels); anything else degrades to its
// Go type name. Unwrap still exposes the real cause, so a caller that needs its
// text must fetch and redact it deliberately.
type Error struct {
	// Op names the logical operation. Only an Op* constant is rendered.
	Op Op
	// Endpoint is the sanitized endpoint label. Only an Endpoint* constant is
	// rendered: never a URL with a query string.
	Endpoint Endpoint
	// Status is the HTTP status code, or 0 when no response was received.
	Status int
	// Outcome is the classified meaning of the response.
	Outcome Outcome
	// RetryAfter is the parsed Retry-After hint, or 0 when absent.
	RetryAfter time.Duration
	// Err is the wrapped cause, if any. It is never rendered verbatim.
	Err error
}

// Error renders a sanitized, single-line message.
func (e *Error) Error() string {
	if e == nil {
		return "protocol: <nil>"
	}

	var b strings.Builder
	b.WriteString("garmin ")
	b.WriteString(e.Op.String())
	b.WriteString(" [")
	b.WriteString(e.Endpoint.String())
	b.WriteString("]: ")
	b.WriteString(e.Outcome.String())
	if e.Status != 0 {
		b.WriteString(" (status ")
		b.WriteString(strconv.Itoa(e.Status))
		b.WriteString(")")
	}
	if e.RetryAfter > 0 {
		b.WriteString(" retry after ")
		b.WriteString(e.RetryAfter.String())
	}
	if cause := redactedCause(e.Err); cause != "" {
		b.WriteString(": ")
		b.WriteString(cause)
	}
	return b.String()
}

// Unwrap exposes the wrapped cause, so errors.Is and errors.As keep working.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is matches the sentinel that corresponds to the classified outcome.
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	return target == sentinelFor(e.Outcome)
}

// Retryable reports whether the failed request may be retried.
func (e *Error) Retryable() bool {
	if e == nil {
		return false
	}
	return e.Outcome.Retryable()
}

func sentinelFor(o Outcome) error {
	switch o {
	case OutcomeMFARequired:
		return ErrMFARequired
	case OutcomeInvalidCredentials:
		return ErrInvalidCredentials
	case OutcomeAccountLocked:
		return ErrAccountLocked
	case OutcomeAccountRestricted:
		return ErrAccountRestricted
	case OutcomeSessionRejected:
		return ErrSessionRejected
	case OutcomeBotChallenge:
		return ErrBotChallenge
	case OutcomeRateLimited:
		return ErrRateLimited
	case OutcomeTemporaryFailure:
		return ErrTemporary
	default:
		return ErrUnknownResponse
	}
}

// ParseRetryAfter interprets a Retry-After header value relative to now. It
// accepts both RFC 7231 forms, delta-seconds and an HTTP-date, and clamps past
// or negative values to zero. The second result reports whether the value was
// understood.
func ParseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}

	if secs, err := strconv.Atoi(trimmed); err == nil {
		if secs <= 0 {
			return 0, true
		}
		return time.Duration(secs) * time.Second, true
	}

	deadline, err := http.ParseTime(trimmed)
	if err != nil {
		return 0, false
	}
	if delay := deadline.Sub(now); delay > 0 {
		return delay, true
	}
	return 0, true
}
