package client

import (
	"fmt"
	"time"
)

// Safe defaults. Every default is the most restrictive value that still serves
// the Garmin surface this package reads.
const (
	// DefaultRequestTimeout bounds one outbound call end to end, including the
	// body read. It matches config.DefaultRequestTimeout; this package keeps its
	// own constant so the transport layer does not depend on server settings.
	DefaultRequestTimeout = 30 * time.Second
	// DefaultConnectTimeout bounds the TCP dial.
	DefaultConnectTimeout = 10 * time.Second
	// DefaultResponseHeaderTimeout bounds the wait for response headers after the
	// request is written. Source: the 15-second timeout upstream sets on every
	// API request in Client._run_request.
	DefaultResponseHeaderTimeout = 15 * time.Second
	// DefaultTLSHandshakeTimeout bounds the TLS handshake.
	DefaultTLSHandshakeTimeout = 10 * time.Second
	// DefaultMaxResponseBytes bounds the bytes read off the wire.
	DefaultMaxResponseBytes int64 = 8 << 20
	// DefaultMaxDecompressedBytes bounds the bytes produced by decompressing a
	// response, which is the bound a compression bomb attacks.
	DefaultMaxDecompressedBytes int64 = 32 << 20
	// DefaultMaxPageSize is the page size Garmin's own web client uses.
	// Source: the limit = 20 in get_activities and get_activities_by_date.
	DefaultMaxPageSize = 20
	// DefaultMaxPages bounds a paginated read.
	DefaultMaxPages = 100
	// DefaultMaxDateRangeDays bounds a queried date window.
	DefaultMaxDateRangeDays = 366
	// DefaultMaxConcurrency bounds concurrent fan-out for one principal.
	DefaultMaxConcurrency = 4
	// DefaultMaxAttempts is the total attempt count, so 3 means one call plus two
	// retries. Source: retry_attempts = 3 in GarminConnect.__init__.
	DefaultMaxAttempts = 3
	// DefaultBaseBackoff is the first backoff step.
	// Source: retry_min_wait = 1.0.
	DefaultBaseBackoff = time.Second
	// DefaultMaxBackoff caps one backoff step. Source: retry_max_wait = 10.0.
	DefaultMaxBackoff = 10 * time.Second
)

// Validation caps. A bound an operator can raise without limit is a denial of
// service waiting to happen, so every bound has a ceiling of its own.
const (
	// MaxResponseBytesCap is the largest accepted wire-body bound.
	MaxResponseBytesCap int64 = 64 << 20
	// MaxDecompressedBytesCap is the largest accepted decompressed-body bound.
	MaxDecompressedBytesCap int64 = 128 << 20
	// MaxPageSizeCap is the largest accepted page size.
	// Source: MAX_ACTIVITY_LIMIT = 1000.
	MaxPageSizeCap = 1000
	// MaxPagesCap is the largest accepted page count.
	// Source: MAX_PAGINATED_REQUESTS = 2000.
	MaxPagesCap = 2000
	// MaxDateRangeDaysCap is the largest accepted date window, five years.
	MaxDateRangeDaysCap = 1826
	// MaxConcurrencyCap is the largest accepted fan-out.
	MaxConcurrencyCap = 16
	// MaxAttemptsCap is the largest accepted attempt count.
	MaxAttemptsCap = 10
	// MaxTimeoutCap is the largest accepted timeout.
	MaxTimeoutCap = 10 * time.Minute
	// MaxBackoffCap is the largest accepted backoff step.
	MaxBackoffCap = 2 * time.Minute
)

// Limits holds every bound the request layer enforces. It is plain immutable
// data: a zero field means "use the default", so Limits{} is the safe
// configuration rather than an unbounded one.
type Limits struct {
	// RequestTimeout bounds one attempt end to end, headers and body included.
	RequestTimeout time.Duration
	// ConnectTimeout bounds the dial.
	ConnectTimeout time.Duration
	// ResponseHeaderTimeout bounds the wait for response headers.
	ResponseHeaderTimeout time.Duration
	// TLSHandshakeTimeout bounds the TLS handshake.
	TLSHandshakeTimeout time.Duration

	// MaxResponseBytes bounds the bytes read off the wire.
	MaxResponseBytes int64
	// MaxDecompressedBytes bounds the bytes a decompressed response may produce.
	// It must be at least MaxResponseBytes.
	MaxDecompressedBytes int64

	// MaxPageSize bounds one page of a paginated read.
	MaxPageSize int
	// MaxPages bounds how many pages one paginated read may fetch.
	MaxPages int
	// MaxDateRangeDays bounds an inclusive date window.
	MaxDateRangeDays int
	// MaxConcurrency bounds concurrent fan-out for one principal.
	MaxConcurrency int

	// MaxAttempts is the total attempts per request, retries included.
	MaxAttempts int
	// BaseBackoff is the first backoff step; it doubles per attempt.
	BaseBackoff time.Duration
	// MaxBackoff caps one backoff step before jitter.
	MaxBackoff time.Duration
}

