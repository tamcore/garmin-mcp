package protocol

import (
	"net/http"
	"regexp"
	"strings"
)

// HTML scraping patterns for the embedded SSO widget flow.
// Source: _CSRF_RE, _TITLE_RE and the inline ticket regex in client.py (0.3.10).
var (
	csrfPattern   = regexp.MustCompile(`name="_csrf"\s+value="([^"]*)"`)
	titlePattern  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	ticketPattern = regexp.MustCompile(`\?ticket=(ST-[^"&\s'<]+)`)
)

// Widget page title hints, lowercased. Source: the title_lower checks in
// _widget_web_login (client.py, 0.3.10), which tests for "bad gateway",
// "service unavailable", "cloudflare", "502", "503"; then "locked", "invalid",
// "incorrect", "account error"; then "unable to sign in"/"unable to login"; then
// "mfa"/"authentication application".
//
// Two deliberate deviations from upstream: Cloudflare interstitials are reported
// as OutcomeBotChallenge rather than folded into the server-error branch, because
// they need a different remedy than a retry; and "gateway timeout"/"504" are
// added to the temporary set as the missing member of the 502/503 family.
//
// Every hint is matched as a delimited word or phrase, never as a substring:
// substring matching read "unlocked" as "locked" and "invalidated" as "invalid".
var (
	titleHintsBotChallenge = [...]string{"cloudflare", "attention required", "just a moment"}
	titleHintsTemporary    = [...]string{"bad gateway", "service unavailable", "gateway timeout", "502", "503", "504"}
	titleHintsLocked       = [...]string{"locked"}
	titleHintsInvalid      = [...]string{"invalid", "incorrect", "account error"}
	titleHintsRestricted   = [...]string{"unable to sign in", "unable to login"}
	titleHintsMFA          = [...]string{"mfa", "authentication application"}
)

const (
	// titleSuccess is the exact widget title that precedes a service ticket.
	// Source: the `title != "Success"` check in _widget_web_login.
	titleSuccess = "Success"

	// titleHintMFAUncertainOTP marks the email-OTP page, whose delivery upstream
	// cannot confirm because no JavaScript ran.
	titleHintMFAUncertainOTP = "authentication application"
)

// ExtractCSRFToken returns the _csrf hidden form value from an HTML document.
func ExtractCSRFToken(body []byte) (string, bool) {
	match := csrfPattern.FindSubmatch(body)
	if match == nil {
		return "", false
	}
	token := sanitizeToken(string(match[1]), MaxServiceTicketLen)
	return token, token != ""
}

// ExtractPageTitle returns the sanitized, length-bounded <title> text, or "".
func ExtractPageTitle(body []byte) string {
	match := titlePattern.FindSubmatch(body)
	if match == nil {
		return ""
	}
	return sanitizeTitle(string(match[1]))
}

// ExtractServiceTicket returns the CAS service ticket carried in a redirect URL.
// Only ST- prefixed tickets are accepted, so a ticket-granting cookie value can
// never be mistaken for one.
func ExtractServiceTicket(body []byte) (string, bool) {
	match := ticketPattern.FindSubmatch(body)
	if match == nil {
		return "", false
	}
	ticket := sanitizeToken(string(match[1]), MaxServiceTicketLen)
	return ticket, ticket != ""
}

// ClassifyWidgetSignInPage classifies the widget embed and sign-in GET pages,
// whose only success condition is a usable _csrf token.
// Source: _widget_web_login steps 1 and 2.
func ClassifyWidgetSignInPage(r Response) Classification {
	f := newWidgetFields(r, contextWidgetPage)
	if f.outcome != OutcomeUnknown {
		return newClassification(f)
	}
	if f.csrfToken != "" {
		f.outcome = OutcomeSuccess
	}
	return newClassification(f)
}

