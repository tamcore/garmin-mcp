package loginweb_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// postWith sends one form to the remote profile with exactly the capability cookie
// named, and no jar, so a test can present another transaction's cookie deliberately.
func (h *remoteHarness) postWith(path, capability string, form url.Values) (*http.Response, string) {
	h.t.Helper()

	req, err := http.NewRequestWithContext(h.t.Context(), http.MethodPost,
		h.b.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		h.t.Fatalf("build the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if capability != "" {
		req.AddCookie(&http.Cookie{Name: loginweb.RemoteCookieName, Value: capability})
	}

	resp, err := newBrowserClient(h.t).Do(req)
	if err != nil {
		h.t.Fatalf("POST %s: %v", path, err)
	}
	return resp, readBody(h.t, resp)
}

// TestUnsolicitedRemoteCredentialsAreRefused covers the fixed-route rule: without a
// valid transaction cookie and form token the route discloses nothing, and nothing
// reaches Garmin. Discoverability is not a security boundary.
func TestUnsolicitedRemoteCredentialsAreRefused(t *testing.T) {
	tests := []struct {
		name    string
		started bool
		token   string
	}{
		{name: "no cookie and no token"},
		{name: "no cookie but a real-looking token", token: wrongToken},
		{name: "a started transaction but no token", started: true},
		{name: "a started transaction and a wrong token", started: true, token: wrongToken},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})
			capability := ""
			if tc.started {
				capability = capabilityCookie(h.authorize())
			}

			resp, body := h.postWith(pathCredentials, capability, url.Values{
				fieldCSRF:     {tc.token},
				fieldEmail:    {testEmail},
				fieldPassword: {testPassword},
			})

			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404", resp.StatusCode)
			}
			if h.garmin.logins != 0 {
				t.Error("unsolicited credentials reached Garmin")
			}
			if strings.Contains(body, testEmail) {
				t.Error("the refusal page echoes the submitted account")
			}
		})
	}
}

// TestACrossClientTransactionCookieIsRefused pairs one transaction's capability with
// another transaction's form token. Neither transaction advances, and the refusal
// discloses nothing about the other client.
func TestACrossClientTransactionCookieIsRefused(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	alpha := capabilityCookie(h.authorize())
	beta := newBrowser(t, h.server.Handler())
	betaResp, _ := beta.get(pathAuthorize + "?" + authorizeQuery(testOtherClient).Encode())
	if betaResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the second authorization = %d, want 303", betaResp.StatusCode)
	}
	_, betaPage := beta.get(pathLogin)

	resp, body := h.postWith(pathLogin, alpha, url.Values{fieldCSRF: {csrfToken(t, betaPage)}})

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a cross-client cookie and token = %d, want 404", resp.StatusCode)
	}
	if strings.Contains(body, testOtherClient) {
		t.Error("the refusal page discloses the other client")
	}
	if attaches, grants, _ := h.authz.counts(); attaches != 0 || grants != 0 {
		t.Errorf("attaches=%d grants=%d, want 0/0", attaches, grants)
	}
}

// TestATerminalTransactionIsUnusableImmediately covers the replay case: once consent
// is granted the capability, the cookie and every route stop working, and no second
// code is issued.
func TestATerminalTransactionIsUnusableImmediately(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})

	authorized := h.authorize()
	capability := capabilityCookie(authorized)
	h.submitRemoteCredentials(h.continueToCredentials())
	_, page := h.b.get(pathConsent)

	granted := h.decide(page, decisionAllow)
	wantStatus(t, granted, http.StatusSeeOther, "POST "+pathConsent)

	replay, body := h.postWith(pathConsent, capability,
		url.Values{fieldCSRF: {csrfToken(t, page)}, fieldDecision: {decisionAllow}})
	if replay.StatusCode != http.StatusNotFound {
		t.Errorf("a replayed capability = %d, want 404", replay.StatusCode)
	}
	if strings.Contains(body, testCodeParam) {
		t.Error("the replay page carries an authorization code")
	}
	if _, grants, _ := h.authz.counts(); grants != 1 {
		t.Errorf("grants = %d, want exactly 1", grants)
	}
}

// TestAnExpiredTransactionIsRefused proves the absolute lifetime is not extended by
// activity: past the deadline every route refuses and says nothing about an account.
func TestAnExpiredTransactionIsRefused(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})
	h.authorize()
	form := h.continueToCredentials()

	h.clock.Advance(loginweb.DefaultTTL + time.Minute)

	resp, body := h.b.post(pathCredentials, url.Values{
		fieldCSRF:     {csrfToken(t, form)},
		fieldEmail:    {testEmail},
		fieldPassword: {testPassword},
	})

	if resp.StatusCode != http.StatusGone && resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 410 or 404", resp.StatusCode)
	}
	if h.garmin.logins != 0 {
		t.Error("an expired transaction still reached Garmin")
	}
	if strings.Contains(body, testEmail) {
		t.Error("the expiry page echoes the submitted account")
	}
}

