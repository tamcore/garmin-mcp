package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// Caller performs one authenticated request for one principal. It is the seam
// between this package and the token lifecycle: *auth.Refresher satisfies it, and
// it is that type which attaches the DI bearer token, enforces the Garmin host
// allowlist and replays a 401 once after a refresh.
//
// The interface lives with its consumer on purpose. This package never sees a
// token, and no method here may attach one.
type Caller interface {
	Do(ctx context.Context, principal string, req *http.Request) (*http.Response, error)
}

// Session binds one principal to the Caller that may act for it. Every domain
// method takes one, so no call can be made without naming whose tokens it uses.
type Session struct {
	caller    Caller
	principal string
}

// NewSession validates a caller and a principal.
func NewSession(caller Caller, principal string) (Session, error) {
	if principal == "" {
		return Session{}, fmt.Errorf("garmin api: new session: %w", ErrMissingPrincipal)
	}
	if caller == nil {
		return Session{}, fmt.Errorf("garmin api: new session: no caller: %w", ErrNotConfigured)
	}
	return Session{caller: caller, principal: principal}, nil
}

// Principal is the pseudonymous principal identifier the session acts for.
func (s Session) Principal() string { return s.principal }

// IsZero reports whether the session is unusable.
func (s Session) IsZero() bool { return s.caller == nil || s.principal == "" }

// Sleeper waits for a backoff delay, or reports why it could not.
type Sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

// SleeperFunc adapts a function to Sleeper, which is how a test replaces real
// waiting.
type SleeperFunc func(ctx context.Context, d time.Duration) error

// Sleep implements Sleeper.
func (f SleeperFunc) Sleep(ctx context.Context, d time.Duration) error { return f(ctx, d) }

// realSleeper waits for d, or returns the context error if the wait is cut short.
type realSleeper struct{}

func (realSleeper) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Config configures a Client. Hosts is required; everything else has a safe
// default.
type Config struct {
	// Hosts builds absolute Garmin URLs for one region. It must be usable, so a
	// zero value is refused rather than aimed at a default region.
	Hosts protocol.Hosts
	// Limits holds every bound the request layer enforces. The zero value means
	// DefaultLimits.
	Limits Limits
	// Now is the time source used to interpret a Retry-After HTTP-date. Nil means
	// time.Now.
	Now func() time.Time
	// Sleeper waits out a backoff delay. Nil means real waiting.
	Sleeper Sleeper
	// Jitter returns a value in [0,1) used for full-jitter backoff. Nil means
	// math/rand/v2, which is adequate: jitter is pacing, not a secret.
	Jitter func() float64
	// Logger receives redacted records. Nil means slog.Default.
	Logger *slog.Logger
}

// Client is the authenticated request layer for Garmin's API tier.
//
// It owns URL construction, request validation, bounded body reading, response
// classification, retry pacing and tolerant decoding. It owns no credential and no
// per-principal state: a Session supplies both.
//
// A Client is immutable after construction and safe for concurrent use.
type Client struct {
	hosts   protocol.Hosts
	limits  Limits
	now     func() time.Time
	sleeper Sleeper
	jitter  func() float64
	logger  *slog.Logger
}

