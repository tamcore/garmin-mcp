package oauthserver

import (
	"fmt"
	"net/http"
)

// The OAuth error codes this server emits, from RFC 6749 §4.1.2.1 and §5.2 and
// RFC 8707 §2. They are the only strings that ever reach a client as an error code;
// anything more specific stays in the wrapped cause, for the server's own log.
const (
	// ErrorInvalidRequest is a malformed or incomplete request.
	ErrorInvalidRequest = "invalid_request"
	// ErrorInvalidClient is a client that cannot be identified or authenticated.
	ErrorInvalidClient = "invalid_client"
	// ErrorInvalidGrant is a code or refresh token that is unusable, including a
	// failed PKCE verification and a broken binding.
	ErrorInvalidGrant = "invalid_grant"
	// ErrorUnauthorizedClient is a client not permitted this grant.
	ErrorUnauthorizedClient = "unauthorized_client"
	// ErrorUnsupportedResponseType covers the implicit and hybrid flows, which this
	// server does not implement.
	ErrorUnsupportedResponseType = "unsupported_response_type"
	// ErrorUnsupportedGrantType covers the resource-owner password grant and every
	// other grant this server does not implement.
	ErrorUnsupportedGrantType = "unsupported_grant_type"
	// ErrorInvalidScope is a scope request that is malformed or wider than the
	// client may have.
	ErrorInvalidScope = "invalid_scope"
	// ErrorInvalidTarget is the RFC 8707 §2 code for an unusable resource indicator.
	ErrorInvalidTarget = "invalid_target"
	// ErrorAccessDenied is the user declining.
	ErrorAccessDenied = "access_denied"
	// ErrorServerError is this server failing.
	ErrorServerError = "server_error"
)

// An AuthorizeError is a failure at the authorization endpoint, together with the
// decision about how it may be delivered.
//
// That decision is the point of the type. An authorization error may be sent to a
// redirect URI only once the client and the exact registered redirect URI are both
// validated; before that, the only safe destination is a locally rendered page,
// because the presented redirect URI may be an attacker's. Location is therefore
// empty unless redirecting has been earned, and a caller that reads Location and
// finds it empty has no choice but to render.
//
// Description is a fixed, sanitized string. The specific cause is reachable with
// errors.Is or errors.Unwrap for the log, and is never rendered.
type AuthorizeError struct {
	code        string
	description string
	status      int
	location    string
	cause       error
}

// localAuthorizeError returns an error that this server must render itself.
func localAuthorizeError(code, description string, cause error) *AuthorizeError {
	return &AuthorizeError{
		code:        code,
		description: description,
		status:      http.StatusBadRequest,
		cause:       cause,
	}
}

// redirectAuthorizeError returns an error that may be delivered to the client's
// already-validated redirect URI, with the client's state echoed byte for byte.
//
// A state that failed its own validation is deliberately not echoed: there is
// nothing trustworthy to echo. If building the redirect fails, the error degrades to
// a local render rather than guessing at a destination.
func redirectAuthorizeError(
	redirect RedirectURI, state ClientState, code, description string, cause error,
) *AuthorizeError {
	params := map[string]string{paramError: code, paramErrorDescription: description}
	if !state.IsZero() {
		params[paramState] = state.Reveal()
	}
	location, err := redirect.WithParams(params)
	if err != nil {
		return localAuthorizeError(code, description, cause)
	}
	return &AuthorizeError{
		code:        code,
		description: description,
		status:      http.StatusFound,
		location:    location,
		cause:       cause,
	}
}

// Code returns the OAuth error code.
func (e *AuthorizeError) Code() string { return e.code }

// Description returns the sanitized description. It is safe to render and safe to
// put in a query parameter: it carries no token, code, verifier or state.
func (e *AuthorizeError) Description() string { return e.description }

// Status returns the HTTP status the caller should use: 400 for a local render, 302
// for a redirect.
func (e *AuthorizeError) Status() int { return e.status }

// Location returns the redirect target, or the empty string when the error must be
// rendered locally.
func (e *AuthorizeError) Location() string { return e.location }

// IsRedirect reports whether this error may be delivered by redirecting.
func (e *AuthorizeError) IsRedirect() bool { return e.location != "" }

// Error implements error. It names the code, the delivery decision and the cause,
// and never a credential.
func (e *AuthorizeError) Error() string {
	delivery := "local"
	if e.IsRedirect() {
		delivery = "redirect"
	}
	if e.cause == nil {
		return fmt.Sprintf("oauthserver: %s (%s)", e.code, delivery)
	}
	return fmt.Sprintf("oauthserver: %s (%s): %v", e.code, delivery, e.cause)
}

// Unwrap exposes the specific cause, so a caller can branch with errors.Is on the
// sentinels in errors.go.
func (e *AuthorizeError) Unwrap() error { return e.cause }
