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
//
// It is secret-bearing: Body and Header hold whatever Garmin sent, including
// Set-Cookie and Authorization values. String, GoString and MarshalJSON are
// implemented so printing or serializing a Response emits only its shape. Never
// hand these fields to a logger directly.
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
//
// It is secret-bearing: ServiceTicket, CSRFToken and PageTitle carry credential
// and page material. String, GoString and MarshalJSON are implemented so
// printing or serializing a Classification reports presence rather than content.
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
	// delivery is not confirmed. Upstream 0.3.10 removed the shelving this flag
	// once drove and instead requests delivery explicitly via
	// PathWidgetRequestMFACode; the flag is kept so a caller can decide.
	MFADeliveryUncertain bool
	// CSRFToken is the _csrf form value found in an HTML page, if any.
	CSRFToken string
	// PageTitle is the sanitized, length-bounded HTML <title>.
	PageTitle string
	// RetryAfter is the parsed Retry-After hint, or 0 when absent.
	RetryAfter time.Duration
	// ResponseStatusType is the sanitized responseStatus.type token, kept for
	// diagnostics when the value is not recognized. Only recognized values reach
	// a rendered form.
	ResponseStatusType string
}

// Err returns nil for OutcomeSuccess and a *Error otherwise, wrapping cause.
// OutcomeMFARequired is reported as an error too, so callers can match it with
// errors.Is(err, ErrMFARequired) and route to the OTP step.
//
// op and endpoint are typed labels: pass an Op* and an Endpoint* constant. Any
// other value renders as "unknown".
func (c Classification) Err(op Op, endpoint Endpoint, cause error) error {
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
// _do_portal_web_login and _complete_mfa (client.py, 0.3.10).
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
		if outcome, ok := statusOutcomeFor(contextLoginPOST, r.Status); ok {
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
