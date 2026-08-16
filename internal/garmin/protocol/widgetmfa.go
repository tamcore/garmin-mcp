package protocol

import (
	"log/slog"
	"regexp"
	"strings"
)

// The MFA delivery methods Garmin can be asked to send a code by. Anything else —
// an authenticator app, above all — has nothing to deliver, so no request is made.
const (
	widgetMethodEmail = "email"
	widgetMethodSMS   = "sms"
)

// Field bounds. A widget page is served by Garmin, but a WAF interstitial or a
// tampered response can put anything in these variables, and everything here
// except the GUID is printable — a method or a client id is a log field. Bounding
// and shaping them is what stops a page moving the account identifier into a field
// that is not sealed.
const (
	maxWidgetGUIDLen   = 64
	maxWidgetMethodLen = 32
	maxWidgetLocaleLen = 16
	maxWidgetClientLen = 64
)

// widgetSafeValue is the shape a printable widget variable may have. It admits the
// values Garmin sends — "email", "sms", "en_US", "GarminConnect" — and refuses
// anything carrying punctuation an identifier would need.
var widgetSafeValue = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// widgetMFAVarPattern matches the inline JS variables Garmin's widget MFA page
// declares. Source: _parse_widget_mfa_vars in the pinned python-garminconnect,
// whose pattern this mirrors, including the tolerance for surrounding whitespace.
var widgetMFAVarPattern = regexp.MustCompile(
	`var\s+(customerGuid|mfaMethod|locale|clientId|codeSentTo)\s*=\s*"([^"]*)"\s*;`)

// A WidgetMFARequest is what the widget page says about delivering an OTP.
//
// It is identifier-bearing: customerGuid names the account. The material sits
// behind two pointers for the reason response.go states — fmt's badVerb path
// re-prints a value at depth 0 and dereferences a top-level pointer to a struct, so
// a method-stripping alias would otherwise print the account identifier verbatim.
// String, GoString and LogValue report presence rather than content.
//
// The zero value is inert and reports nothing deliverable.
type WidgetMFARequest struct {
	sealed *sealedWidgetMFA
}

// sealedWidgetMFA is the same deliberate extra indirection as sealedParts in
// response.go.
type sealedWidgetMFA struct {
	inner *widgetMFAParts
}

type widgetMFAParts struct {
	customerGUID string
	method       string
	locale       string
	clientID     string
	codeSentTo   string
}

func (w WidgetMFARequest) p() widgetMFAParts {
	if w.sealed == nil || w.sealed.inner == nil {
		return widgetMFAParts{}
	}
	return *w.sealed.inner
}

// CustomerGUID is the account identifier the request body carries. It names the
// account: put it in the body and nowhere else.
func (w WidgetMFARequest) CustomerGUID() string { return w.p().customerGUID }

// Method is the delivery method the page declared, lower-cased, or "" when the page
// declared something that is not a plain identifier.
//
// It is bounded and shaped rather than passed through: this value is logged, and a
// page that put the account identifier here would otherwise put it in a log line.
func (w WidgetMFARequest) Method() string {
	return safeWidgetValue(w.p().method, maxWidgetMethodLen)
}

// Locale is the page's locale, which Garmin uses to localise the message. It is
// bounded and shaped for the same reason Method is.
func (w WidgetMFARequest) Locale() string {
	return safeWidgetValue(w.p().locale, maxWidgetLocaleLen)
}

// ClientID is the SSO client the page was rendered for. It is a query parameter of
// the delivery request rather than part of its body, so it reaches access logs and
// Referer headers: it is bounded and shaped before it can.
func (w WidgetMFARequest) ClientID() string {
	return safeWidgetValue(w.p().clientID, maxWidgetClientLen)
}

// safeWidgetValue returns value when it is a plain bounded identifier, and ""
// otherwise. Refusing is safe: every caller treats an empty field as "do not send".
func safeWidgetValue(value string, limit int) string {
	if value == "" || len(value) > limit || !widgetSafeValue.MatchString(value) {
		return ""
	}
	return value
}

// CodeAlreadySent reports that the sign-in POST already delivered a code, which the
// page states by naming where it went.
func (w WidgetMFARequest) CodeAlreadySent() bool { return w.p().codeSentTo != "" }

// Deliverable reports whether asking Garmin to send a code is the right move: the
// method is one Garmin can deliver, and no code has been sent yet.
//
// An authenticator app is not deliverable — there is nothing to send — and neither
// is a page that already reports a code on its way, because asking again sends the
// account a second message and spends a rate-limit budget for nothing.
func (w WidgetMFARequest) Deliverable() bool {
	if w.CodeAlreadySent() {
		return false
	}
	// Every field the request needs must be present and usable. One variable on a
	// page is not a request: sending Garmin an empty customerGuid asks it to
	// deliver a code to nobody, and an over-long or punctuated GUID is a page this
	// server should not be building a request from at all.
	guid := w.p().customerGUID
	if guid == "" || len(guid) > maxWidgetGUIDLen || !widgetSafeValue.MatchString(guid) {
		return false
	}
	if w.ClientID() == "" {
		return false
	}
	switch w.Method() {
	case widgetMethodEmail, widgetMethodSMS:
		return true
	default:
		return false
	}
}

// String reports presence, never the account identifier.
func (w WidgetMFARequest) String() string {
	return "protocol.WidgetMFARequest{method:" + w.Method() +
		" deliverable:" + boolText(w.Deliverable()) + " guid:[redacted]}"
}

// GoString keeps %#v on the same redacted rendering as %v.
func (w WidgetMFARequest) GoString() string { return w.String() }

// LogValue reports the shape of the request and never its identifier.
func (w WidgetMFARequest) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("method", w.Method()),
		slog.Bool("deliverable", w.Deliverable()),
		slog.Bool("codeAlreadySent", w.CodeAlreadySent()),
	)
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// parseWidgetMFAVars extracts the inline variables, and reports whether the page
// declared any. A page that declares none yields the zero value, so a caller cannot
// send Garmin a request built from empty strings.
func parseWidgetMFAVars(html string) (WidgetMFARequest, bool) {
	matches := widgetMFAVarPattern.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return WidgetMFARequest{}, false
	}

	// A variable declared twice with two values makes the page ambiguous, and the
	// disagreement decides whether a code is requested at all — a second
	// codeSentTo would silence the request. An ambiguous page is treated as one
	// that declared nothing, so the caller falls back to the title heuristic
	// rather than acting on the reading that happened to come last.
	seen := make(map[string]string, len(matches))
	for _, match := range matches {
		name, value := match[1], match[2]
		if previous, repeated := seen[name]; repeated && previous != value {
			return WidgetMFARequest{}, false
		}
		seen[name] = value
	}

	parts := widgetMFAParts{
		customerGUID: seen["customerGuid"],
		method:       strings.ToLower(seen["mfaMethod"]),
		locale:       seen["locale"],
		clientID:     seen["clientId"],
		codeSentTo:   seen["codeSentTo"],
	}
	return WidgetMFARequest{sealed: &sealedWidgetMFA{inner: &parts}}, true
}
