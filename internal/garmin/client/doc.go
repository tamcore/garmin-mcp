// Package client is the authenticated request layer for Garmin Connect's API
// tier.
//
// The reference behavior is python-garminconnect 0.3.10 (commit
// 414b54023a31259232744bb67f00a2aa71065e09): Client._run_request for the request
// path, the URL table in GarminConnect.__init__ for the endpoints, and the
// _handle_api_errors decorator for the retry policy. Wire identifiers, response
// classification and redaction for the login surface live in
// internal/garmin/protocol; the token lifecycle lives in internal/garmin/auth.
// This package owns everything between them: URL construction, request validation,
// bounded reading, failure classification, retry pacing and tolerant decoding.
// Domain clients live in internal/garmin/api.
//
// # Boundaries
//
// This package never sees a credential. A Session pairs a principal with a Caller,
// and *auth.Refresher is that Caller: it attaches the DI bearer token, refuses any
// host outside the configured protocol.Hosts, and replays a 401 once after a
// refresh. Nothing here may attach a token, and nothing here may widen that host
// boundary.
//
// # Bounds
//
// Limits holds every bound, and its zero value means the safe defaults rather than
// "unbounded":
//
//	request timeout          30s    per attempt, headers and body included
//	connect timeout          10s
//	response header timeout  15s    matches upstream's 15s API timeout
//	tls handshake timeout    10s
//	response bytes           8 MiB  wire bytes, cap 64 MiB
//	decompressed bytes       32 MiB cap 128 MiB
//	page size                20     cap 1000 (upstream MAX_ACTIVITY_LIMIT)
//	pages per read           100    cap 2000 (upstream MAX_PAGINATED_REQUESTS)
//	date range               366 days, cap 1826
//	concurrent fan-out       4      cap 16
//	attempts                 3 total (upstream retry_attempts)
//	backoff                  1s base, 10s cap (upstream retry_min/max_wait)
//
// Compression is handled here rather than by the transport: the request asks for
// gzip explicitly, so both the wire bytes and the decompressed bytes are read under
// their own bound and a compression bomb cannot exhaust memory. An overrun is
// reported, never truncated, because a truncated JSON document decodes into a
// plausible-looking half-record.
//
// # Failures
//
// Every failure is an *APIError carrying the sanitized Op and Endpoint labels, the
// HTTP status, the classified Kind, the parsed Retry-After and a wrapped cause.
// The classes stay distinguishable — ErrNotFound, ErrAuthentication,
// ErrRateLimited, ErrInvalidFile, ErrValidation, ErrTemporaryConnection, ErrServer,
// ErrMalformedPayload — because collapsing them is what made upstream report a rate
// limit as a bad password (garmin_mcp issue #109). errors.Is and errors.As reach
// both the kind sentinel and the real cause.
//
// A rendered message contains only labels: no URL with a query, no header, no
// cookie, no token, no body and no coordinate. Only a recognized cause shape is
// described — this package's sentinels, a nested *APIError, a self-redacting
// *protocol.Error, a context error — and anything else degrades to its Go type
// name.
//
// # Retry policy
//
// Only a transport failure and a selected 5xx (500, 502, 503, 504, 507) are
// retried, with a bounded exponential step and full jitter, honoring Retry-After
// when it is longer and abandoning the retries when it is longer than the backoff
// cap. Never retried: a credential or MFA submission, an unsafe write, a delete, an
// ordinary 4xx, a 404, a 401/403 and a 429. The prohibitions are explicit in the
// predicate, not implied by the classifier.
//
// A 204 is normalized: the payload reports NoContent and DecodeJSON leaves the
// target untouched, matching upstream's empty-JSON normalization.
//
// # Tolerant decoding
//
// Garmin's schemas drift, so a domain model uses optional pointers,
// json.RawMessage where the shape varies, and the union decoders Number, Text and
// List, which accept the several shapes upstream tolerates. An unknown field never
// fails an otherwise useful response. The raw payload is retained for diagnostics as
// a Payload, whose bytes sit two pointers deep and whose String, GoString,
// MarshalJSON and LogValue report only the shape.
//
// # Strict request models
//
// A write, a date, a page and an identifier are typed and validated before anything
// is dispatched: Date, DateRange, Page, ID, DisplayName and Request with its
// Effect. A path is a clean absolute path — no query, no fragment, no backslash, no
// traversal segment in either the literal or the percent-decoded form — and every
// query parameter is encoded by this package. DisplayName is identity material and
// follows the repository redaction convention.
//
// # File downloads
//
// Download streams a file response into a caller-supplied io.Writer instead of
// buffering it into a Payload, because an activity or workout file is the one
// Garmin response that is not a small JSON document. Both bounds still apply —
// MaxResponseBytes to the wire bytes, MaxDecompressedBytes to the bytes handed to
// the sink — and an overrun is reported rather than truncated. The transfer is
// attempted exactly once: the sink already holds the failed attempt's bytes, so a
// retry could only corrupt it. This package never opens, creates or names a file.
//
// # Documented gaps
//
//   - Multipart upload, which activity and course file import need.
//   - The JWT_WEB cookie fallback and any non-DI authentication, which stay with
//     internal/garmin/auth.
//   - Per-endpoint response caching and per-principal rate limiting, which belong to
//     internal/ratelimit and the tool layer.
package client
