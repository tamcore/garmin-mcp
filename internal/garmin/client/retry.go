package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// retryableServerStatuses is the "selected 5xx" set. A 5xx outside it — a 501 Not
// Implemented, a 505 Version Not Supported — describes a request Garmin will
// refuse again, so repeating it only spends the account's rate budget.
var retryableServerStatuses = [...]int{
	http.StatusInternalServerError,
	http.StatusBadGateway,
	http.StatusServiceUnavailable,
	http.StatusGatewayTimeout,
	http.StatusInsufficientStorage,
}

// doWithRetry performs req, retrying only what the predicate allows.
func (c *Client) doWithRetry(ctx context.Context, session Session, req Request) (Payload, error) {
	var lastPayload Payload
	var lastErr error

	for attempt := range c.limits.MaxAttempts {
		payload, err := c.attempt(ctx, session, req)
		if err == nil {
			return payload, nil
		}
		lastPayload, lastErr = payload, err

		delay, ok := c.retryDelay(req, attempt, err)
		if !ok {
			return lastPayload, lastErr
		}
		if sleepErr := c.sleeper.Sleep(ctx, delay); sleepErr != nil {
			return lastPayload, c.fail(req, 0, KindTemporaryConnection, 0, sleepErr)
		}
	}
	return lastPayload, lastErr
}

// retryDelay reports how long to wait before repeating req, and whether repeating
// it is permitted at all.
//
// The prohibitions are explicit and checked before anything else:
//
//   - a credential or MFA submission is never repeated, whatever the failure was.
//     Those operations belong to internal/garmin/auth, and replaying one can lock
//     an account or burn a one-time code;
//   - an unsafe write and a delete are never repeated, because Garmin gives no
//     guarantee that the rejected request was not applied;
//   - an ordinary 4xx, a 404, a 401/403 and a 429 are never repeated: they are
//     deterministic and caller-actionable. A rate limit is reported with its
//     parsed Retry-After so the caller, which knows its own budget, decides.
//
// What remains is a transport failure and a selected 5xx. The delay is a bounded
// exponential step with full jitter — a uniform draw from [0, step] — so a fleet of
// clients cannot synchronize its retries. A Retry-After hint wins when it is
// longer, and a hint past the backoff cap ends the retries instead: sleeping
// minutes inside one tool call is worse for the caller than an actionable error.
func (c *Client) retryDelay(req Request, attempt int, err error) (time.Duration, bool) {
	if attempt+1 >= c.limits.MaxAttempts {
		return 0, false
	}
	if req.Op.IsCredentialSubmission() || !req.Effect.repeatable() {
		return 0, false
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.Retryable() {
		return 0, false
	}
	if apiErr.Status != 0 && !isRetryableServerStatus(apiErr.Status) {
		return 0, false
	}

	step := c.backoffStep(attempt)
	delay := time.Duration(c.jitterFraction() * float64(step))
	if hint := apiErr.RetryAfter; hint > delay {
		if hint > c.limits.MaxBackoff {
			return 0, false
		}
		delay = hint
	}
	return delay, true
}

// backoffStep is the exponential step for one attempt, capped by MaxBackoff.
func (c *Client) backoffStep(attempt int) time.Duration {
	step := c.limits.BaseBackoff
	for range attempt {
		step *= 2
		if step >= c.limits.MaxBackoff {
			return c.limits.MaxBackoff
		}
	}
	return step
}

// jitterFraction clamps the injected jitter source into [0,1].
func (c *Client) jitterFraction() float64 {
	fraction := c.jitter()
	switch {
	case fraction < 0:
		return 0
	case fraction > 1:
		return 1
	default:
		return fraction
	}
}

func isRetryableServerStatus(status int) bool {
	for _, retryable := range retryableServerStatuses {
		if status == retryable {
			return true
		}
	}
	return false
}

// classifyStatus maps an HTTP status onto a failure kind. The second result is
// false for a status that is not a failure at all.
//
// Source: the status handling in Client._run_request — a 404 is a missing
// resource, not a connectivity failure — plus the distinctions garmin_mcp issue
// #109 requires: a 429 is a rate limit, never a bad password, and a 401/403 is a
// rejected session.
func classifyStatus(status int, fileTransfer bool) (Kind, bool) {
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		return KindUnknown, false
	case status == http.StatusNotFound, status == http.StatusGone:
		return KindNotFound, true
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return KindAuthentication, true
	case status == http.StatusTooManyRequests:
		return KindRateLimited, true
	case status == http.StatusUnsupportedMediaType:
		return KindInvalidFile, true
	case status == http.StatusBadRequest && fileTransfer:
		return KindInvalidFile, true
	case status == http.StatusRequestTimeout, status == http.StatusTooEarly:
		return KindTemporaryConnection, true
	case status >= http.StatusInternalServerError:
		return KindServer, true
	case status >= http.StatusBadRequest:
		return KindValidation, true
	default:
		// A 1xx or a 3xx reaching this layer means the transport did not follow the
		// exchange to a final response.
		return KindUnknown, true
	}
}

// transportKind classifies a failure that arrived instead of a response.
//
// Only a recognized transport shape is temporary and therefore retryable.
// Anything else — a refused destination from the token layer, a programming error
// — stays KindUnknown, so this package never retries a failure it does not
// understand.
func transportKind(err error) Kind {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return KindTemporaryConnection
	}

	var netErr net.Error
	var opErr *net.OpError
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &netErr), errors.As(err, &opErr), errors.As(err, &dnsErr):
		return KindTemporaryConnection
	}
	if isSyscallTransportError(err) {
		return KindTemporaryConnection
	}
	return KindUnknown
}

// isSyscallTransportError recognizes the connection-level errno values a Garmin
// call can fail with.
func isSyscallTransportError(err error) bool {
	for _, errno := range [...]syscall.Errno{
		syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EPIPE,
		syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.ETIMEDOUT,
	} {
		if errors.Is(err, errno) {
			return true
		}
	}
	return false
}

// readBody reads the response body under both bounds: the wire bytes and, when the
// body is gzipped, the decompressed bytes. Either overrun is reported as
// ErrResponseTooLarge rather than truncated, because a truncated JSON document
// decodes into a plausible-looking half-record.
func (c *Client) readBody(resp *http.Response) ([]byte, error) {
	if resp.Body == nil || resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	wire, err := readBounded(resp.Body, c.limits.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Header.Get(headerContentEncoding)), encodingGzip) {
		return wire, nil
	}

	reader, err := gzip.NewReader(bytes.NewReader(wire))
	if err != nil {
		return nil, fmt.Errorf("garmin api: gzip response could not be read: %w", ErrMalformedPayload)
	}
	defer func() { _ = reader.Close() }()

	return readBounded(reader, c.limits.MaxDecompressedBytes)
}

// readBounded reads at most limit bytes, reporting ErrResponseTooLarge when more
// were available.
func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	read, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(read)) > limit {
		return nil, fmt.Errorf("garmin api: response over its bound: %w", ErrResponseTooLarge)
	}
	return read, nil
}
