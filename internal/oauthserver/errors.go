package oauthserver

import "errors"

// Validation sentinels. Every error a parser or a constructor in this package
// returns wraps exactly one of them, so a caller branches with errors.Is and
// never with a string match. No message ever carries a token, a code, a verifier
// or client state.
var (
	// ErrInvalidLookup reports a malformed persisted lookup value. Only the
	// storage adapter can produce it, by reading a corrupt column.
	ErrInvalidLookup = errors.New("oauthserver: invalid lookup value")

	// ErrInvalidScope reports a scope token that breaks the RFC 6749 §3.3
	// grammar or the bounds this package sets.
	ErrInvalidScope = errors.New("oauthserver: invalid scope")

	// ErrInvalidRedirectURI reports a redirect URI this server refuses to
	// register or to redirect to: relative, over-long, fragment-bearing,
	// userinfo-bearing, wildcarded, or non-HTTPS without being a literal
	// loopback address.
	ErrInvalidRedirectURI = errors.New("oauthserver: invalid redirect URI")

	// ErrRedirectURINotRegistered reports that a presented redirect URI is not
	// byte-identical to one registered for the client. This is the error that
	// must never be delivered by redirecting; it is rendered locally.
	ErrRedirectURINotRegistered = errors.New("oauthserver: redirect URI is not registered for this client")

	// ErrInvalidResource reports a malformed RFC 8707 resource indicator.
	ErrInvalidResource = errors.New("oauthserver: invalid resource indicator")

	// ErrResourceNotAllowed reports a well-formed resource indicator that is not
	// this deployment's canonical resource, or not one the client may request.
	ErrResourceNotAllowed = errors.New("oauthserver: resource indicator is not allowed")

	// ErrInvalidCodeChallenge reports a missing or malformed PKCE challenge,
	// including the "plain" method, which this server never accepts.
	ErrInvalidCodeChallenge = errors.New("oauthserver: invalid PKCE code challenge")

	// ErrInvalidCodeVerifier reports a code verifier that breaks the RFC 7636
	// §4.1 grammar or its length bounds.
	ErrInvalidCodeVerifier = errors.New("oauthserver: invalid PKCE code verifier")

	// ErrPKCEVerificationFailed reports a well-formed verifier that does not hash
	// to the bound challenge.
	ErrPKCEVerificationFailed = errors.New("oauthserver: PKCE verification failed")

	// ErrInvalidClient reports a client registration this server refuses.
	ErrInvalidClient = errors.New("oauthserver: invalid client registration")

	// ErrClientAuthFailed reports that client authentication at the token
	// endpoint failed, including a public client that presented a secret.
	ErrClientAuthFailed = errors.New("oauthserver: client authentication failed")

	// ErrInvalidState reports client state that breaks the bounds this server
	// sets. The state itself never appears in the message.
	ErrInvalidState = errors.New("oauthserver: invalid client state")

	// ErrInvalidConfig reports a Config that cannot be served safely.
	ErrInvalidConfig = errors.New("oauthserver: invalid configuration")
)

// Protocol and lifecycle sentinels. A storage implementation returns the
// "not found", "already used" and "reused" members; everything else originates
// in this package.
var (
	// ErrUnknownClient reports that no client is registered under the presented
	// client id.
	ErrUnknownClient = errors.New("oauthserver: unknown client")

	// ErrTransactionNotFound reports that no live authorization transaction
	// matches the presented capability. An expired or completed transaction is
	// indistinguishable from one that never existed, by design.
	ErrTransactionNotFound = errors.New("oauthserver: authorization transaction not found")

	// ErrTransactionExpired reports that a transaction outlived its bounded
	// lifetime.
	ErrTransactionExpired = errors.New("oauthserver: authorization transaction expired")

	// ErrTransactionStage reports an operation attempted out of order, for
	// example issuing a code before the principal was resolved.
	ErrTransactionStage = errors.New("oauthserver: authorization transaction is at the wrong stage")

	// ErrTransactionConflict reports that a compare-and-set on a transaction
	// lost, which means another request advanced it concurrently.
	ErrTransactionConflict = errors.New("oauthserver: authorization transaction changed concurrently")

	// ErrCodeNotFound reports that no live authorization code matches.
	ErrCodeNotFound = errors.New("oauthserver: authorization code not found")

	// ErrCodeAlreadyUsed reports a replayed authorization code. A code is
	// single-use and its consumption is atomic.
	ErrCodeAlreadyUsed = errors.New("oauthserver: authorization code was already used")

	// ErrCodeExpired reports a code presented after its bounded lifetime.
	ErrCodeExpired = errors.New("oauthserver: authorization code expired")

	// ErrCodeBinding reports that a code was presented with a client id,
	// redirect URI, resource or PKCE verifier that does not match the bindings
	// captured when it was issued.
	ErrCodeBinding = errors.New("oauthserver: authorization code binding mismatch")

	// ErrTokenNotFound reports that no live token matches the presented value.
	ErrTokenNotFound = errors.New("oauthserver: token not found")

	// ErrTokenExpired reports a token presented after its expiry.
	ErrTokenExpired = errors.New("oauthserver: token expired")

	// ErrTokenRevoked reports a token whose family has been revoked.
	ErrTokenRevoked = errors.New("oauthserver: token family is revoked")

	// ErrRefreshTokenReused reports that an already-rotated refresh token was
	// presented again. The storage implementation revokes the whole family in the
	// same transaction that detects this.
	ErrRefreshTokenReused = errors.New("oauthserver: refresh token was reused")

	// ErrRefreshBinding reports a refresh request that tries to widen scope,
	// change resource, or present the token under a different client.
	ErrRefreshBinding = errors.New("oauthserver: refresh token binding mismatch")

	// ErrConsentNotFound reports that no consent record covers the request.
	ErrConsentNotFound = errors.New("oauthserver: no consent record")

	// ErrConsentRequired reports that the request needs fresh consent, because no
	// record covers it or because it widens scope beyond the recorded grant.
	ErrConsentRequired = errors.New("oauthserver: fresh consent is required")

	// ErrMissingToken reports a protected-resource request that carried no bearer
	// token at all. RFC 6750 §3.1 distinguishes that from an invalid token: the
	// challenge for it carries no error code.
	ErrMissingToken = errors.New("oauthserver: request carried no bearer token")

	// ErrInvalidToken reports a bearer token that was presented but is not
	// usable: unknown, expired, revoked, or minted for another audience.
	ErrInvalidToken = errors.New("oauthserver: invalid bearer token")

	// ErrInsufficientScope reports a verified token that lacks a required scope.
	ErrInsufficientScope = errors.New("oauthserver: insufficient scope")

	// ErrUnsupportedGrantType reports a grant this server does not implement. The
	// implicit grant and the resource-owner password grant are permanently in
	// this set.
	ErrUnsupportedGrantType = errors.New("oauthserver: unsupported grant type")

	// ErrUnsupportedResponseType reports a response type other than "code".
	ErrUnsupportedResponseType = errors.New("oauthserver: unsupported response type")

	// ErrDuplicateParameter reports a request that carried a security-relevant
	// parameter more than once, which different parsers can disagree about.
	ErrDuplicateParameter = errors.New("oauthserver: duplicate request parameter")

	// ErrStorage reports that the storage layer failed. The wrapped cause stays
	// for the log; no client ever sees it.
	ErrStorage = errors.New("oauthserver: storage failure")
)
