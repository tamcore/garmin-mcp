package client

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// Sentinel errors for errors.Is checks. Each one names a failure class and
// carries no request detail.
//
// The classes upstream keeps apart stay apart here: a missing resource, a
// rejected session, a rate limit, a refused upload, a validated-away request and
// a transport hiccup are five different remediations, and collapsing them was the
// defect behind garmin_mcp issue #109.
var (
	// ErrNotFound reports HTTP 404: the resource does not exist.
	ErrNotFound = errors.New("garmin api: resource not found")
	// ErrAuthentication reports a rejected session (401, 403, or a
	// privacy-protected payload). The token must be refreshed or the login redone.
	ErrAuthentication = errors.New("garmin api: request not authenticated")
	// ErrRateLimited reports HTTP 429. Honor RetryAfter before trying again.
	ErrRateLimited = errors.New("garmin api: rate limited")
	// ErrInvalidFile reports an upload or download Garmin refused as unusable
	// (HTTP 415, or 400 on a file endpoint).
	ErrInvalidFile = errors.New("garmin api: invalid file")
	// ErrValidation reports a request Garmin or this package rejected as invalid
	// before it could have any effect.
	ErrValidation = errors.New("garmin api: invalid request")
	// ErrTemporaryConnection reports a transport failure: DNS, dial, TLS, reset,
	// or a deadline reached below the request timeout.
	ErrTemporaryConnection = errors.New("garmin api: temporary connection failure")
	// ErrServer reports a 5xx response.
	ErrServer = errors.New("garmin api: server error")
	// ErrMalformedPayload reports a response body that could not be decoded at
	// all. An unknown field never produces this: tolerant decoding ignores it.
	ErrMalformedPayload = errors.New("garmin api: malformed response payload")
	// ErrUnexpectedResponse reports a response that matched no known class.
	ErrUnexpectedResponse = errors.New("garmin api: unexpected response")
	// ErrResponseTooLarge reports a body, or a decompressed body, over its bound.
	ErrResponseTooLarge = errors.New("garmin api: response exceeds its size bound")
	// ErrInvalidLimits reports a Limits value that cannot be used.
	ErrInvalidLimits = errors.New("garmin api: invalid limits")
	// ErrNotConfigured reports a Client configuration that cannot be used.
	ErrNotConfigured = errors.New("garmin api: incomplete configuration")
	// ErrMissingPrincipal reports a Session with no principal.
	ErrMissingPrincipal = errors.New("garmin api: principal is required")
	// ErrPaginationExhausted reports a paginated read that hit its page bound
	// without the server ever returning a short or empty page. Source: the
	// MAX_PAGINATED_REQUESTS guard in get_activities_by_date, which fails loudly
	// rather than truncating.
	ErrPaginationExhausted = errors.New("garmin api: pagination exceeded its page bound")
)

// sentinels is the set this package renders verbatim inside a redacted cause,
// because the package authored every one of these strings.
var sentinels = [...]error{
	ErrNotFound, ErrAuthentication, ErrRateLimited, ErrInvalidFile, ErrValidation,
	ErrTemporaryConnection, ErrServer, ErrMalformedPayload, ErrUnexpectedResponse,
	ErrResponseTooLarge, ErrInvalidLimits, ErrNotConfigured, ErrMissingPrincipal,
	ErrPaginationExhausted,
}

// protocolSentinels are the login-layer sentinels this package may render, for a
// cause that arrives as a bare sentinel rather than inside a *protocol.Error.
var protocolSentinels = [...]error{
	protocol.ErrMFARequired, protocol.ErrInvalidCredentials, protocol.ErrAccountLocked,
	protocol.ErrAccountRestricted, protocol.ErrSessionRejected, protocol.ErrBotChallenge,
	protocol.ErrRateLimited, protocol.ErrTemporary, protocol.ErrUnknownResponse,
}

// Kind is the classified failure class of one API call.
type Kind int

const (
	// KindUnknown means the response matched no known class.
	KindUnknown Kind = iota
	// KindNotFound is HTTP 404.
	KindNotFound
	// KindAuthentication is a rejected session.
	KindAuthentication
	// KindRateLimited is HTTP 429.
	KindRateLimited
	// KindInvalidFile is a refused file payload.
	KindInvalidFile
	// KindValidation is a rejected request.
	KindValidation
	// KindTemporaryConnection is a transport failure.
	KindTemporaryConnection
	// KindServer is a 5xx response.
	KindServer
	// KindMalformedPayload is an undecodable body.
	KindMalformedPayload
)

// String returns a stable snake_case label safe for logs and metrics.
func (k Kind) String() string {
	switch k {
	case KindUnknown:
		return labelUnknown
	case KindNotFound:
		return "not_found"
	case KindAuthentication:
		return "authentication"
	case KindRateLimited:
		return "rate_limited"
	case KindInvalidFile:
		return "invalid_file"
	case KindValidation:
		return "validation"
	case KindTemporaryConnection:
		return "temporary_connection"
	case KindServer:
		return "server_error"
	case KindMalformedPayload:
		return "malformed_payload"
	default:
		return "invalid_kind(" + strconv.Itoa(int(k)) + ")"
	}
}