// DefaultLimits returns the safe defaults. The result validates.
func DefaultLimits() Limits {
	return Limits{
		RequestTimeout:        DefaultRequestTimeout,
		ConnectTimeout:        DefaultConnectTimeout,
		ResponseHeaderTimeout: DefaultResponseHeaderTimeout,
		TLSHandshakeTimeout:   DefaultTLSHandshakeTimeout,
		MaxResponseBytes:      DefaultMaxResponseBytes,
		MaxDecompressedBytes:  DefaultMaxDecompressedBytes,
		MaxPageSize:           DefaultMaxPageSize,
		MaxPages:              DefaultMaxPages,
		MaxDateRangeDays:      DefaultMaxDateRangeDays,
		MaxConcurrency:        DefaultMaxConcurrency,
		MaxAttempts:           DefaultMaxAttempts,
		BaseBackoff:           DefaultBaseBackoff,
		MaxBackoff:            DefaultMaxBackoff,
	}
}

// Resolved returns a copy of l with every zero field replaced by its default.
// The receiver is not modified.
func (l Limits) Resolved() Limits {
	out := DefaultLimits()
	pickDuration(&out.RequestTimeout, l.RequestTimeout)
	pickDuration(&out.ConnectTimeout, l.ConnectTimeout)
	pickDuration(&out.ResponseHeaderTimeout, l.ResponseHeaderTimeout)
	pickDuration(&out.TLSHandshakeTimeout, l.TLSHandshakeTimeout)
	pickDuration(&out.BaseBackoff, l.BaseBackoff)
	pickDuration(&out.MaxBackoff, l.MaxBackoff)
	pickInt64(&out.MaxResponseBytes, l.MaxResponseBytes)
	pickInt64(&out.MaxDecompressedBytes, l.MaxDecompressedBytes)
	pickInt(&out.MaxPageSize, l.MaxPageSize)
	pickInt(&out.MaxPages, l.MaxPages)
	pickInt(&out.MaxDateRangeDays, l.MaxDateRangeDays)
	pickInt(&out.MaxConcurrency, l.MaxConcurrency)
	pickInt(&out.MaxAttempts, l.MaxAttempts)
	return out
}

// Validate reports whether l is usable. A zero field is a default, not an error;
// a negative field, a field over its cap, and an incoherent pair are rejected.
// Every failure wraps ErrInvalidLimits and names the field, never a value the
// caller did not supply.
func (l Limits) Validate() error {
	if err := l.validateScalars(); err != nil {
		return err
	}

	resolved := l.Resolved()
	if resolved.MaxDecompressedBytes < resolved.MaxResponseBytes {
		return limitError("max decompressed bytes is below max response bytes")
	}
	if resolved.BaseBackoff > resolved.MaxBackoff {
		return limitError("base backoff is above max backoff")
	}
	return nil
}

// validateScalars checks every field against zero and its own ceiling.
func (l Limits) validateScalars() error {
	checks := []struct {
		name    string
		value   int64
		ceiling int64
	}{
		{"max response bytes", l.MaxResponseBytes, MaxResponseBytesCap},
		{"max decompressed bytes", l.MaxDecompressedBytes, MaxDecompressedBytesCap},
		{"max page size", int64(l.MaxPageSize), MaxPageSizeCap},
		{"max pages", int64(l.MaxPages), MaxPagesCap},
		{"max date range days", int64(l.MaxDateRangeDays), MaxDateRangeDaysCap},
		{"max concurrency", int64(l.MaxConcurrency), MaxConcurrencyCap},
		{"max attempts", int64(l.MaxAttempts), MaxAttemptsCap},
		{"request timeout", int64(l.RequestTimeout), int64(MaxTimeoutCap)},
		{"connect timeout", int64(l.ConnectTimeout), int64(MaxTimeoutCap)},
		{"response header timeout", int64(l.ResponseHeaderTimeout), int64(MaxTimeoutCap)},
		{"tls handshake timeout", int64(l.TLSHandshakeTimeout), int64(MaxTimeoutCap)},
		{"base backoff", int64(l.BaseBackoff), int64(MaxBackoffCap)},
		{"max backoff", int64(l.MaxBackoff), int64(MaxBackoffCap)},
	}

	for _, check := range checks {
		switch {
		case check.value < 0:
			return limitError(check.name + " is negative")
		case check.value > check.ceiling:
			return limitError(check.name + " exceeds its cap")
		}
	}
	return nil
}

// limitError builds a rejection that matches ErrInvalidLimits.
func limitError(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidLimits, reason)
}

func pickDuration(target *time.Duration, value time.Duration) {
	if value > 0 {
		*target = value
	}
}

func pickInt64(target *int64, value int64) {
	if value > 0 {
		*target = value
	}
}

func pickInt(target *int, value int) {
	if value > 0 {
		*target = value
	}
}
