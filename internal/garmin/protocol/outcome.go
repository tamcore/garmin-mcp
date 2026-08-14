package protocol

import "strconv"

// Outcome is the classified meaning of one Garmin login or MFA response.
//
// The taxonomy reproduces the distinctions the upstream login chain makes:
// definitive credential failures stop the fallback chain, while rate limiting,
// bot challenges and transport errors let the next strategy try.
type Outcome int

const (
	// OutcomeUnknown means the response matched no known shape. Callers must not
	// treat it as authentication success or failure.
	OutcomeUnknown Outcome = iota

	// OutcomeSuccess means Garmin issued a CAS service ticket.
	// Source: responseStatus.type == "SUCCESSFUL", or widget title "Success".
	OutcomeSuccess

	// OutcomeMFARequired means an OTP must be submitted to a verify endpoint.
	// Source: responseStatus.type == "MFA_REQUIRED", or widget MFA page titles.
	OutcomeMFARequired

	// OutcomeInvalidCredentials is a definitive credential rejection.
	// Source: responseStatus.type == "INVALID_USERNAME_PASSWORD".
	OutcomeInvalidCredentials

	// OutcomeAccountLocked means the account itself is blocked, so retrying with
	// another strategy cannot help. Source: the "locked" widget title hint.
	OutcomeAccountLocked

	// OutcomeAccountRestricted means the account may not use this flow, for
	// example a Garmin child/family account on web SSO. Source: the
	// "unable to sign in"/"unable to login" widget title hints, which upstream
	// lets fall through to the remaining strategies.
	OutcomeAccountRestricted

	// OutcomeBotChallenge covers CAPTCHA, WAF interstitials and plain 403.
	// Source: HTTP 403 handling and responseStatus.type == "CAPTCHA_REQUIRED".
	OutcomeBotChallenge

	// OutcomeRateLimited is HTTP 429, or a 429 reported inside a JSON error body.
	// Source: the status_code == 429 checks and error["status-code"] == "429".
	OutcomeRateLimited

	// OutcomeTemporaryFailure is a transport or server-side error worth retrying.
	// Source: 5xx responses and the "bad gateway"/"service unavailable" hints.
	OutcomeTemporaryFailure
)

// Stable labels reused by Outcome.String and the error renderer.
const (
	labelUnknown     = "unknown"
	labelRateLimited = "rate_limited"
)

// String returns a stable snake_case label safe for logs and metrics.
func (o Outcome) String() string {
	switch o {
	case OutcomeUnknown:
		return labelUnknown
	case OutcomeSuccess:
		return "success"
	case OutcomeMFARequired:
		return "mfa_required"
	case OutcomeInvalidCredentials:
		return "invalid_credentials"
	case OutcomeAccountLocked:
		return "account_locked"
	case OutcomeAccountRestricted:
		return "account_restricted"
	case OutcomeBotChallenge:
		return "bot_challenge"
	case OutcomeRateLimited:
		return labelRateLimited
	case OutcomeTemporaryFailure:
		return "temporary_failure"
	default:
		return "invalid_outcome(" + strconv.Itoa(int(o)) + ")"
	}
}

// Retryable reports whether the same request may be retried later, subject to
// backoff and any Retry-After hint. Credential and MFA outcomes are never
// retried automatically.
func (o Outcome) Retryable() bool {
	return o == OutcomeRateLimited || o == OutcomeTemporaryFailure
}

// StopsFallback reports whether the login strategy chain must stop instead of
// trying the next transport. Source: upstream re-raises authentication errors
// and MFA sentinels, but continues past 429, HTML challenges and transport
// errors.
func (o Outcome) StopsFallback() bool {
	switch o {
	case OutcomeSuccess, OutcomeMFARequired, OutcomeInvalidCredentials, OutcomeAccountLocked:
		return true
	default:
		return false
	}
}
