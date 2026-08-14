//go:build fakegarmin

package auth_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func TestLoginMobileStrategySucceeds(t *testing.T) {
	h := newHarness(t, mobileSuccessScript())

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if result.State() != auth.StateAuthenticated {
		t.Errorf("State() = %s, want authenticated", result.State())
	}
	if result.Strategy() != auth.StrategyMobileIOS {
		t.Errorf("Strategy() = %s, want mobile_ios", result.Strategy())
	}
	if result.NeedsMFA() {
		t.Error("NeedsMFA() = true")
	}

	set, version, ok := h.store.get(testPrincipal)
	if !ok {
		t.Fatal("no token set was stored")
	}
	if version != 1 {
		t.Errorf("stored version = %d, want 1", version)
	}
	if set.Token() != testAccessToken || set.RefreshToken() != testRefresh {
		t.Error("stored token set does not match the DI token response")
	}
	if want := protocol.DIClientIDs()[0]; set.ClientID() != want {
		t.Errorf("ClientID() = %q, want %q", set.ClientID(), want)
	}

	if h.requestCount(protocol.PathSocialProfile) != 1 {
		t.Error("the candidate session was not validated exactly once")
	}
	if h.requestCount(protocol.PathWidgetSignIn) != 0 || h.requestCount(protocol.PathPortalLogin) != 0 {
		t.Errorf("a later strategy ran after success: %v", h.paths())
	}
	if len(h.clock.Sleeps()) != 0 {
		t.Errorf("the mobile strategy paced %v; it performs no GET first", h.clock.Sleeps())
	}
	h.assertNoCredentialsInQueries()
}

func TestLoginStopsOnDefinitiveVerdicts(t *testing.T) {
	tests := map[string]struct {
		body     string
		sentinel error
	}{
		"invalid credentials": {
			body:     testkit.LoginInvalidCredentialsJSON(),
			sentinel: protocol.ErrInvalidCredentials,
		},
		"account locked": {
			body:     testkit.LoginAccountLockedJSON(),
			sentinel: protocol.ErrAccountLocked,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, baseScript().With(protocol.PathMobileLogin,
				testkit.JSON(http.StatusOK, tc.body)))

			result, err := h.login()
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("err = %v, want a %v", err, tc.sentinel)
			}
			if result.State() != auth.StateFailed {
				t.Errorf("State() = %s, want failed", result.State())
			}
			if h.requestCount(protocol.PathWidgetEmbed) != 0 || h.requestCount(protocol.PathPortalSignInPage) != 0 {
				t.Errorf("the fallback chain continued past a definitive verdict: %v", h.paths())
			}
			if _, _, ok := h.store.get(testPrincipal); ok {
				t.Error("a failed login stored a token set")
			}
		})
	}
}

func TestLoginFallsThroughBotChallengeToWidget(t *testing.T) {
	script := widgetPages(baseScript()).
		With(protocol.PathMobileLogin, testkit.HTML(http.StatusForbidden, testkit.BotChallengeHTML())).
		With(protocol.PathWidgetSignIn,
			testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)),
			testkit.HTML(http.StatusOK, testkit.WidgetSuccessHTML(testTicket)))

	h := newHarness(t, script)

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.Strategy() != auth.StrategyWidget {
		t.Fatalf("Strategy() = %s, want sso_widget", result.Strategy())
	}
	if h.requestCount(protocol.PathPortalLogin) != 0 {
		t.Errorf("the portal strategy ran after the widget succeeded: %v", h.paths())
	}
	if got := h.clock.Sleeps(); len(got) != 1 || got[0] != protocol.WidgetPacingMin {
		t.Errorf("widget pacing = %v, want one sleep of %v", got, protocol.WidgetPacingMin)
	}
	h.assertNoCredentialsInQueries()
}

func TestLoginFallsThroughRateLimitToPortal(t *testing.T) {
	script := widgetPages(baseScript()).
		With(protocol.PathMobileLogin, testkit.RateLimited(30)).
		With(protocol.PathWidgetSignIn,
			testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)),
			testkit.RateLimited(30)).
		With(protocol.PathPortalSignInPage, testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF))).
		With(protocol.PathPortalLogin, testkit.JSON(http.StatusOK, testkit.LoginSuccessJSON(testTicket)))

	h := newHarness(t, script)

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.Strategy() != auth.StrategyPortal {
		t.Fatalf("Strategy() = %s, want portal", result.Strategy())
	}

	want := []time.Duration{protocol.WidgetPacingMin, protocol.PortalPacingMin}
	got := h.clock.Sleeps()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("pacing = %v, want %v", got, want)
	}
	h.assertNoCredentialsInQueries()
}

