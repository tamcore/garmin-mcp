package oauthserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func postForm(t *testing.T, handler http.Handler, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestTokenHandlerReturnsAnRFC6749Response(t *testing.T) {
	h := newHarness(t)
	code := h.issuedCode(t, validAuthorizeRequest())

	rec := postForm(t, h.srv.TokenHandler(), url.Values{
		paramGrantType:    {GrantAuthorizationCode},
		paramClientID:     {testClientID},
		paramCode:         {code},
		paramRedirectURI:  {testRedirect},
		paramCodeVerifier: {testVerifier},
		paramResource:     {testResourceURI},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != testNoStore {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}

	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the response is not JSON: %v", err)
	}
	if body.TokenType != TokenTypeBearer {
		t.Fatalf("token_type = %q", body.TokenType)
	}
	if body.ExpiresIn != int(h.srv.AccessTokenTTL().Seconds()) {
		t.Fatalf("expires_in = %d", body.ExpiresIn)
	}
	if body.Scope != testScopeProfile {
		t.Fatalf("scope = %q", body.Scope)
	}
	if _, err := h.srv.VerifyAccessToken(
		context.Background(), SecretFromString(body.AccessToken),
	); err != nil {
		t.Fatalf("the returned access token does not verify: %v", err)
	}
	if body.RefreshToken == "" || body.RefreshToken == body.AccessToken {
		t.Fatal("the response did not carry a distinct refresh token")
	}
}

func TestTokenHandlerRendersOAuthErrors(t *testing.T) {
	h := newHarness(t)

	cases := map[string]struct {
		form       url.Values
		wantStatus int
		wantError  string
	}{
		"unsupported grant": {
			url.Values{paramGrantType: {"password"}, paramClientID: {testClientID}},
			http.StatusBadRequest, ErrorUnsupportedGrantType,
		},
		"unknown client": {
			url.Values{
				paramGrantType: {GrantAuthorizationCode},
				paramClientID:  {testUnknownClient},
			},
			http.StatusUnauthorized, ErrorInvalidClient,
		},
		"unknown code": {
			url.Values{
				paramGrantType:    {GrantAuthorizationCode},
				paramClientID:     {testClientID},
				paramCode:         {"not-a-real-code"},
				paramRedirectURI:  {testRedirect},
				paramCodeVerifier: {testVerifier},
			},
			http.StatusBadRequest, ErrorInvalidGrant,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := postForm(t, h.srv.TokenHandler(), tc.form)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != testNoStore {
				t.Fatalf("Cache-Control = %q, want no-store even on an error", got)
			}
			var body struct {
				Error       string `json:"error"`
				Description string `json:"error_description"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("the error response is not JSON: %v", err)
			}
			if body.Error != tc.wantError {
				t.Fatalf("error = %q, want %q", body.Error, tc.wantError)
			}
			if body.Description == "" {
				t.Fatal("the error response carried no description")
			}
		})
	}
}

func TestTokenHandlerRefusesUnusableRequests(t *testing.T) {
	h := newHarness(t)
	handler := h.srv.TokenHandler()

	t.Run("GET", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/token", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
	})

	t.Run("wrong content type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("duplicate parameter", func(t *testing.T) {
		rec := postForm(t, handler, url.Values{
			paramGrantType: {GrantAuthorizationCode, GrantRefreshToken},
			paramClientID:  {testClientID},
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("oversized body", func(t *testing.T) {
		body := strings.NewReader(paramCode + "=" + strings.Repeat("a", maxTokenBody+1))
		req := httptest.NewRequest(http.MethodPost, "/oauth/token", body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestTokenHandlerRotatesThroughTheSameSurface(t *testing.T) {
	h := newHarness(t)
	first := h.firstTokens(t)

	rec := postForm(t, h.srv.TokenHandler(), url.Values{
		paramGrantType:    {GrantRefreshToken},
		paramClientID:     {testClientID},
		paramRefreshToken: {first.RefreshToken.Reveal()},
		paramResource:     {testResourceURI},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the response is not JSON: %v", err)
	}
	if body.RefreshToken == first.RefreshToken.Reveal() {
		t.Fatal("the handler returned the same refresh token")
	}
}
