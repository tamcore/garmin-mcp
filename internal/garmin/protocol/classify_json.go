package protocol

import (
	"encoding/json"
	"net/http"
	"strings"
)

// MaxResponseStatusTypeLen bounds the responseStatus.type token kept for
// diagnostics.
const MaxResponseStatusTypeLen = 64

// HeaderRetryAfter is the response header carrying a rate-limit hint.
const HeaderRetryAfter = "Retry-After"

// ClassifyJSONLogin classifies a response from the mobile/iOS or portal JSON
// login and MFA verify APIs. Source: the shared handling in _do_mobile_login,
// _do_portal_web_login and _complete_mfa (client.py, 0.3.10).
func ClassifyJSONLogin(r Response) Classification {
	status := r.Status()
	f := classificationFields{status: status, retryAfter: r.retryAfter()}

	// HTTP-level verdicts that upstream checks before touching the body.
	switch status {
	case http.StatusTooManyRequests:
		f.outcome = OutcomeRateLimited
		return newClassification(f)
	case http.StatusForbidden:
		f.outcome = OutcomeBotChallenge
		return newClassification(f)
	}

	if payload, ok := decodeLoginPayload(r); ok {
		f = applyLoginPayload(f, payload)
	}

	if f.outcome == OutcomeUnknown {
		if outcome, ok := statusOutcomeFor(contextLoginPOST, status); ok {
			f.outcome = outcome
		}
	}
	return newClassification(f)
}

// ClassifyMFAVerifyJSON classifies a response from the mobile or portal
// verifyCode API — an explicit OTP-verification response, never a credential
// POST. It reuses ClassifyJSONLogin's parsing and then reinterprets an
// OutcomeInvalidCredentials verdict as OutcomeMFARejected: this endpoint is never
// sent a password, so a definitive rejection here can only be about the
// one-time code.
//
// Every other outcome — success, a repeated MFA_REQUIRED, account lockout, bot
// challenge, rate limit, temporary failure or unknown — passes through
// unchanged, so a lockout stays a lockout rather than reading as a code retry.
func ClassifyMFAVerifyJSON(r Response) Classification {
	c := ClassifyJSONLogin(r)
	if c.Outcome() == OutcomeInvalidCredentials {
		return c.withOutcome(OutcomeMFARejected)
	}
	return c
}

type loginPayload struct {
	ResponseStatus struct {
		Type string `json:"type"`
	} `json:"responseStatus"`
	ServiceTicketID string `json:"serviceTicketId"`
	CustomerMFAInfo struct {
		MFALastMethodUsed string `json:"mfaLastMethodUsed"`
	} `json:"customerMfaInfo"`
	Error json.RawMessage `json:"error"`
}

func decodeLoginPayload(r Response) (loginPayload, bool) {
	body := strings.TrimSpace(string(r.body()))
	if body == "" {
		return loginPayload{}, false
	}
	if !strings.HasPrefix(body, "{") && !isJSONContentType(r.contentType()) {
		return loginPayload{}, false
	}

	var payload loginPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return loginPayload{}, false
	}
	return payload, true
}

func applyLoginPayload(f classificationFields, payload loginPayload) classificationFields {
	out := f
	out.responseStatusType = sanitizeToken(payload.ResponseStatus.Type, MaxResponseStatusTypeLen)

	// A 429 can be reported inside an otherwise successful HTTP response.
	// Source: res["error"]["status-code"] == "429".
	if embeddedStatusCode(payload.Error) == "429" {
		out.outcome = OutcomeRateLimited
		return out
	}

	switch outcome := outcomeForStatusType(out.responseStatusType); outcome {
	case OutcomeSuccess:
		ticket := sanitizeToken(payload.ServiceTicketID, MaxServiceTicketLen)
		if ticket == "" {
			// SUCCESSFUL without a usable ticket is not a usable session.
			return out
		}
		out.outcome = OutcomeSuccess
		out.serviceTicket = ticket
	case OutcomeMFARequired:
		out.outcome = OutcomeMFARequired
		out.mfaMethod = mfaMethodOrDefault(payload.CustomerMFAInfo.MFALastMethodUsed)
	default:
		out.outcome = outcome
	}
	return out
}

func embeddedStatusCode(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var body struct {
		StatusCode string `json:"status-code"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return ""
	}
	return body.StatusCode
}

// knownStatusType maps the responseStatus.type values upstream acts on. Source:
// the resp_type comparisons in _do_mobile_login, _do_portal_web_login and
// _complete_mfa (client.py, 0.3.10): SUCCESSFUL, MFA_REQUIRED,
// INVALID_USERNAME_PASSWORD and CAPTCHA_REQUIRED. ACCOUNT_LOCKED is added because
// the widget flow treats a lockout as definitive.
func knownStatusType(statusType string) (Outcome, bool) {
	switch statusType {
	case "SUCCESSFUL":
		return OutcomeSuccess, true
	case "MFA_REQUIRED":
		return OutcomeMFARequired, true
	case "INVALID_USERNAME_PASSWORD":
		return OutcomeInvalidCredentials, true
	case "CAPTCHA_REQUIRED":
		return OutcomeBotChallenge, true
	case "ACCOUNT_LOCKED":
		return OutcomeAccountLocked, true
	default:
		return OutcomeUnknown, false
	}
}

// statusTypeCredentialQualifiers name the subject of an INVALID_* token that
// makes it a credential rejection rather than, say, INVALID_TOKEN.
var statusTypeCredentialQualifiers = [...]string{"CREDENTIAL", "CREDENTIALS", "USERNAME", "PASSWORD", "EMAIL"}

// outcomeForStatusType maps a responseStatus.type token. Known values are matched
// exactly. Unknown values fall back to a token-aware scan of the underscore
// separated parts, so Garmin can add a variant without this classifier failing
// open — but substring matching is deliberately avoided: it read UNLOCKED as
// LOCKED and stopped the strategy chain on a healthy account. Anything still
// unrecognized stays OutcomeUnknown.
func outcomeForStatusType(statusType string) Outcome {
	if statusType == "" {
		return OutcomeUnknown
	}
	if outcome, ok := knownStatusType(statusType); ok {
		return outcome
	}

	parts := make(map[string]bool)
	for part := range strings.SplitSeq(statusType, "_") {
		parts[part] = true
	}

	if parts["LOCKED"] {
		return OutcomeAccountLocked
	}
	if parts["INVALID"] && hasAnyPart(parts, statusTypeCredentialQualifiers[:]...) {
		return OutcomeInvalidCredentials
	}
	return OutcomeUnknown
}

func hasAnyPart(parts map[string]bool, candidates ...string) bool {
	for _, candidate := range candidates {
		if parts[candidate] {
			return true
		}
	}
	return false
}

func isJSONContentType(value string) bool {
	return strings.Contains(strings.ToLower(value), "json")
}
