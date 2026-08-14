package protocol

import "net/http"

// statusContext names which request produced a response. An HTTP status alone is
// ambiguous: 401 on a credential POST, on a widget GET and on a profile
// validation call mean three different things, and reading them from one shared
// table produced false "bad password" verdicts that stopped the login strategy
// chain (garmin_mcp issue #109).
type statusContext int

const (
	// contextLoginPOST is a credential or OTP POST to a JSON or HTML login
	// endpoint. Source: the status handling in Client._do_mobile_login,
	// _do_portal_web_login, _widget_web_login step 3 and _complete_mfa.
	contextLoginPOST statusContext = iota

	// contextWidgetPage is a widget embed or sign-in GET, fetched for cookies and
	// the CSRF token. Source: Client._widget_web_login steps 1 and 2, which
	// special-case 429 and otherwise fall through on "not r.ok".
	contextWidgetPage

	// contextSessionValidation is the authenticated profile call that proves a
	// candidate session works. Source: Client._verify_token, where only a
	// definitive 401/403 returns False.
	contextSessionValidation
)

// String makes a context readable in a test failure.
func (c statusContext) String() string {
	switch c {
	case contextLoginPOST:
		return "login_post"
	case contextWidgetPage:
		return "widget_page"
	case contextSessionValidation:
		return "session_validation"
	default:
		return labelUnknown
	}
}

// statusOutcomeFor interprets an HTTP status in one context. The second result
// reports whether the status carries a verdict at all; false means the caller
// must decide from the body and leaves the outcome untouched.
//
// No context maps a bare status to OutcomeInvalidCredentials: only an explicit
// credential rejection in the payload may stop the fallback chain.
func statusOutcomeFor(ctx statusContext, status int) (Outcome, bool) {
	if status == http.StatusTooManyRequests {
		return OutcomeRateLimited, true
	}
	if ctx == contextSessionValidation {
		return sessionValidationOutcome(status)
	}

	switch {
	case status == http.StatusForbidden:
		// A 403 on a login or widget request is Cloudflare or a CAPTCHA gate.
		return OutcomeBotChallenge, true
	case status == http.StatusRequestTimeout, status == http.StatusTooEarly:
		return OutcomeTemporaryFailure, true
	case status >= http.StatusInternalServerError:
		return OutcomeTemporaryFailure, true
	default:
		// Includes 401: a session, CSRF or protocol fault, never a verdict on
		// the password.
		return OutcomeUnknown, false
	}
}

// sessionValidationOutcome interprets the profile validation call. Both 401 and
// 403 mean the API tier rejected this session, which is a reason to discard the
// token and try another login strategy, not a bot challenge and not a bad
// password.
func sessionValidationOutcome(status int) (Outcome, bool) {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return OutcomeSessionRejected, true
	case status == http.StatusRequestTimeout, status == http.StatusTooEarly:
		return OutcomeTemporaryFailure, true
	case status >= http.StatusInternalServerError:
		return OutcomeTemporaryFailure, true
	default:
		return OutcomeUnknown, false
	}
}

// ClassifySessionValidation classifies the authenticated profile call that proves
// a candidate session is accepted by the API tier.
// Source: Client._verify_token, which calls /userprofile-service/socialProfile.
//
// Any 2xx means the session works. 401 and 403 mean it was rejected, reported as
// OutcomeSessionRejected so the caller discards the token and tries the next
// strategy instead of blaming the password. Everything else is inconclusive:
// upstream keeps the token on a transient error, so a caller must not treat
// OutcomeTemporaryFailure or OutcomeUnknown here as a rejection.
func ClassifySessionValidation(r Response) Classification {
	c := Classification{Status: r.Status, RetryAfter: r.retryAfter()}

	if outcome, ok := statusOutcomeFor(contextSessionValidation, r.Status); ok {
		c.Outcome = outcome
		return c
	}
	if r.Status >= http.StatusOK && r.Status < http.StatusMultipleChoices {
		c.Outcome = OutcomeSuccess
	}
	return c
}
