package oauthserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testConsentKey(t *testing.T) ConsentKey {
	t.Helper()
	return ConsentKey{
		Principal:   mustPrincipal(t, testPrincipalID),
		ClientID:    testClientID,
		RedirectURI: mustRedirectURI(t, testRedirect),
		Resource:    mustResource(t, testResourceURI),
	}
}

// TestRevokeConsentCascadesToTheTokenFamilies is the transactional cascade: withdrawing
// consent must not leave a live token behind that was issued under it.
func TestRevokeConsentCascadesToTheTokenFamilies(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)
	stored, err := h.store.AccessToken(context.Background(), tokens.AccessToken.Lookup())
	if err != nil {
		t.Fatalf("reading the access token: %v", err)
	}
	if h.store.consentCount() != 1 {
		t.Fatalf("consents = %d, want 1 before revocation", h.store.consentCount())
	}

	if err := h.srv.RevokeConsent(context.Background(), testConsentKey(t)); err != nil {
		t.Fatalf("RevokeConsent: %v", err)
	}

	if h.store.consentCount() != 0 {
		t.Fatal("the consent record survived revocation")
	}
	if !h.store.familyRevoked(stored.Family) {
		t.Fatal("revoking consent left the token family alive")
	}
	if _, err := h.srv.VerifyAccessToken(
		context.Background(), tokens.AccessToken,
	); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("the access token still verified after revocation: %v", err)
	}
	if _, err := h.exchange(t, refreshRequest(tokens.RefreshToken)); err == nil {
		t.Fatal("the refresh token still worked after revocation")
	}
}

func TestRevokeConsentIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.firstTokens(t)

	for range 3 {
		if err := h.srv.RevokeConsent(context.Background(), testConsentKey(t)); err != nil {
			t.Fatalf("RevokeConsent: %v", err)
		}
	}
}

func TestRevokeConsentFailsClosedOnAStorageFailure(t *testing.T) {
	h := newHarness(t)
	h.firstTokens(t)
	h.store.failOn["RevokeConsent"] = errors.New("disk on fire")

	err := h.srv.RevokeConsent(context.Background(), testConsentKey(t))

	if !errors.Is(err, ErrStorage) {
		t.Fatalf("error = %v, want it to wrap ErrStorage", err)
	}
	if h.store.consentCount() != 1 {
		t.Fatal("a failed revocation reported failure but deleted the consent")
	}
}

func TestRevokeConsentRefusesAnUnresolvedPrincipal(t *testing.T) {
	h := newHarness(t)
	key := testConsentKey(t)
	key.Principal = identityZeroPrincipal()

	if err := h.srv.RevokeConsent(context.Background(), key); err == nil {
		t.Fatal("RevokeConsent accepted the zero principal")
	}
}

// TestRevokePrincipalUnlinksEverything is the Garmin-account unlink half: every family
// for the principal dies and every consent goes, whichever client issued it.
func TestRevokePrincipalUnlinksEverything(t *testing.T) {
	other := publicClientSpec()
	other.ID = testOtherClientID
	h := newHarness(t, publicClientSpec(), other)

	first := h.firstTokens(t)
	secondReq := validAuthorizeRequest()
	secondReq.ClientID = other.ID
	grant := codeGrantRequest(h.issuedCode(t, secondReq))
	grant.ClientID = other.ID
	second, err := h.exchange(t, grant)
	if err != nil {
		t.Fatalf("the second client's code grant: %v", err)
	}
	if h.store.consentCount() != 2 {
		t.Fatalf("consents = %d, want 2", h.store.consentCount())
	}

	if err := h.srv.RevokePrincipal(
		context.Background(), mustPrincipal(t, testPrincipalID),
	); err != nil {
		t.Fatalf("RevokePrincipal: %v", err)
	}

	if h.store.consentCount() != 0 {
		t.Fatalf("consents = %d, want 0 after unlinking", h.store.consentCount())
	}
	for label, token := range map[string]Secret{
		"first client":  first.AccessToken,
		"second client": second.AccessToken,
	} {
		if _, err := h.srv.VerifyAccessToken(context.Background(), token); !errors.Is(
			err, ErrInvalidToken) {
			t.Fatalf("the %s access token survived unlinking: %v", label, err)
		}
	}
}

func TestRevokePrincipalFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.firstTokens(t)
	h.store.failOn["RevokePrincipal"] = errors.New("disk on fire")

	err := h.srv.RevokePrincipal(context.Background(), mustPrincipal(t, testPrincipalID))

	if !errors.Is(err, ErrStorage) {
		t.Fatalf("error = %v, want it to wrap ErrStorage", err)
	}
}

func TestRevokeTokenKillsTheFamilyForEitherTokenType(t *testing.T) {
	for name, pick := range map[string]func(TokenResponse) Secret{
		"revoking the refresh token": func(r TokenResponse) Secret { return r.RefreshToken },
		"revoking the access token":  func(r TokenResponse) Secret { return r.AccessToken },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			tokens := h.firstTokens(t)
			stored, err := h.store.AccessToken(context.Background(), tokens.AccessToken.Lookup())
			if err != nil {
				t.Fatalf("reading the access token: %v", err)
			}

			err = h.srv.RevokeToken(context.Background(), RevokeRequest{
				ClientID: testClientID,
				Token:    pick(tokens).Reveal(),
			})
			if err != nil {
				t.Fatalf("RevokeToken: %v", err)
			}

			if !h.store.familyRevoked(stored.Family) {
				t.Fatal("the token family survived revocation")
			}
		})
	}
}

// TestRevokeTokenIsSilentAboutTokensItWillNotRevoke follows RFC 7009 §2.2: an invalid
// token is not an error, because answering differently would turn the endpoint into an
// oracle for guessing valid tokens.
func TestRevokeTokenIsSilentAboutTokensItWillNotRevoke(t *testing.T) {
	other := publicClientSpec()
	other.ID = testOtherClientID
	h := newHarness(t, publicClientSpec(), other)
	tokens := h.firstTokens(t)
	stored, err := h.store.AccessToken(context.Background(), tokens.AccessToken.Lookup())
	if err != nil {
		t.Fatalf("reading the access token: %v", err)
	}

	for name, req := range map[string]RevokeRequest{
		"token not stored":       {ClientID: testClientID, Token: "not-a-real-token"},
		"absent token":           {ClientID: testClientID},
		"another client's token": {ClientID: other.ID, Token: tokens.RefreshToken.Reveal()},
	} {
		t.Run(name, func(t *testing.T) {
			if err := h.srv.RevokeToken(context.Background(), req); err != nil {
				t.Fatalf("RevokeToken reported %v, want silence", err)
			}
		})
	}
	if h.store.familyRevoked(stored.Family) {
		t.Fatal("another client revoked this client's token family")
	}
}

func TestRevokeTokenRequiresAKnownClient(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)

	err := h.srv.RevokeToken(context.Background(), RevokeRequest{
		ClientID: testUnknownClient,
		Token:    tokens.RefreshToken.Reveal(),
	})

	if !errors.Is(err, ErrUnknownClient) {
		t.Fatalf("error = %v, want ErrUnknownClient", err)
	}
}

func TestRevocationHandlerAnswersAFormPost(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)
	form := url.Values{
		paramClientID:      {testClientID},
		paramToken:         {tokens.RefreshToken.Reveal()},
		paramTokenTypeHint: {paramRefreshToken},
	}

	req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.srv.RevocationHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != testNoStore {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if strings.Contains(rec.Body.String(), tokens.RefreshToken.Reveal()) {
		t.Fatal("the response echoed the revoked token")
	}
}

func TestRevocationHandlerRefusesOtherMethods(t *testing.T) {
	h := newHarness(t)

	rec := httptest.NewRecorder()
	h.srv.RevocationHandler().ServeHTTP(
		rec, httptest.NewRequest(http.MethodGet, "/oauth/revoke", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
