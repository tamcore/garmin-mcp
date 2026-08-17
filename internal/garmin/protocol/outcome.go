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

	// OutcomeSessionRejected means the API tier refused a candidate session: the
	// DI token or JWT_WEB cookie is not accepted, which is account and region
	// dependent. It says nothing about the password, so the caller must discard
	// the token and let the next login strategy try. Source: the 401/403 check in
	// Client._verify_token (upstream issue #369).
	OutcomeSessionRejected

	// OutcomeMFARejected means Garmin rejected the submitted one-time code. It is
	// produced only by ClassifyMFAVerifyJSON and ClassifyMFAVerifyWidget, which
	// classify an explicit OTP-verification response and reinterpret what would
	// otherwise read as OutcomeInvalidCredentials: neither verify endpoint is ever
	// sent a password, so a definitive rejection there can only be about the code.
	//
	// This is not an upstream distinction: python-garminconnect 0.3.10 raises the
	// same GarminConnectAuthenticationError for a wrong password and a wrong OTP
	// (Client._complete_mfa and Client._complete_mfa_widget both fold every
	// non-success verdict into one error). This project adds the split because the
	// two failures need different handling downstream: a wrong password may let the
	// login strategy fall through, while a wrong OTP must leave the pending MFA
	// transaction alone so the same user can retry the code.
	//
	// It never appears during the credential POST, so it structurally cannot reach
	// StopsFallback: that method classifies only the outcomes login.go's credential
	// strategy chain can see, and ClassifyMFAVerifyJSON/ClassifyMFAVerifyWidget run
	// nowhere in that chain. It is deliberately absent from StopsFallback for that
	// reason, not because it should let a fallback continue past it.
	//
	// mfa.go's own verify-endpoint loop is a second, narrower fallback — mobile and
	// portal sit in different rate-limit buckets — and it enforces the stop
	// directly rather than through StopsFallback, because StopsFallback's signature
	// takes no information about which endpoint asked or what was submitted: a
	// definitive rejection or an account lockout on the flow's own endpoint ends
	// that loop before the alternate endpoint is ever asked, so a real wrong code
	// is never posted twice against Garmin's own OTP attempt counter.
	OutcomeMFARejected
)

// Stable labels reused by Outcome.String, the error renderer and the redacted
// formatters.
const (
	labelUnknown     = "unknown"
	labelRateLimited = "rate_limited"
	labelOther       = "other"
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
	case OutcomeSessionRejected:
		return "session_rejected"
	case OutcomeMFARejected:
		return "mfa_rejected"
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