// New validates cfg and returns a Client.
func New(cfg Config) (*Client, error) {
	if cfg.Hosts.ConnectAPIBase() == "" {
		return nil, fmt.Errorf("garmin api: new client: unusable hosts: %w", ErrNotConfigured)
	}
	if err := cfg.Limits.Validate(); err != nil {
		return nil, fmt.Errorf("garmin api: new client: %w", err)
	}

	c := &Client{
		hosts:   cfg.Hosts,
		limits:  cfg.Limits.Resolved(),
		now:     cfg.Now,
		sleeper: cfg.Sleeper,
		jitter:  cfg.Jitter,
		logger:  cfg.Logger,
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleeper == nil {
		c.sleeper = realSleeper{}
	}
	if c.jitter == nil {
		c.jitter = rand.Float64
	}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	return c, nil
}

// Limits reports the resolved bounds this Client enforces.
func (c *Client) Limits() Limits { return c.limits }

// Hosts reports the region the Client addresses.
func (c *Client) Hosts() protocol.Hosts { return c.hosts }

// Do performs req for the session's principal and returns the bounded response
// payload.
//
// The context's deadline and cancellation are honored throughout: each attempt
// runs under a derived context bounded by the configured request timeout, so a
// caller deadline shorter than that timeout still wins. A 204 is normalized to an
// empty payload rather than an error. Every failure is an *APIError.
func (c *Client) Do(ctx context.Context, session Session, req Request) (Payload, error) {
	if session.IsZero() {
		return Payload{}, c.fail(req, 0, KindValidation, 0,
			fmt.Errorf("garmin api: unusable session: %w", ErrMissingPrincipal))
	}
	if err := req.Validate(); err != nil {
		return Payload{}, c.fail(req, 0, KindValidation, 0, err)
	}
	if err := ctx.Err(); err != nil {
		return Payload{}, c.fail(req, 0, KindTemporaryConnection, 0, err)
	}
	return c.doWithRetry(ctx, session, req)
}

// attempt performs exactly one request attempt under its own timeout.
func (c *Client) attempt(ctx context.Context, session Session, req Request) (Payload, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, c.limits.RequestTimeout)
	defer cancel()

	httpReq, err := c.newHTTPRequest(attemptCtx, req)
	if err != nil {
		return Payload{}, c.fail(req, 0, KindValidation, 0, err)
	}

	resp, err := session.caller.Do(attemptCtx, session.principal, httpReq)
	if err != nil {
		return Payload{}, c.fail(req, 0, transportKind(err), 0, err)
	}
	defer closeBody(resp)

	body, err := c.readBody(resp)
	if err != nil {
		return Payload{}, c.fail(req, resp.StatusCode, KindUnknown, c.retryAfter(resp), err)
	}

	payload := newPayload(req.Op, req.Endpoint, resp.StatusCode, resp.Header.Get(headerContentType), body)
	if kind, failed := classifyStatus(resp.StatusCode, req.FileTransfer); failed {
		return payload, c.fail(req, resp.StatusCode, kind, c.retryAfter(resp), nil)
	}
	return payload, nil
}

// Header names and values this package reads or writes.
const (
	headerContentType     = "Content-Type"
	headerContentEncoding = "Content-Encoding"
	headerAccept          = "Accept"
	headerAcceptEncoding  = "Accept-Encoding"
	encodingGzip          = "gzip"
)

// newHTTPRequest builds the outbound request. It sets GetBody so the caller can
// replay the request after a token refresh, and asks for gzip explicitly so this
// package, not the transport, owns the decompression bound.
func (c *Client) newHTTPRequest(ctx context.Context, req Request) (*http.Request, error) {
	target := req.requestURL(c.hosts.ConnectAPIBase())

	var reader io.Reader
	if len(req.Body) > 0 {
		reader = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.method(), target, reader)
	if err != nil {
		return nil, validationError("request could not be built for the configured host")
	}
	if len(req.Body) > 0 {
		body := req.Body
		httpReq.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
		httpReq.Header.Set(headerContentType, req.contentType())
	}

	for key, values := range protocol.NativeAPIHeaders() {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	httpReq.Header.Set(headerAccept, req.accept())
	httpReq.Header.Set(headerAcceptEncoding, encodingGzip)
	return httpReq, nil
}

// retryAfter parses the response's Retry-After header against the Client's clock.
func (c *Client) retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	delay, _ := protocol.ParseRetryAfter(resp.Header.Get(protocol.HeaderRetryAfter), c.now())
	return delay
}

// fail builds the labeled *APIError for one failure.
func (c *Client) fail(req Request, status int, kind Kind, retryAfter time.Duration, cause error) error {
	return &APIError{
		Op:         req.Op,
		Endpoint:   req.Endpoint,
		Status:     status,
		Kind:       kind,
		RetryAfter: retryAfter,
		Err:        cause,
	}
}

// closeBody drains a little and closes a response body, so the connection can be
// reused and no payload is left dangling.
func closeBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	_ = resp.Body.Close()
}
