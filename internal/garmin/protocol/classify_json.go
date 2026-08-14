package protocol

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// MaxResponseStatusTypeLen bounds the responseStatus.type token kept for
// diagnostics.
const MaxResponseStatusTypeLen = 64

// HeaderRetryAfter is the response header carrying a rate-limit hint.
const HeaderRetryAfter = "Retry-After"

// Response is the classifier input: one HTTP response, already read into memory.
type Response struct {
	// Status is the HTTP status code, or 0 when the request never completed.
	Status int
	// Header is the response header set. It is only read, never modified.
	Header http.Header
	// ContentType is the media type. When empty it is taken from Header.
	ContentType string
	// Body is the response body. The classifier never copies it into an error.
	Body []byte
	// Now is the reference instant for Retry-After HTTP-dates. The zero value
	// means time.Now().
	Now time.Time
}

// Classification is the immutable verdict for one response.
type Classification struct {
	// Outcome is the classified meaning of the response.
	Outcome Outcome
	// Status echoes the HTTP status of the classified response.
	Status int
	// ServiceTicket is the extracted CAS service ticket on success.
	ServiceTicket string
	// MFAMethod is the delivery method Garmin last used, on OutcomeMFARequired.
	MFAMethod string
	// MFADeliveryUncertain marks an MFA challenge scraped from HTML, where OTP
	// delivery is not confirmed. Source: the _mfa_delivery_uncertain flag set
	// for the widget "authentication application" page.
	MFADeliveryUncertain bool
	// CSRFToken is the _csrf form value found in an HTML page, if any.
	CSRFToken string
	// PageTitle is the sanitized, length-bounded HTML <title>.
	PageTitle string
	// RetryAfter is the parsed Retry-After hint, or 0 when absent.
	RetryAfter time.Duration
	// ResponseStatusType is the sanitized responseStatus.type token, kept for
	// diagnostics when the value is not recognized.
	ResponseStatusType string
}

// Err returns nil for OutcomeSuccess and a *Error otherwise, wrapping cause.
// OutcomeMFARequired is reported as an error too, so callers can match it with
// errors.Is(err, ErrMFARequired) and route to the OTP step.
func (c Classification) Err(op, endpoint string, cause error) error {
	if c.Outcome == OutcomeSuccess {
		return nil
	}
	return &Error{
		Op:         op,
		Endpoint:   endpoint,
		Status:     c.Status,
		Outcome:    c.Outcome,
		RetryAfter: c.RetryAfter,
		Err:        cause,
	}
}

// ClassifyJSONLogin classifies a response from the mobile/iOS or portal JSON
// login and MFA verify APIs. Source: the shared handling in _do_mobile_login,
// _do_portal_web_login and _complete_mfa.
func ClassifyJSONLogin(r Response) Classification {
	c := Classification{Status: r.Status, RetryAfter: r.retryAfter()}

	// HTTP-level verdicts that upstream checks before touching the body.
	switch r.Status {
	case http.StatusTooManyRequests:
		c.Outcome = OutcomeRateLimited
		return c
	case http.StatusForbidden:
		c.Outcome = OutcomeBotChallenge
		return c
	}

	if payload, ok := decodeLoginPayload(r); ok {
		c = applyLoginPayload(c, payload)
	}

	if c.Outcome == OutcomeUnknown {
		if outcome, ok := statusOutcome(r.Status); ok {
			c.Outcome = outcome
		}
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
	body := strings.TrimSpace(string(r.Body))
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

func applyLoginPayload(c Classification, payload loginPayload) Classification {
	out := c
	out.ResponseStatusType = sanitizeToken(payload.ResponseStatus.Type, MaxResponseStatusTypeLen)

	// A 429 can be reported inside an otherwise successful HTTP response.
	// Source: res["error"]["status-code"] == "429".
	if embeddedStatusCode(payload.Error) == "429" {
		out.Outcome = OutcomeRateLimited
		return out
	}

	switch outcome := outcomeForStatusType(out.ResponseStatusType); outcome {
	case OutcomeSuccess:
		ticket := sanitizeToken(payload.ServiceTicketID, MaxServiceTicketLen)
		if ticket == "" {
			// SUCCESSFUL without a usable ticket is not a usable session.
			return out
		}
		out.Outcome = OutcomeSuccess
		out.ServiceTicket = ticket
	case OutcomeMFARequired:
		out.Outcome = OutcomeMFARequired
		out.MFAMethod = mfaMethodOrDefault(payload.CustomerMFAInfo.MFALastMethodUsed)
	default:
		out.Outcome = outcome
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

// outcomeForStatusType maps a responseStatus.type token. Upstream recognizes
// SUCCESSFUL, MFA_REQUIRED, INVALID_USERNAME_PASSWORD and CAPTCHA_REQUIRED; the
// keyword checks additionally absorb the credential and lockout variants Garmin
// may add, instead of failing open.
func outcomeForStatusType(statusType string) Outcome {
	switch statusType {
	case "":
		return OutcomeUnknown
	case "SUCCESSFUL":
		return OutcomeSuccess
	case "MFA_REQUIRED":
		return OutcomeMFARequired
	case "CAPTCHA_REQUIRED":
		return OutcomeBotChallenge
	}

	if strings.Contains(statusType, "LOCKED") {
		return OutcomeAccountLocked
	}
	if strings.Contains(statusType, "INVALID") && containsAny(statusType, "CREDENTIAL", "USERNAME", "PASSWORD") {
		return OutcomeInvalidCredentials
	}
	return OutcomeUnknown
}

// statusOutcome maps an HTTP status to an outcome when the body said nothing
// usable. 401 is treated as a definitive rejection, matching the 401/403 check
// in Client._verify_token.
func statusOutcome(status int) (Outcome, bool) {
	switch {
	case status == http.StatusTooManyRequests:
		return OutcomeRateLimited, true
	case status == http.StatusForbidden:
		return OutcomeBotChallenge, true
	case status == http.StatusUnauthorized:
		return OutcomeInvalidCredentials, true
	case status == http.StatusRequestTimeout, status == http.StatusTooEarly:
		return OutcomeTemporaryFailure, true
	case status >= http.StatusInternalServerError:
		return OutcomeTemporaryFailure, true
	default:
		return OutcomeUnknown, false
	}
}

func (r Response) contentType() string {
	if r.ContentType != "" {
		return r.ContentType
	}
	if r.Header == nil {
		return ""
	}
	return r.Header.Get("Content-Type")
}

func (r Response) retryAfter() time.Duration {
	if r.Header == nil {
		return 0
	}
	delay, _ := ParseRetryAfter(r.Header.Get(HeaderRetryAfter), r.now())
	return delay
}

func (r Response) now() time.Time {
	if r.Now.IsZero() {
		return time.Now()
	}
	return r.Now
}

func isJSONContentType(value string) bool {
	return strings.Contains(strings.ToLower(value), "json")
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}