// TestTheRemoteAttemptBudgetIsBounded stops credential guessing: after the budget the
// transaction refuses, whatever is submitted.
func TestTheRemoteAttemptBudgetIsBounded(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginErr: errLoginRejected})
	h.authorize()
	form := h.continueToCredentials()

	var status int
	for range loginweb.DefaultMaxAttempts + 1 {
		resp, body := h.b.post(pathCredentials, url.Values{
			fieldCSRF:     {csrfToken(t, form)},
			fieldEmail:    {testEmail},
			fieldPassword: {testPassword},
		})
		status = resp.StatusCode
		if status == http.StatusTooManyRequests {
			break
		}
		form = body
	}

	if status != http.StatusTooManyRequests {
		t.Fatalf("the last attempt = %d, want 429", status)
	}
	if h.garmin.logins > loginweb.DefaultMaxAttempts {
		t.Errorf("Garmin saw %d attempts, want at most %d",
			h.garmin.logins, loginweb.DefaultMaxAttempts)
	}
}

// TestRemoteBodyAndFieldBoundsApplyBeforeParsing keeps an oversized body from being
// read and an over-long field from reaching Garmin.
func TestRemoteBodyAndFieldBoundsApplyBeforeParsing(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: remoteSucceeded()})
	h.authorize()
	form := h.continueToCredentials()

	resp, _ := h.b.post(pathCredentials, url.Values{
		fieldCSRF:     {csrfToken(t, form)},
		fieldEmail:    {testEmail},
		fieldPassword: {strings.Repeat("x", 2*loginweb.MaxRequestBytes)},
	})
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized body = %d, want 413", resp.StatusCode)
	}

	_, form = h.b.get(pathCredentials)
	resp, body := h.b.post(pathCredentials, url.Values{
		fieldCSRF:     {csrfToken(t, form)},
		fieldEmail:    {strings.Repeat("a", loginweb.MaxEmailLen+1) + "@example.test"},
		fieldPassword: {testPassword},
	})
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("an over-long field was accepted")
	}
	if strings.Contains(body, testPassword) {
		t.Error("the rejection page echoes the password")
	}
	if h.garmin.logins != 0 {
		t.Errorf("Garmin saw %d bounded-out submissions, want 0", h.garmin.logins)
	}
}

// TestRemoteMFARejectedCodeMayBeRetried covers the retryable half of the remote
// profile: a rejected code re-renders the OTP form, and a correct code afterward
// still completes the same transaction.
func TestRemoteMFARejectedCodeMayBeRetried(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{
		loginAttempt: challenged(),
		mfaResponses: []mfaResponse{
			{err: protocol.ErrMFARejected},
			{attempt: remoteSucceeded()},
		},
	})

	h.authorize()
	h.submitRemoteCredentials(h.continueToCredentials())
	_, mfaForm := h.b.get(pathMFA)

	resp, retryForm := h.b.post(pathMFA, url.Values{
		fieldCSRF: {csrfToken(t, mfaForm)},
		fieldCode: {"000000"},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST %s with a rejected code = %d, want 401", pathMFA, resp.StatusCode)
	}
	if !strings.Contains(retryForm, "not accepted") {
		t.Errorf("page = %q, want it to say the code was not accepted", retryForm)
	}

	resp, _ = h.b.post(pathMFA, url.Values{
		fieldCSRF: {csrfToken(t, retryForm)},
		fieldCode: {testCode},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("retry with the right code = %d, want 303", resp.StatusCode)
	}
	if h.garmin.mfaCalls != 2 {
		t.Errorf("CompleteMFA was called %d times, want 2", h.garmin.mfaCalls)
	}
}

// TestRemoteMFATerminalFailureAbandonsTheTransaction covers the terminal half: a
// failure that says nothing about the submitted code must end the transaction
// rather than offer a retry.
func TestRemoteMFATerminalFailureAbandonsTheTransaction(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{
		loginAttempt: challenged(),
		mfaErr:       protocol.ErrAccountLocked,
	})

	h.authorize()
	h.submitRemoteCredentials(h.continueToCredentials())
	_, mfaForm := h.b.get(pathMFA)

	resp, page := h.b.post(pathMFA, url.Values{
		fieldCSRF: {csrfToken(t, mfaForm)},
		fieldCode: {testCode},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST %s with a terminal failure = %d, want 404; body=%s", pathMFA, resp.StatusCode, page)
	}
	if strings.Contains(page, "not accepted") {
		t.Error("a terminal failure was rendered as a retryable rejected code")
	}

	// The transaction is discarded: the OTP page no longer exists.
	resp, _ = h.b.get(pathMFA)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s after a terminal failure = %d, want 404", pathMFA, resp.StatusCode)
	}
	if h.garmin.mfaCalls != 1 {
		t.Errorf("CompleteMFA was called %d times, want 1", h.garmin.mfaCalls)
	}
}
