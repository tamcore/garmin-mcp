package protocol

import (
	"net/http"
	"strings"
	"testing"
)

// Fuzz targets for the classifiers that turn a raw Garmin response into a
// verdict. Garmin's response bodies drift with server-side changes and a WAF
// interstitial can put anything in them, so these are the parsers most exposed
// to untrusted input in this package. No target performs I/O: each one builds a
// Response from in-memory bytes with NewResponseFromParts and classifies it.

// assertClassification checks the invariants every Classify* function must
// hold, whatever bytes produced it: the outcome is one of the declared
// constants, a service ticket is only ever reported alongside OutcomeSuccess,
// and every sealed accessor answers without panicking.
func assertClassification(t *testing.T, c Classification) {
	t.Helper()

	label := c.Outcome().String()
	if strings.HasPrefix(label, "invalid_outcome(") {
		t.Fatalf("classification produced an invalid outcome: %s", label)
	}

	if ticket := c.ServiceTicket(); ticket != "" && c.Outcome() != OutcomeSuccess {
		t.Fatalf("classification carries a service ticket but outcome is %s, not success", label)
	}

	_ = c.Status()
	_ = c.MFAMethod()
	_ = c.MFADeliveryUncertain()
	_ = c.CSRFToken()
	_ = c.PageTitle()
	_ = c.RetryAfter()
	_ = c.ResponseStatusType()
	_, _ = c.WidgetMFA()
	_ = c.Err(OpMobileLogin, EndpointWidgetRequestMFACode, nil)
}

// FuzzClassifyJSONLogin exercises the mobile/portal JSON login and MFA verify
// classifiers, including the nested "error"."status-code" probe and the
// responseStatus.type token scan. ClassifyMFAVerifyJSON is included because it
// shares the same parser and reinterprets one of its outcomes.
func FuzzClassifyJSONLogin(f *testing.F) {
	f.Add(200, `{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"abc"}`)
	f.Add(200, `{"responseStatus":{"type":"MFA_REQUIRED"},"customerMfaInfo":{"mfaLastMethodUsed":"EMAIL"}}`)
	f.Add(200, `{"error":{"status-code":"429"}}`)
	f.Add(403, `not json`)
	f.Add(429, ``)
	f.Add(200, `{"responseStatus":{"type":"SOME_UNKNOWN_INVALID_CREDENTIALS_VARIANT"}}`)
	f.Add(200, `{"responseStatus":{"type":"INVALID_USERNAME_PASSWORD"}}`)
	f.Fuzz(func(t *testing.T, status int, body string) {
		r := NewResponseFromParts(status, "application/json", nil, []byte(body))
		assertClassification(t, ClassifyJSONLogin(r))
		assertClassification(t, ClassifyMFAVerifyJSON(r))
	})
}

// FuzzClassifyWidgetPages exercises the HTML scraping regexes shared by the
// widget sign-in page, credential/OTP POST and MFA-verify classifiers: the
// CSRF token, the page title, the CAS service ticket, and the inline MFA JS
// variables.
func FuzzClassifyWidgetPages(f *testing.F) {
	f.Add(`<html><head><title>Success</title></head><body>?ticket=ST-abc123</body></html>`)
	f.Add(`<html><head><title>Authentication Application</title></head><body>
		var customerGuid = "abc-123"; var mfaMethod = "sms"; var locale = "en_US";
		var clientId = "GarminConnect"; var codeSentTo = "";
	</body></html>`)
	f.Add(`<form><input name="_csrf" value="tok"></form>`)
	f.Add(`<title>Account Locked</title>`)
	f.Add(`<title>Invalid</title>`)
	f.Add(``)
	f.Fuzz(func(t *testing.T, body string) {
		raw := []byte(body)
		assertClassification(t, ClassifyWidgetSignInPage(NewResponseFromParts(http.StatusOK, "text/html", nil, raw)))
		assertClassification(t, ClassifyWidgetLogin(NewResponseFromParts(http.StatusOK, "text/html", nil, raw)))
		assertClassification(t, ClassifyMFAVerifyWidget(NewResponseFromParts(http.StatusOK, "text/html", nil, raw)))
	})
}

// FuzzParseWidgetMFAVars exercises the inline JS variable parser directly,
// including its repeated-variable-disagreement rule.
func FuzzParseWidgetMFAVars(f *testing.F) {
	f.Add(`var customerGuid = "g"; var mfaMethod = "email"; var clientId = "c";`)
	f.Add(`var customerGuid = "g"; var customerGuid = "h";`)
	f.Add(``)
	f.Fuzz(func(t *testing.T, html string) {
		req, ok := parseWidgetMFAVars(html)
		if !ok {
			return
		}
		_ = req.Deliverable()
		_ = req.String()
	})
}