// ClassifyWidgetLogin classifies the HTML response to a widget credential or OTP
// POST, using the page title heuristics upstream relies on.
// Source: _widget_web_login step 3 and _complete_mfa_widget.
//
// The page's inline JS variables (mfaMethod, customerGuid, locale, clientId,
// codeSentTo) are parsed as well as the title, because the title says only that some
// MFA is required while the variables say which method and whether a code is already
// on its way. Classification.WidgetMFA carries them; requesting delivery is the auth
// package's job, since this package performs no I/O.
func ClassifyWidgetLogin(r Response) Classification {
	f := newWidgetFields(r, contextLoginPOST)
	if f.outcome != OutcomeUnknown {
		return newClassification(f)
	}

	title := strings.ToLower(f.pageTitle)
	switch {
	case containsAnyWordPhrase(title, titleHintsBotChallenge[:]...):
		f.outcome = OutcomeBotChallenge
	case containsAnyWordPhrase(title, titleHintsTemporary[:]...):
		f.outcome = OutcomeTemporaryFailure
	case containsAnyWordPhrase(title, titleHintsLocked[:]...):
		f.outcome = OutcomeAccountLocked
	case containsAnyWordPhrase(title, titleHintsInvalid[:]...):
		f.outcome = OutcomeInvalidCredentials
	case containsAnyWordPhrase(title, titleHintsRestricted[:]...):
		// A Garmin child/family account cannot use web SSO. Upstream logs this
		// and lets the remaining strategies try.
		f.outcome = OutcomeAccountRestricted
	case containsAnyWordPhrase(title, titleHintsMFA[:]...):
		f.outcome = OutcomeMFARequired
		applyWidgetMFAVars(&f, r, title)
	case f.pageTitle == titleSuccess:
		if ticket, ok := ExtractServiceTicket(r.body()); ok {
			f.outcome = OutcomeSuccess
			f.serviceTicket = ticket
		}
	}
	return newClassification(f)
}

// newWidgetFields collects the verdict fields shared by both widget classifiers
// and applies the HTTP-status verdicts that outrank any page content.
func newWidgetFields(r Response, ctx statusContext) classificationFields {
	status := r.Status()
	f := classificationFields{
		status:     status,
		retryAfter: r.retryAfter(),
		pageTitle:  ExtractPageTitle(r.body()),
	}
	if csrf, ok := ExtractCSRFToken(r.body()); ok {
		f.csrfToken = csrf
	}

	if status >= http.StatusBadRequest {
		if outcome, ok := statusOutcomeFor(ctx, status); ok {
			f.outcome = outcome
		}
	}
	return f
}

// applyWidgetMFAVars records what the page says about code delivery.
//
// The parsed method outranks the title guess: a title matching "authentication
// application" is an authenticator app, which has nothing to deliver, and guessing
// email for it would make this server ask Garmin to send a message it never sends.
// A page carrying no variables keeps the old behaviour, which is to guess email and
// admit the delivery is unconfirmed.
func applyWidgetMFAVars(f *classificationFields, r Response, title string) {
	request, ok := parseWidgetMFAVars(string(r.body()))
	if !ok {
		// No variables: the title is all there is, so the previous rule stands
		// unchanged rather than being tightened on a page this server cannot read
		// any better than it could before.
		f.mfaMethod = MFAMethodEmail
		f.mfaDeliveryUncertain = containsWordPhrase(title, titleHintMFAUncertainOTP)
		return
	}

	f.widgetMFA, f.widgetMFAFound = request, true
	if method := request.Method(); method != "" {
		f.mfaMethod = method
	} else {
		f.mfaMethod = MFAMethodEmail
	}
	// Uncertainty is about a code that should arrive and might not. An
	// authenticator app has no code in flight, so reporting its delivery as
	// uncertain would tell the caller to wait for a message Garmin never sends;
	// a page naming where a code went has confirmed delivery itself. What is left
	// uncertain is exactly the deliverable case, until the auth package asks.
	f.mfaDeliveryUncertain = request.Deliverable()
}