func TestLoginExhaustsEveryStrategy(t *testing.T) {
	temporary := testkit.JSON(http.StatusBadGateway, `{"error":{"status-code":"502"}}`)
	script := widgetPages(baseScript()).
		With(protocol.PathMobileLogin, temporary).
		With(protocol.PathWidgetSignIn,
			testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)),
			temporary).
		With(protocol.PathPortalSignInPage, testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF))).
		With(protocol.PathPortalLogin, temporary)

	h := newHarness(t, script)

	result, err := h.login()
	if !errors.Is(err, auth.ErrLoginExhausted) {
		t.Fatalf("err = %v, want ErrLoginExhausted", err)
	}
	if result.State() != auth.StateFailed {
		t.Errorf("State() = %s, want failed", result.State())
	}
	for _, path := range []string{protocol.PathMobileLogin, protocol.PathWidgetSignIn, protocol.PathPortalLogin} {
		if h.requestCount(path) == 0 {
			t.Errorf("strategy for %s never ran: %v", path, h.paths())
		}
	}
}

// A session the API tier refuses says nothing about the password, so the token is
// discarded and the next strategy gets a turn. Source: upstream issue #369.
func TestLoginRejectedSessionIsNotStoredAndFallsThrough(t *testing.T) {
	script := widgetPages(baseScript()).
		With(protocol.PathMobileLogin, testkit.JSON(http.StatusOK, testkit.LoginSuccessJSON(testTicket))).
		With(protocol.PathWidgetSignIn,
			testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)),
			testkit.HTML(http.StatusOK, testkit.WidgetSuccessHTML(testTicket))).
		With(protocol.PathSocialProfile,
			testkit.JSON(http.StatusUnauthorized, `{"message":"rejected"}`),
			testkit.JSON(http.StatusOK, testkit.SocialProfileJSON("Fake User")))

	h := newHarness(t, script)

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.Strategy() != auth.StrategyWidget {
		t.Fatalf("Strategy() = %s, want sso_widget", result.Strategy())
	}
	if h.store.saveCount() != 1 {
		t.Errorf("Save was called %d times; a rejected session must not be saved", h.store.saveCount())
	}
}

// A validation call that merely failed is not a rejection: upstream keeps the
// token on a transient error, so the two must stay distinguishable.
func TestLoginTemporaryValidationFailureIsDistinguishable(t *testing.T) {
	script := widgetPages(baseScript()).
		With(protocol.PathMobileLogin, testkit.JSON(http.StatusOK, testkit.LoginSuccessJSON(testTicket))).
		With(protocol.PathWidgetSignIn,
			testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)),
			testkit.HTML(http.StatusOK, testkit.WidgetSuccessHTML(testTicket))).
		With(protocol.PathPortalSignInPage, testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF))).
		With(protocol.PathPortalLogin, testkit.JSON(http.StatusOK, testkit.LoginSuccessJSON(testTicket))).
		With(protocol.PathSocialProfile, testkit.JSON(http.StatusInternalServerError, `{"message":"later"}`))

	h := newHarness(t, script)

	_, err := h.login()
	if !errors.Is(err, protocol.ErrTemporary) {
		t.Fatalf("err = %v, want a temporary failure", err)
	}
	if errors.Is(err, protocol.ErrSessionRejected) {
		t.Fatal("a temporary validation failure was reported as a rejection")
	}
	if _, _, ok := h.store.get(testPrincipal); ok {
		t.Error("an unvalidated session was stored")
	}
}

func TestLoginTriesDICandidateClientIDsInOrder(t *testing.T) {
	script := mobileSuccessScript().With(protocol.PathDIToken,
		testkit.JSON(http.StatusNotFound, `{"error":"unknown client"}`),
		testkit.JSON(http.StatusOK, testkit.DITokenJSON(testAccessToken, testRefresh)))

	h := newHarness(t, script)

	if _, err := h.login(); err != nil {
		t.Fatalf("Login: %v", err)
	}

	candidates := protocol.DIClientIDs()
	var exchanges []testkit.RecordedRequest
	for _, req := range h.server.Requests() {
		if req.Path == protocol.PathDIToken {
			exchanges = append(exchanges, req)
		}
	}
	if len(exchanges) != 2 {
		t.Fatalf("%d DI exchanges, want 2", len(exchanges))
	}
	for i, req := range exchanges {
		if got, want := req.Header.Get("Authorization"), protocol.BasicAuthHeader(candidates[i]); got != want {
			t.Errorf("exchange %d used Authorization %q, want the header for %q", i, got, candidates[i])
		}
		if !strings.Contains(string(req.Body), candidates[i]) {
			t.Errorf("exchange %d did not send client_id %q", i, candidates[i])
		}
	}

	set, _, _ := h.store.get(testPrincipal)
	if set.ClientID() != candidates[1] {
		t.Errorf("ClientID() = %q, want the accepted candidate %q", set.ClientID(), candidates[1])
	}
}

