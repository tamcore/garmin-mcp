package loginweb_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/loginweb"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The refusal fixtures. The description is fixed text, as the interface requires.
const (
	refusalDescription = "The client is not registered with this server."
	refusalRedirect    = testRedirectURI + "?error=invalid_request&state=echoed"
)

// fakeRefusal is an authorization refusal under test control.
type fakeRefusal struct {
	status   int
	location string
}

func (f fakeRefusal) Error() string       { return "oauth: refused" }
func (f fakeRefusal) Status() int         { return f.status }
func (f fakeRefusal) Description() string { return refusalDescription }
func (f fakeRefusal) Location() string    { return f.location }

// TestNewRemoteRefusesAnIncoherentConfiguration keeps a login server that cannot work
// from serving anything at all.
func TestNewRemoteRefusesAnIncoherentConfiguration(t *testing.T) {
	authz := newFakeAuthorizations(time.Now)
	garmin := &fakeAuthenticator{}

	tests := map[string]struct {
		config loginweb.RemoteConfig
		want   error
	}{
		"no authorization server": {
			config: loginweb.RemoteConfig{Authenticator: garmin},
			want:   loginweb.ErrNoAuthorizations,
		},
		"no authenticator": {
			config: loginweb.RemoteConfig{Authorizations: authz},
			want:   loginweb.ErrNoAuthenticator,
		},
		"a negative lifetime": {
			config: loginweb.RemoteConfig{
				Authorizations: authz, Authenticator: garmin, TTL: -time.Second,
			},
			want: loginweb.ErrInvalidConfig,
		},
		"a negative attempt budget": {
			config: loginweb.RemoteConfig{
				Authorizations: authz, Authenticator: garmin, MaxAttempts: -1,
			},
			want: loginweb.ErrInvalidConfig,
		},
		"a negative HSTS age": {
			config: loginweb.RemoteConfig{
				Authorizations: authz, Authenticator: garmin, HSTSMaxAge: -time.Hour,
			},
			want: loginweb.ErrInvalidConfig,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			server, err := loginweb.NewRemote(tc.config)
			if !errors.Is(err, tc.want) {
				t.Fatalf("NewRemote error = %v, want %v", err, tc.want)
			}
			if server != nil {
				t.Error("NewRemote returned a server for a refused configuration")
			}
		})
	}
}

// TestARefusedAuthorizationIsDeliveredAsTheServerDecided covers both deliveries: a
// redirect once the client and the redirect URI are validated, and a local render
// before that, because the presented redirect URI may be an attacker's.
func TestARefusedAuthorizationIsDeliveredAsTheServerDecided(t *testing.T) {
	tests := map[string]struct {
		refusal    error
		wantStatus int
		wantHeader string
	}{
		"a validated client may be redirected": {
			refusal:    fakeRefusal{status: http.StatusFound, location: refusalRedirect},
			wantStatus: http.StatusFound,
			wantHeader: refusalRedirect,
		},
		"an unvalidated client is rendered here": {
			refusal:    fakeRefusal{status: http.StatusBadRequest},
			wantStatus: http.StatusBadRequest,
		},
		"an unrecognised failure is generic": {
			refusal:    errors.New("storage is unreachable"),
			wantStatus: http.StatusNotFound,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			h := newRemote(t, &fakeAuthenticator{})
			h.authz.beginErr = tc.refusal

			resp, body := h.b.get(pathAuthorize + "?" + authorizeQuery(testClientID).Encode())

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if got := resp.Header.Get("Location"); got != tc.wantHeader {
				t.Errorf("Location = %q, want %q", got, tc.wantHeader)
			}
			if capabilityCookie(resp) != "" {
				t.Error("a refused authorization set a transaction cookie")
			}
			if strings.Contains(body, "storage") {
				t.Errorf("the refusal page leaks the internal cause:\n%s", body)
			}
		})
	}
}

// TestTheSessionRegistryIsBounded refuses a new transaction rather than growing
// without limit, and rather than evicting somebody else's live login.
func TestTheSessionRegistryIsBounded(t *testing.T) {
	clock := testkit.NewFakeClock(time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	authz := newFakeAuthorizations(clock.Now)
	server, err := loginweb.NewRemote(loginweb.RemoteConfig{
		Authorizations: authz,
		Authenticator:  &fakeAuthenticator{},
		MaxSessions:    1,
		Now:            clock.Now,
	})
	if err != nil {
		t.Fatalf("loginweb.NewRemote returned error: %v", err)
	}
	b := newBrowser(t, server.Handler())

	first, _ := b.get(pathAuthorize + "?" + authorizeQuery(testClientID).Encode())
	if first.StatusCode != http.StatusSeeOther {
		t.Fatalf("the first authorization = %d, want 303", first.StatusCode)
	}

	second, body := b.get(pathAuthorize + "?" + authorizeQuery(testOtherClient).Encode())
	if second.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("the second authorization = %d, want 503", second.StatusCode)
	}
	if strings.Contains(body, testOtherClient) {
		t.Error("the refusal page discloses the client")
	}
}

// TestALoginWithoutAPrincipalEndsTheTransaction covers the case where Garmin answers
// but no principal is resolved: nothing is attached, and the transaction stops.
func TestALoginWithoutAPrincipalEndsTheTransaction(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{loginAttempt: loginweb.Attempt{Strategy: "sso_widget"}})

	h.authorize()
	resp := h.submitRemoteCredentials(h.continueToCredentials())

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if attaches, grants, denials := h.authz.counts(); attaches != 0 || grants != 0 || denials != 0 {
		t.Errorf("attaches=%d grants=%d denials=%d, want 0/0/0", attaches, grants, denials)
	}
	if resp, _ := h.b.get(pathConsent); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the transaction is still addressable: %d", resp.StatusCode)
	}
}

// TestARejectedCodeCanBeRetriedOnTheOTPForm keeps the second leg usable within the
// budget, with generic text and no disclosure.
func TestARejectedCodeCanBeRetriedOnTheOTPForm(t *testing.T) {
	h := newRemote(t, &fakeAuthenticator{
		loginAttempt: challenged(),
		mfaErr:       errLoginRejected,
	})

	h.authorize()
	h.submitRemoteCredentials(h.continueToCredentials())
	_, mfaForm := h.b.get(pathMFA)

	resp, page := h.b.post(pathMFA, url.Values{
		fieldCSRF: {csrfToken(t, mfaForm)},
		fieldCode: {testCode},
	})

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a rejected code = %d, want 401", resp.StatusCode)
	}
	if strings.Contains(page, testCode) || strings.Contains(page, testTxnID) {
		t.Error("the retry page echoes the code or the continuation capability")
	}
	if !strings.Contains(page, `name="code"`) {
		t.Errorf("the retry page is not the OTP form:\n%s", page)
	}
}
