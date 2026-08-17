package oauthserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifyAccessTokenAcceptsAFreshToken(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)

	info, err := h.srv.VerifyAccessToken(context.Background(), tokens.AccessToken)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}

	if info.Principal.ID() != testPrincipalID {
		t.Fatalf("Principal = %q", info.Principal.ID())
	}
	if info.ClientID != testClientID {
		t.Fatalf("ClientID = %q", info.ClientID)
	}
	if info.Scopes.String() != testScopeProfile {
		t.Fatalf("Scopes = %q", info.Scopes)
	}
	if !info.Resource.Equal(mustResource(t, testResourceURI)) {
		t.Fatalf("Resource = %q", info.Resource)
	}
	if !info.ExpiresAt.Equal(testNow.Add(h.srv.AccessTokenTTL())) {
		t.Fatalf("ExpiresAt = %v", info.ExpiresAt)
	}
}

func TestVerifyAccessTokenDistinguishesMissingFromInvalid(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)

	if _, err := h.srv.VerifyAccessToken(context.Background(), Secret{}); !errors.Is(
		err, ErrMissingToken) {
		t.Fatalf("an absent token: error = %v, want ErrMissingToken", err)
	}
	if _, err := h.srv.VerifyAccessToken(
		context.Background(), SecretFromString("not-a-real-token"),
	); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("an unknown token: error = %v, want ErrInvalidToken", err)
	}

	h.advance(h.srv.AccessTokenTTL())
	_, err := h.srv.VerifyAccessToken(context.Background(), tokens.AccessToken)
	if !errors.Is(err, ErrInvalidToken) || !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("an expired token: error = %v, want ErrInvalidToken and ErrTokenExpired", err)
	}
}

func TestVerifyAccessTokenRefusesARevokedFamily(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)
	stored, err := h.store.AccessToken(context.Background(), tokens.AccessToken.Lookup())
	if err != nil {
		t.Fatalf("reading the access token: %v", err)
	}
	if err := h.store.RevokeFamily(context.Background(), stored.Family, RevokeReasonClient); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}

	_, err = h.srv.VerifyAccessToken(context.Background(), tokens.AccessToken)

	if !errors.Is(err, ErrInvalidToken) || !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("error = %v, want ErrInvalidToken and ErrTokenRevoked", err)
	}
}

// TestVerifyAccessTokenValidatesTheAudienceExactly is the RFC 8707 half: a token that is
// otherwise perfectly valid, but was minted for another resource, is refused here.
func TestVerifyAccessTokenValidatesTheAudienceExactly(t *testing.T) {
	h := newHarness(t)
	foreign, err := NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	record := AccessToken{
		Lookup:    foreign.Lookup(),
		ClientID:  testClientID,
		Principal: mustPrincipal(t, testPrincipalID),
		Scopes:    mustScopeSet(t, testScopeProfile),
		Resource:  mustResource(t, "https://another.example/mcp"),
		Family:    "foreign-family",
		IssuedAt:  testNow,
		ExpiresAt: testNow.Add(time.Hour),
	}
	if err := h.store.SaveTokenPair(context.Background(), record, RefreshToken{
		Lookup: Lookup{1}, Family: record.Family,
	}); err != nil {
		t.Fatalf("SaveTokenPair: %v", err)
	}

	_, err = h.srv.VerifyAccessToken(context.Background(), foreign)

	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
	if !errors.Is(err, ErrResourceNotAllowed) {
		t.Fatalf("error = %v, want it to name the audience mismatch", err)
	}
}

// protect wires the middleware around a handler that reports the principal it sees.
func (h *harness) protect(required ...Scope) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, err := TokenInfoFromContext(r.Context())
		if err != nil {
			http.Error(w, "no token info in context", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(info.Principal.ID()))
	})
	return h.srv.RequireBearerToken(required...)(inner).ServeHTTP
}

func TestRequireBearerTokenAdmitsAValidToken(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken.Reveal())
	rec := httptest.NewRecorder()

	h.protect()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != testPrincipalID {
		t.Fatalf("the handler saw principal %q", rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("a successful request carried a challenge: %q", got)
	}
}

