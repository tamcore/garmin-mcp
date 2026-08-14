package loginweb_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// TestTheStylesheetIsServedFromThisOrigin keeps the one asset a page references
// local, which is what lets the policy forbid every other source.
func TestTheStylesheetIsServedFromThisOrigin(t *testing.T) {
	server := newServer(t, &fakeAuthenticator{})
	b := newBrowser(t, server.Handler())

	resp, body := b.get("/style.css")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /style.css = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", got)
	}
	if body == "" {
		t.Error("the stylesheet is empty")
	}
	for _, forbidden := range []string{schemeHTTP, schemeHTTPS, "@import"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the stylesheet pulls in %q", forbidden)
		}
	}
}

// TestTheFinalPageNeedsACompletedRun keeps the success page from becoming a probe for
// whether a login is in progress.
func TestTheFinalPageNeedsACompletedRun(t *testing.T) {
	fake := &fakeAuthenticator{loginAttempt: succeeded()}
	server := newServer(t, fake)
	b := newBrowser(t, server.Handler())

	b.get("/")
	if resp, _ := b.get("/done"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /done before the login = %d, want 404", resp.StatusCode)
	}

	submitCredentials(t, b)

	resp, page := b.get("/done")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /done after the login = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, "linked") {
		t.Errorf("the final page does not report the linked account:\n%s", page)
	}
}

// TestARejectedLoginCanBeRetriedUntilTheBudgetIsUsedUp covers the bounded retry: a
// wrong password re-renders the form with generic text and a fresh token, and the run
// ends once the budget is exhausted.
func TestARejectedLoginCanBeRetriedUntilTheBudgetIsUsedUp(t *testing.T) {
	fake := &fakeAuthenticator{loginErr: errLoginRejected}
	server, err := loginweb.New(loginweb.Config{Authenticator: fake, MaxAttempts: 2})
	if err != nil {
		t.Fatalf("loginweb.New returned error: %v", err)
	}

	b := newBrowser(t, server.Handler())
	_, form := b.get("/login")

	// The first attempt is refused by Garmin and offers a fresh form.
	resp, retryPage := b.post("/login", url.Values{
		fieldCSRF:     {csrfToken(t, form)},
		fieldEmail:    {testEmail},
		fieldPassword: {testPassword},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the first attempt = %d, want 401", resp.StatusCode)
	}
	if strings.Contains(retryPage, testPassword) {
		t.Error("the retry page echoes the password")
	}

	// The second attempt uses the rotated token and exhausts the budget.
	resp, _ = b.post("/login", url.Values{
		fieldCSRF:     {csrfToken(t, retryPage)},
		fieldEmail:    {testEmail},
		fieldPassword: {testPassword},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the second attempt = %d, want 401", resp.StatusCode)
	}

	// A third attempt has no budget left.
	resp, exhausted := b.post("/login", url.Values{
		fieldCSRF:     {csrfToken(t, retryPage)},
		fieldEmail:    {testEmail},
		fieldPassword: {testPassword},
	})
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("a submission was accepted after the attempt budget ran out")
	}
	if strings.Contains(exhausted, testEmail) {
		t.Error("the refusal page echoes the submitted account")
	}
	if fake.logins > 2 {
		t.Errorf("Garmin was called %d times, want at most the attempt budget", fake.logins)
	}
}

// TestAStaleFormTokenIsRefused proves the rotation has teeth: the token from an
// already-submitted form no longer works.
func TestAStaleFormTokenIsRefused(t *testing.T) {
	fake := &fakeAuthenticator{loginErr: errLoginRejected}
	server := newServer(t, fake)
	b := newBrowser(t, server.Handler())

	_, form := b.get("/login")
	stale := csrfToken(t, form)
	credentials := url.Values{
		fieldCSRF:     {stale},
		fieldEmail:    {testEmail},
		fieldPassword: {testPassword},
	}

	b.post("/login", credentials)
	resp, _ := b.post("/login", credentials)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a replayed form token = %d, want 404", resp.StatusCode)
	}
	if fake.logins != 1 {
		t.Errorf("Garmin was called %d times, want 1", fake.logins)
	}
}

// TestNewRefusesAnIncoherentConfiguration keeps a login server that cannot be
// configured coherently from binding a port at all.
func TestNewRefusesAnIncoherentConfiguration(t *testing.T) {
	if _, err := loginweb.New(loginweb.Config{}); !errors.Is(err, loginweb.ErrNoAuthenticator) {
		t.Errorf("error %v does not match ErrNoAuthenticator", err)
	}

	_, err := loginweb.New(loginweb.Config{Authenticator: &fakeAuthenticator{}, TTL: -1})
	if !errors.Is(err, loginweb.ErrInvalidConfig) {
		t.Errorf("error %v does not match ErrInvalidConfig", err)
	}
}
