package auth

// Observer receives login and refresh outcomes for metrics. A nil Observer is
// valid: every call site guards it. outcome is one of "ok", "error", or
// (login attempts only) "mfa-required", never an error string, which can
// embed a header or a URL.
type Observer interface {
	// TokenRefresh records one completed token refresh.
	TokenRefresh(outcome string)
	// LoginAttempt records one login strategy's attempt, keyed by the
	// strategy's own constant label.
	LoginAttempt(strategy, outcome string)
}

const (
	outcomeOK          = "ok"
	outcomeError       = "error"
	outcomeMFARequired = "mfa-required"
)