// TestRequireBearerTokenChallengeDistinguishesMissingFromInvalid is the RFC 6750 §3.1
// requirement: a request with no credentials must not be told which error it made, and a
// request with a broken credential must be.
func TestRequireBearerTokenChallengeDistinguishesMissingFromInvalid(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)

	cases := map[string]struct {
		authorization string
		wantStatus    int
		wantError     string
	}{
		"no header":        {"", http.StatusUnauthorized, ""},
		"empty bearer":     {"Bearer", http.StatusUnauthorized, ""},
		"wrong scheme":     {"Basic abcdef", http.StatusUnauthorized, ""},
		"two values":       {"Bearer a b", http.StatusUnauthorized, ""},
		"unknown token":    {"Bearer not-a-real-token", http.StatusUnauthorized, "invalid_token"},
		"valid token":      {"Bearer " + tokens.AccessToken.Reveal(), http.StatusOK, ""},
		"lowercase scheme": {"bearer " + tokens.AccessToken.Reveal(), http.StatusOK, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.authorization != "" {
				req.Header.Set("Authorization", tc.authorization)
			}
			rec := httptest.NewRecorder()

			h.protect()(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				return
			}
			challenge := rec.Header().Get("WWW-Authenticate")
			if !strings.HasPrefix(challenge, "Bearer ") {
				t.Fatalf("challenge = %q, want a Bearer challenge", challenge)
			}
			wantMetadata := `resource_metadata="` + testConfig().ResourceMetadataURL + `"`
			if !strings.Contains(challenge, wantMetadata) {
				t.Fatalf("challenge = %q, want it to name the resource metadata URL", challenge)
			}
			if tc.wantError == "" && strings.Contains(challenge, "error=") {
				t.Fatalf("a request with no credentials was given an error code: %q", challenge)
			}
			if tc.wantError != "" && !strings.Contains(challenge, `error="`+tc.wantError+`"`) {
				t.Fatalf("challenge = %q, want error=%q", challenge, tc.wantError)
			}
			if strings.Contains(rec.Body.String(), tokens.AccessToken.Reveal()) {
				t.Fatal("the response body echoed the presented token")
			}
		})
	}
}

func TestRequireBearerTokenRefusesInsufficientScope(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken.Reveal())
	rec := httptest.NewRecorder()

	h.protect(testScopeHealth)(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	challenge := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(challenge, `error="insufficient_scope"`) {
		t.Fatalf("challenge = %q, want error=insufficient_scope", challenge)
	}
	if !strings.Contains(challenge, "scope=\""+string(testScopeHealth)+"\"") {
		t.Fatalf("challenge = %q, want it to name the required scope", challenge)
	}
}

// TestRequireBearerTokenIgnoresATokenOutsideTheHeader is the "header only" rule: a bearer
// token in a query parameter or a cookie is not a credential here.
func TestRequireBearerTokenIgnoresATokenOutsideTheHeader(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)

	for name, build := range map[string]func() *http.Request{
		"query parameter": func() *http.Request {
			return httptest.NewRequest(http.MethodPost,
				"/mcp?access_token="+tokens.AccessToken.Reveal(), nil)
		},
		"cookie": func() *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.AddCookie(&http.Cookie{Name: "access_token", Value: tokens.AccessToken.Reveal()})
			return req
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			h.protect()(rec, build())

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestTokenInfoFromContextFailsWithoutMiddleware(t *testing.T) {
	if _, err := TokenInfoFromContext(context.Background()); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("error = %v, want ErrMissingToken", err)
	}
}

func TestTokenVerifierMatchesTheSDKShape(t *testing.T) {
	h := newHarness(t)
	tokens := h.firstTokens(t)
	verifier := h.srv.TokenVerifier()

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	info, err := verifier(context.Background(), tokens.AccessToken.Reveal(), req)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	if info.UserID != testPrincipalID {
		t.Fatalf("UserID = %q, want the principal", info.UserID)
	}
	if len(info.Scopes) != 1 || info.Scopes[0] != testScopeProfile {
		t.Fatalf("Scopes = %v", info.Scopes)
	}
	if !info.Expiration.Equal(testNow.Add(h.srv.AccessTokenTTL())) {
		t.Fatalf("Expiration = %v", info.Expiration)
	}
	if got := info.Extra[paramClientID]; got != testClientID {
		t.Fatalf("Extra[client_id] = %v", got)
	}
	if got := info.Extra[paramResource]; got != testResourceURI {
		t.Fatalf("Extra[resource] = %v", got)
	}

	if _, err := verifier(context.Background(), "not-a-real-token", req); err == nil {
		t.Fatal("the verifier accepted an unknown token")
	}
}
