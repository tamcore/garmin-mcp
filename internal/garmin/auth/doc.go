// Package auth performs the native Garmin Connect login and keeps the resulting
// DI token set usable.
//
// The reference behavior is python-garminconnect 0.3.10 (commit
// 414b54023a31259232744bb67f00a2aa71065e09, file garminconnect/client.py). Wire
// identifiers, pacing bounds and response classification live in
// internal/garmin/protocol; this package owns the state machine, the transport
// choreography, the MFA continuation and the token lifecycle.
//
// # Login state machine
//
// A login is an explicit Machine. Every transition is one method, the value is
// immutable, and a forbidden transition returns a *TransitionError that matches
// ErrInvalidTransition. The full table:
//
//	from                  | transition          | to
//	----------------------+---------------------+----------------------
//	created               | submit_credentials  | credentials_submitted
//	created               | fail                | failed
//	created               | expire              | expired
//	created               | cancel              | cancelled
//	credentials_submitted | require_mfa         | mfa_pending
//	credentials_submitted | authenticate        | authenticated
//	credentials_submitted | fail                | failed
//	credentials_submitted | expire              | expired
//	credentials_submitted | cancel              | cancelled
//	mfa_pending           | verify_mfa          | authenticated
//	mfa_pending           | fail                | failed
//	mfa_pending           | expire              | expired
//	mfa_pending           | cancel              | cancelled
//	authenticated         | (none)              | terminal
//	failed                | (none)              | terminal
//	expired               | (none)              | terminal
//	cancelled             | (none)              | terminal
//
// # Strategy fallback
//
// Login tries mobile iOS, then the SSO embed widget, then the portal. The
// classifier decides each step: a definitive invalid-credential or locked verdict
// stops the chain, while a bot challenge, a rate limit, a temporary failure, an
// unrecognized response and a session the API tier refuses fall through to the
// next strategy. The anti-WAF pacing bounds from the protocol package are honored
// between a page GET and a credential POST, driven by an injected Clock, Sleeper
// and Jitter.
//
// Upstream's five strategies collapse to three: its cffi and requests variants
// differ only in the curl_cffi TLS fingerprint, which Go's standard TLS stack
// cannot reproduce. The Doer transport is where a fingerprinting variant would
// attach if the phase-0 gate ever produces evidence for one.
//
// # MFA continuation
//
// Pending cookies, the CSRF value, the selected strategy and the MFA metadata
// stay server-side in a Registry: a bounded map with a short absolute TTL, an
// attempt budget, cancellation and a single-use terminal transition. The caller
// receives only an opaque 256-bit capability, and the registry stores its SHA-256
// rather than the capability itself. Every login owns its own entry, so two
// interleaved logins cannot observe or overwrite each other — the failure
// upstream 0.3.10 had to fix. Cross-principal, expired, replayed and out-of-order
// transitions are all refused.
//
// # Tokens
//
// A CAS service ticket is exchanged for a DI token set against the DI token URL,
// trying the candidate DI client ids in the pinned order. Every candidate session
// is validated against the social-profile endpoint before it is stored, and a
// rejection (401/403) stays distinguishable from a temporary validation failure.
// Refresh happens inside a configurable safety window that defaults to the
// upstream 15 minutes, is serialized per principal, and persists the rotated
// refresh token with a compare-and-set so a slow writer cannot clobber a newer
// token. After a 401, a safe or idempotent call is retried once, and only after a
// successful refresh.
//
// # Secrets
//
// No password and no OTP is ever stored in a field that outlives its request.
// TokenSet, Credentials, Pending and Result are secret-bearing: their material
// sits behind a pointer to unexported fields, and String, GoString, MarshalJSON
// and LogValue render presence rather than content. No credential, token, cookie,
// MFA code or raw body appears in an error, a log record or a rendered string.
//
// # Unverified claims
//
// UnverifiedExpiry and UnverifiedClientID read a DI token's payload without
// verifying its signature. They exist for scheduling and labeling only. Never
// authorize anything from one.
//
// # Documented gaps
//
//   - Widget MFA variable parsing. Upstream 0.3.10 parses the widget page's
//     inline JS variables (customerGuid, mfaMethod, locale, clientId, codeSentTo)
//     and explicitly requests email or SMS code delivery via
//     protocol.PathWidgetRequestMFACode. The protocol package ports only the wire
//     constant, so this package cannot build that request and does not send it;
//     Pending.MFADeliveryUncertain reports the uncertainty instead.
//   - JWT_WEB cookie fallback. Upstream falls back to consuming the CAS ticket
//     through the web front end when the DI exchange fails. This package requires
//     the DI token set and reports the exchange failure instead.
//   - curl_cffi TLS fingerprint rotation and the randomized browser header sets
//     that pair with it.
//   - Local 0.3.x garmin_tokens.json import and export, which belongs to the
//     storage layer.
package auth