// The exp and client_id claims are read from an unverified token purely to label
// and schedule the stored set.
func TestLoginReadsUnverifiedClaimsForScheduling(t *testing.T) {
	const claimedClientID = "GARMIN_CONNECT_MOBILE_IOS_DI"
	token := jwtLike(signedHeader, `{"exp":1786104000,"client_id":"`+claimedClientID+`"}`, fakeSignature)

	script := mobileSuccessScript().With(protocol.PathDIToken, testkit.JSON(http.StatusOK,
		`{"access_token":"`+token+`","refresh_token":"`+testRefresh+`","token_type":"Bearer"}`))

	h := newHarness(t, script)

	if _, err := h.login(); err != nil {
		t.Fatalf("Login: %v", err)
	}

	set, _, _ := h.store.get(testPrincipal)
	if set.ClientID() != claimedClientID {
		t.Errorf("ClientID() = %q, want the claim %q", set.ClientID(), claimedClientID)
	}
	if want := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC); !set.ExpiresAt().Equal(want) {
		t.Errorf("ExpiresAt() = %v, want %v", set.ExpiresAt(), want)
	}
}

// An opaque DI token carries no exp, which must leave the expiry unknown rather
// than fabricate one.
func TestLoginLeavesExpiryUnknownForAnOpaqueToken(t *testing.T) {
	h := newHarness(t, mobileSuccessScript())

	if _, err := h.login(); err != nil {
		t.Fatalf("Login: %v", err)
	}

	set, _, _ := h.store.get(testPrincipal)
	if !set.ExpiresAt().IsZero() {
		t.Errorf("ExpiresAt() = %v, want the zero instant", set.ExpiresAt())
	}
}

func TestLoginErrorsCarryNoSecrets(t *testing.T) {
	leaky := `{"password":"` + testPassword + `","serviceTicketId":"` + testTicket + `",` +
		`"username":"` + testEmail + `"}`
	script := widgetPages(baseScript()).
		With(protocol.PathMobileLogin, testkit.JSON(http.StatusInternalServerError, leaky)).
		With(protocol.PathWidgetSignIn,
			testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)),
			testkit.JSON(http.StatusInternalServerError, leaky)).
		With(protocol.PathPortalSignInPage, testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF))).
		With(protocol.PathPortalLogin, testkit.JSON(http.StatusInternalServerError, leaky))

	h := newHarness(t, script)

	_, err := h.login()
	if err == nil {
		t.Fatal("Login succeeded against a failing server")
	}
	for _, bad := range []string{testPassword, testEmail, testTicket, testCSRF, leaky} {
		if strings.Contains(err.Error(), bad) {
			t.Fatalf("error %q leaked %q", err, bad)
		}
	}
}

// The default jitter must stay inside the protocol's pacing window without a
// test having to pin it.
func TestLoginDefaultJitterStaysWithinPacingBounds(t *testing.T) {
	script := widgetPages(baseScript()).
		With(protocol.PathMobileLogin, testkit.RateLimited(1)).
		With(protocol.PathWidgetSignIn,
			testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)),
			testkit.HTML(http.StatusOK, testkit.WidgetSuccessHTML(testTicket)))

	server := testkit.NewServer(t, script)
	clock := testkit.NewFakeClock(fakeStart())
	registry, err := auth.NewRegistry(auth.RegistryConfig{Clock: clock})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(auth.Config{
		Hosts:     server.Hosts(protocol.DomainGlobal),
		Transport: server.Doer(),
		Store:     newFakeStore(),
		Registry:  registry,
		Clock:     clock,
		Sleeper:   clock,
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}

	if _, err := authenticator.Login(t.Context(), testPrincipal,
		auth.NewCredentials(testEmail, testPassword)); err != nil {
		t.Fatalf("Login: %v", err)
	}

	sleeps := clock.Sleeps()
	if len(sleeps) != 1 {
		t.Fatalf("sleeps = %v, want exactly one", sleeps)
	}
	if sleeps[0] < protocol.WidgetPacingMin || sleeps[0] > protocol.WidgetPacingMax {
		t.Fatalf("sleep %v is outside [%v, %v]", sleeps[0], protocol.WidgetPacingMin, protocol.WidgetPacingMax)
	}
}
