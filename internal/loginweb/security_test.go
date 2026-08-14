package loginweb_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// The URL schemes a page or a policy must never carry.
const (
	schemeHTTP  = "http://"
	schemeHTTPS = "https://"
)

// TestEveryResponseCarriesTheBrowserSecurityHeaders covers the headers the pages
// depend on: no framing, no sniffing, no referrer, no store, and a policy that
// permits nothing but this origin's own stylesheet and form target.
func TestEveryResponseCarriesTheBrowserSecurityHeaders(t *testing.T) {
	server := newServer(t, &fakeAuthenticator{loginAttempt: succeeded()})
	b := newBrowser(t, server.Handler())

	for _, path := range []string{"/", "/login", "/nothing-here"} {
		t.Run(path, func(t *testing.T) {
			resp, _ := b.get(path)

			want := map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "no-referrer",
				"Cache-Control":          "no-store",
				"X-Frame-Options":        "DENY",
			}
			for header, value := range want {
				if got := resp.Header.Get(header); got != value {
					t.Errorf("%s = %q, want %q", header, got, value)
				}
			}

			policy := resp.Header.Get("Content-Security-Policy")
			for _, directive := range []string{
				"default-src 'none'",
				"frame-ancestors 'none'",
				"form-action 'self'",
				"base-uri 'none'",
				"style-src 'self'",
			} {
				if !strings.Contains(policy, directive) {
					t.Errorf("Content-Security-Policy %q lacks %q", policy, directive)
				}
			}
			for _, forbidden := range []string{"unsafe-inline", schemeHTTP, schemeHTTPS} {
				if strings.Contains(policy, forbidden) {
					t.Errorf("Content-Security-Policy %q permits %q", policy, forbidden)
				}
			}
		})
	}
}

// TestPagesLoadNoThirdPartyAsset is the offline guarantee: no CDN, no font service,
// no tracker, and no script at all.
func TestPagesLoadNoThirdPartyAsset(t *testing.T) {
	server := newServer(t, &fakeAuthenticator{loginAttempt: challenged()})
	b := newBrowser(t, server.Handler())

	_, disclosure := b.get("/")
	_, login := b.get("/login")

	for _, page := range []string{disclosure, login} {
		for _, forbidden := range []string{schemeHTTP, schemeHTTPS, "<script", "//cdn", "fonts."} {
			if strings.Contains(page, forbidden) {
				t.Errorf("a page references %q:\n%s", forbidden, page)
			}
		}
	}
}