// Retryable reports whether this class of failure may be retried at all. It is
// the class-level half of the decision: the retry predicate also requires the
// request itself to be safe to repeat.
func (k Kind) Retryable() bool {
	return k == KindTemporaryConnection || k == KindServer
}

// sentinelFor maps a kind onto the sentinel a caller matches with errors.Is.
func sentinelFor(k Kind) error {
	switch k {
	case KindNotFound:
		return ErrNotFound
	case KindAuthentication:
		return ErrAuthentication
	case KindRateLimited:
		return ErrRateLimited
	case KindInvalidFile:
		return ErrInvalidFile
	case KindValidation:
		return ErrValidation
	case KindTemporaryConnection:
		return ErrTemporaryConnection
	case KindServer:
		return ErrServer
	case KindMalformedPayload:
		return ErrMalformedPayload
	default:
		return ErrUnexpectedResponse
	}
}

// APIError is the structured API failure.
//
// It holds no body, header, cookie, token, credential or coordinate: only the
// sanitized Op and Endpoint labels, the HTTP status, the classified kind, any
// parsed Retry-After, and a wrapped cause.
//
// The rendered message never contains raw cause text, and never the text of a
// wrapper either, because fmt.Errorf lets a caller put a bearer token or a health
// payload in the wrapping message. Only recognized shapes are described: this
// package's sentinels, a nested *APIError, a *protocol.Error — which redacts
// itself — and a context error. Anything else degrades to its Go type name.
// Unwrap still exposes the real cause, so a caller that needs its text must fetch
// and redact it deliberately.
type APIError struct {
	// Op names the logical operation. Only a recognized label is rendered.
	Op Op
	// Endpoint is the sanitized endpoint label: never a URL with a query.
	Endpoint Endpoint
	// Status is the HTTP status code, or 0 when no response was received.
	Status int
	// Kind is the classified failure class.
	Kind Kind
	// RetryAfter is the parsed Retry-After hint, or 0 when absent.
	RetryAfter time.Duration
	// Err is the wrapped cause, if any. It is never rendered verbatim.
	Err error
}

// Error renders a sanitized, single-line message.
func (e *APIError) Error() string {
	return e.render(maxCauseDepth)
}

// render is Error with an explicit remaining cause depth, so a cause chain that
// loops back to this error terminates.
func (e *APIError) render(depth int) string {
	if e == nil {
		return "garmin api: <nil>"
	}

	var b strings.Builder
	b.WriteString("garmin api ")
	b.WriteString(e.Op.String())
	b.WriteString(" [")
	b.WriteString(e.Endpoint.String())
	b.WriteString("]: ")
	b.WriteString(e.Kind.String())
	if e.Status != 0 {
		b.WriteString(" (status ")
		b.WriteString(strconv.Itoa(e.Status))
		b.WriteString(")")
	}
	if e.RetryAfter > 0 {
		b.WriteString(" retry after ")
		b.WriteString(e.RetryAfter.String())
	}
	if cause := redactedCauseAt(e.Err, depth); cause != "" {
		b.WriteString(": ")
		b.WriteString(cause)
	}
	return b.String()
}

// Unwrap exposes the wrapped cause, so errors.Is and errors.As keep working.
func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is matches the sentinel that corresponds to the classified kind.
func (e *APIError) Is(target error) bool {
	if e == nil {
		return false
	}
	return target == sentinelFor(e.Kind)
}

// Retryable reports whether this failure class may be retried. It says nothing
// about whether the request itself is safe to repeat.
func (e *APIError) Retryable() bool {
	if e == nil {
		return false
	}
	return e.Kind.Retryable()
}

// maxCauseDepth bounds how far a cause chain is walked while rendering.
const maxCauseDepth = 8

// redactedCauseAt renders a description of err, or "" for no cause. Only
// recognized shapes are described; everything else degrades to a type name, so
// no attacker-influenced or secret-bearing text can reach the message.
func redactedCauseAt(err error, depth int) string {
	if err == nil || depth <= 0 {
		return ""
	}

	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded.Error()
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.render(depth - 1)
	}

	var protoErr *protocol.Error
	if errors.As(err, &protoErr) {
		// protocol.Error redacts its own message, so its text is safe verbatim.
		return protoErr.Error()
	}

	if text, ok := sentinelText(err); ok {
		return text
	}
	return causeTypeName(err)
}

// sentinelText reports the text of the first sentinel in err's chain, whether
// this package's or protocol's. Those strings are authored here, not received.
func sentinelText(err error) (string, bool) {
	for _, sentinel := range sentinels {
		if errors.Is(err, sentinel) {
			return sentinel.Error(), true
		}
	}
	for _, sentinel := range protocolSentinels {
		if errors.Is(err, sentinel) {
			return sentinel.Error(), true
		}
	}
	return "", false
}

// causeTypeName names the Go type of an unrecognized cause. A type name is
// authored by a package, never by Garmin, so it cannot carry payload material.
func causeTypeName(err error) string {
	return "cause of type " + fmt.Sprintf("%T", err)
}
