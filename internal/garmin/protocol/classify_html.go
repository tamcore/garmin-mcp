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
	c := newWidgetClassification(r, contextWidgetPage)
	if c.Outcome != OutcomeUnknown {
		return c
	}
	if c.CSRFToken != "" {
		c.Outcome = OutcomeSuccess
	}
	return c
}

// ClassifyWidgetLogin classifies the HTML response to a widget credential or OTP
// POST, using the page title heuristics upstream relies on.
// Source: _widget_web_login step 3 and _complete_mfa_widget.
//
// Documented gap: upstream 0.3.10 no longer decides MFA from the title alone. It
// also parses the page's inline JS variables (mfaMethod, customerGuid, locale,
// clientId, codeSentTo) and requires a non-empty mfaMethod before treating an
// "authentication application" page as an MFA challenge, then explicitly requests
// code delivery. That variable parsing and the per-method delivery request are
// not implemented here; only PathWidgetRequestMFACode is ported.
func ClassifyWidgetLogin(r Response) Classification {
	c := newWidgetClassification(r, contextLoginPOST)
	if c.Outcome != OutcomeUnknown {
		return c
	}

	title := strings.ToLower(c.PageTitle)
	switch {
	case containsAnyWordPhrase(title, titleHintsBotChallenge[:]...):
		c.Outcome = OutcomeBotChallenge
	case containsAnyWordPhrase(title, titleHintsTemporary[:]...):
		c.Outcome = OutcomeTemporaryFailure
	case containsAnyWordPhrase(title, titleHintsLocked[:]...):
		c.Outcome = OutcomeAccountLocked
	case containsAnyWordPhrase(title, titleHintsInvalid[:]...):
		c.Outcome = OutcomeInvalidCredentials
	case containsAnyWordPhrase(title, titleHintsRestricted[:]...):
		// A Garmin child/family account cannot use web SSO. Upstream logs this
		// and lets the remaining strategies try.
		c.Outcome = OutcomeAccountRestricted
	case containsAnyWordPhrase(title, titleHintsMFA[:]...):
		c.Outcome = OutcomeMFARequired
		c.MFAMethod = MFAMethodEmail
		// Scraped HTML cannot confirm that Garmin actually sent an OTP.
		c.MFADeliveryUncertain = containsWordPhrase(title, titleHintMFAUncertainOTP)
	case c.PageTitle == titleSuccess:
		if ticket, ok := ExtractServiceTicket(r.Body); ok {
			c.Outcome = OutcomeSuccess
			c.ServiceTicket = ticket
		}
	}
	return c
}

// newWidgetClassification collects the fields shared by both widget classifiers
// and applies the HTTP-status verdicts that outrank any page content.
func newWidgetClassification(r Response, ctx statusContext) Classification {
	c := Classification{
		Status:     r.Status,
		RetryAfter: r.retryAfter(),
		PageTitle:  ExtractPageTitle(r.Body),
	}
	if csrf, ok := ExtractCSRFToken(r.Body); ok {
		c.CSRFToken = csrf
	}

	if r.Status >= http.StatusBadRequest {
		if outcome, ok := statusOutcomeFor(ctx, r.Status); ok {
			c.Outcome = outcome
		}
	}
	return c
}