// TestCredentialFormIsAccessibleAndPasswordManagerCompatible keeps the form usable:
// labelled fields, the right autocomplete tokens, and a real password input.
func TestCredentialFormIsAccessibleAndPasswordManagerCompatible(t *testing.T) {
	server := newServer(t, &fakeAuthenticator{})
	b := newBrowser(t, server.Handler())

	_, page := b.get("/login")

	for _, want := range []string{
		`<label for="email"`,
		`<label for="password"`,
		`id="email"`,
		`id="password"`,
		`type="email"`,
		`type="password"`,
		`autocomplete="username"`,
		`autocomplete="current-password"`,
		`method="post"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the credential form lacks %q:\n%s", want, page)
		}
	}
}

// TestTheRunCookieIsHostOnlyAndShortLived pins the loopback cookie profile: no
// __Host- prefix and no Secure, because this profile is plain HTTP on loopback, but
// HttpOnly, path-scoped, no Domain, and bounded in time.
func TestTheRunCookieIsHostOnlyAndShortLived(t *testing.T) {
	server := newServer(t, &fakeAuthenticator{})
	b := newBrowser(t, server.Handler())

	resp, _ := b.get("/")

	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("the response set %d cookies, want exactly 1", len(cookies))
	}
	cookie := cookies[0]

	switch {
	case strings.HasPrefix(cookie.Name, "__Host-"):
		t.Error("the loopback cookie uses the __Host- prefix, which requires Secure")
	case cookie.Secure:
		t.Error("the loopback cookie is marked Secure on a plain-HTTP profile")
	case !cookie.HttpOnly:
		t.Error("the cookie is not HttpOnly")
	case cookie.Domain != "":
		t.Errorf("the cookie names a domain (%q); it must be host-only", cookie.Domain)
	case cookie.Path != "/":
		t.Errorf("cookie path = %q, want /", cookie.Path)
	case cookie.MaxAge <= 0:
		t.Errorf("cookie MaxAge = %d, want a bounded lifetime", cookie.MaxAge)
	case cookie.SameSite != http.SameSiteStrictMode && cookie.SameSite != http.SameSiteLaxMode:
		t.Errorf("cookie SameSite = %v, want Strict or Lax", cookie.SameSite)
	}
}

// TestUnsolicitedCredentialsAreRefused covers the fixed-route rule: without the run
// cookie and the form token, the route answers a generic page and discloses
// nothing. Discoverability is not a security boundary.
func TestUnsolicitedCredentialsAreRefused(t *testing.T) {
	tests := []struct {
		name   string
		cookie bool
		token  string
	}{
		{name: "no cookie and no token"},
		{name: "cookie but no token", cookie: true},
		{name: "cookie and a wrong token", cookie: true, token: wrongToken},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeAuthenticator{loginAttempt: succeeded()}
			server := newServer(t, fake)
			b := newBrowser(t, server.Handler())

			if tc.cookie {
				b.get("/")
			} else {
				b.client.Jar = nil
			}

			resp, body := b.post("/login", url.Values{
				fieldCSRF:     {tc.token},
				fieldEmail:    {testEmail},
				fieldPassword: {testPassword},
			})

			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404", resp.StatusCode)
			}
			if fake.logins != 0 {
				t.Error("unsolicited credentials reached Garmin")
			}
			if strings.Contains(body, testEmail) {
				t.Error("the refusal page echoes the submitted account")
			}
		})
	}
}

// TestOversizedBodyIsRefusedBeforeParsing bounds the request body, so a hostile or
// broken client cannot make the process read without limit.
func TestOversizedBodyIsRefusedBeforeParsing(t *testing.T) {
	fake := &fakeAuthenticator{loginAttempt: succeeded()}
	server := newServer(t, fake)
	b := newBrowser(t, server.Handler())

	_, form := b.get("/login")
	resp, _ := b.post("/login", url.Values{
		fieldCSRF:     {csrfToken(t, form)},
		fieldEmail:    {testEmail},
		fieldPassword: {strings.Repeat("x", 2*loginweb.MaxRequestBytes)},
	})

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
	if fake.logins != 0 {
		t.Error("an oversized body reached Garmin")
	}
}

// TestOverlongFieldIsRefusedBeforeTheLoginCall bounds each field as well, because a
// body under the limit can still carry an absurd single value.
func TestOverlongFieldIsRefusedBeforeTheLoginCall(t *testing.T) {
	fake := &fakeAuthenticator{loginAttempt: succeeded()}
	server := newServer(t, fake)
	b := newBrowser(t, server.Handler())

	_, form := b.get("/login")
	resp, body := b.post("/login", url.Values{
		fieldCSRF:     {csrfToken(t, form)},
		fieldEmail:    {strings.Repeat("a", loginweb.MaxEmailLen+1) + "@example.test"},
		fieldPassword: {testPassword},
	})

	if resp.StatusCode == http.StatusSeeOther {
		t.Error("an over-long field was accepted")
	}
	if fake.logins != 0 {
		t.Error("an over-long field reached Garmin")
	}
	if strings.Contains(body, testPassword) {
		t.Error("the rejection page echoes the password")
	}
}

// errLoginRejected stands in for a Garmin refusal.
var errLoginRejected = errors.New("garmin auth: login was rejected")

// TestNoCredentialEverReachesTheLogOrAnError is the credential rule in one
// assertion: whatever happens, no log record and no rendered page carries the
// password or the account, and a failed login does not put either in an error.
func TestNoCredentialEverReachesTheLogOrAnError(t *testing.T) {
	var logs bytes.Buffer
	fake := &fakeAuthenticator{loginErr: errLoginRejected}

	server, err := loginweb.New(loginweb.Config{
		Authenticator: fake,
		Logger: slog.New(slog.NewJSONHandler(&logs,
			&slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("loginweb.New returned error: %v", err)
	}

	b := newBrowser(t, server.Handler())
	_, form := b.get("/login")
	_, page := b.post("/login", url.Values{
		fieldCSRF:     {csrfToken(t, form)},
		fieldEmail:    {testEmail},
		fieldPassword: {testPassword},
	})

	for name, content := range map[string]string{"the log": logs.String(), "the page": page} {
		for _, material := range []string{testPassword, testEmail} {
			if strings.Contains(content, material) {
				t.Errorf("%s carries %q:\n%s", name, material, content)
			}
		}
	}

	outcome := server.Outcome()
	if outcome.Succeeded() {
		t.Error("a rejected login reported success")
	}
	if outcome.Err != nil && strings.Contains(outcome.Err.Error(), testPassword) {
		t.Error("the recorded error carries the password")
	}
}
