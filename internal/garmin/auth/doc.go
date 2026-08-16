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
// attempt budget, a per-transaction byte bound, cancellation and a single-use
// terminal transition. The caller receives only an opaque 256-bit capability, and
// the registry stores its SHA-256 rather than the capability itself. A capability
// must be canonical — 43 base64url characters — or it is refused before it is
// hashed, and the stored digest is verified with a constant-time compare. Every
// login owns its own entry, so two interleaved logins cannot observe or overwrite
// each other — the failure upstream 0.3.10 had to fix. Cross-principal, expired,
// replayed and out-of-order transitions are all refused.
//
// A completion is leased. Registry.Attempt charges one attempt and hands out an
// *Attempt that holds the transaction's only completion lease, so a second
// submission of the same capability performs no external effect at all. The lease
// holder claims the terminal success — which re-checks the absolute TTL, because
// the verification before it is a network call that can outlive the transaction —
// and only then are the ticket exchanged and the tokens saved. A completion that
// fails after the claim leaves no usable transaction, so the login restarts rather
// than resuming a half-completed one. A wrong code releases the lease and the
// transaction stays usable until its attempt budget runs out.
//
// Capacity pressure evicts rather than refuses: an abandoned start goes before one
// a user is working on, and a transaction whose completion is in flight is never
// evicted. A registry is reported full only when every resident transaction is
// being completed, so a flood of abandoned starts cannot deny service to every new
// login for a whole TTL.
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
// token. Concurrent refreshes of one principal collapse into a single flight whose
// result is published, closed and retired as one mutex-protected operation, so no
// late caller can start a redundant rotation.
//
// Every token-producing operation reads its compare-and-set baseline before it
// produces a candidate, and a conflict is final: the candidate is stale by
// definition, so it is never rewritten, and the login reports
// ErrTokenPersistenceFailed with the cause wrapped. Login and refresh serialize
// against each other through a TokenGate; pass one gate to Config.TokenGate and
// RefreshConfig.TokenGate whenever an Authenticator and a Refresher share a store.
//
// After a 401, a safe or idempotent call is retried once, and only after a
// successful refresh.
//
// Refresher.Do attaches a DI bearer token only to a request whose scheme and host
// exactly match one of the bases the configured protocol.Hosts exposes. Every
// other destination — a foreign host, a suffix of a Garmin host, a plaintext
// downgrade, a URL carrying userinfo — is refused with ErrForeignHost before the
// token is attached and before anything is dispatched, on the first attempt and on
// the replay alike. The boundary follows the configured Hosts, so it moves with the
// region and with a test's protocol.Overrides. It cannot cover redirects the
// caller's Doer follows on its own; see Refresher.Do for that residual risk.
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
//   - Widget MFA code delivery is no longer a gap: the page's inline JS variables
//     are parsed by the protocol package and requestWidgetMFACode asks Garmin to
//     send an email or SMS code before the caller is prompted for one. What
//     remains deliberate is the handling of a failed request — it does not fail
//     the login, because a code may have arrived from the sign-in POST anyway, and
//     Pending.MFADeliveryUncertain then reports that delivery is unconfirmed.
//   - MFA transaction binding. A pending transaction is bound to its principal
//     only. It is not bound to the browser session, the OAuth client, the redirect
//     URI, the requested resource or a PKCE challenge, so a capability that leaks
//     to another client of the same principal is usable there. That binding belongs
//     with the M2 OAuth transaction work and is deliberately not attempted here.
//   - JWT_WEB cookie fallback. Upstream falls back to consuming the CAS ticket
//     through the web front end when the DI exchange fails. This package requires
//     the DI token set and reports the exchange failure instead.
//   - curl_cffi TLS fingerprint rotation and the randomized browser header sets
//     that pair with it.
//   - Local 0.3.x garmin_tokens.json import and export, which belongs to the
//     storage layer.
package auth
