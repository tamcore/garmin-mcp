package oauthserver

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
)

func TestCodeGrantReturnsAUsableTokenResponse(t *testing.T) {
	h := newHarness(t)
	code := h.issuedCode(t, validAuthorizeRequest())

	got, err := h.exchange(t, codeGrantRequest(code))
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if got.TokenType != TokenTypeBearer {
		t.Fatalf("TokenType = %q, want %q", got.TokenType, TokenTypeBearer)
	}
	if want := int(h.srv.AccessTokenTTL().Seconds()); got.ExpiresIn != want {
		t.Fatalf("ExpiresIn = %d, want %d", got.ExpiresIn, want)
	}
	if got.Scopes.String() != testScopeProfile {
		t.Fatalf("Scopes = %q", got.Scopes)
	}
	if !got.Resource.Equal(mustResource(t, testResourceURI)) {
		t.Fatalf("Resource = %q", got.Resource)
	}
	for label, secret := range map[string]Secret{
		"issued access token":  got.AccessToken,
		"issued refresh token": got.RefreshToken,
	} {
		raw, err := base64.RawURLEncoding.DecodeString(secret.Reveal())
		if err != nil {
			t.Fatalf("the %s is not base64url: %v", label, err)
		}
		if len(raw)*8 < 256 {
			t.Fatalf("the %s carries %d bits, want at least 256", label, len(raw)*8)
		}
	}
}

// TestCodeGrantStoresTokensBoundToTheGrant checks what was persisted, which is what a
// later verification and a later rotation will read.
func TestCodeGrantStoresTokensBoundToTheGrant(t *testing.T) {
	h := newHarness(t)
	got, err := h.exchange(t, codeGrantRequest(h.issuedCode(t, validAuthorizeRequest())))
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	access, err := h.store.AccessToken(context.Background(), got.AccessToken.Lookup())
	if err != nil {
		t.Fatalf("the access token is not stored under its digest: %v", err)
	}
	if access.Principal.ID() != testPrincipalID || access.ClientID != testClientID {
		t.Fatalf("access token is bound to %q / %q", access.Principal.ID(), access.ClientID)
	}
	if !access.Scopes.Equal(got.Scopes) || !access.Resource.Equal(got.Resource) {
		t.Fatal("the stored access token does not match what was returned")
	}
	if want := testNow.Add(h.srv.AccessTokenTTL()); !access.ExpiresAt.Equal(want) {
		t.Fatalf("access expiry = %v, want %v", access.ExpiresAt, want)
	}

	refresh, err := h.store.RefreshToken(context.Background(), got.RefreshToken.Lookup())
	if err != nil {
		t.Fatalf("the refresh token is not stored under its digest: %v", err)
	}
	if refresh.Family == "" || refresh.Family != access.Family {
		t.Fatal("the pair does not share one token family")
	}
	if refresh.Generation != 0 {
		t.Fatalf("Generation = %d, want 0 for the first token in a family", refresh.Generation)
	}
	if want := testNow.Add(h.srv.RefreshTokenTTL()); !refresh.ExpiresAt.Equal(want) {
		t.Fatalf("refresh expiry = %v, want %v", refresh.ExpiresAt, want)
	}
}

// TestCodeGrantVerifiesEveryBinding is the binding matrix. Each row breaks exactly
// one of the bindings captured when the code was issued.
func TestCodeGrantVerifiesEveryBinding(t *testing.T) {
	cases := map[string]struct {
		mutate    func(*TokenRequest)
		wantCause error
		wantCode  string
	}{
		"no code": {
			func(r *TokenRequest) { r.Code = "" }, ErrCodeNotFound, ErrorInvalidGrant,
		},
		"unknown code": {
			func(r *TokenRequest) { r.Code = "not-a-real-code" }, ErrCodeNotFound, ErrorInvalidGrant,
		},
		"wrong redirect URI": {
			func(r *TokenRequest) { r.RedirectURI = "https://client.example/other" },
			ErrCodeBinding, ErrorInvalidGrant,
		},
		"no redirect URI": {
			func(r *TokenRequest) { r.RedirectURI = "" }, ErrCodeBinding, ErrorInvalidGrant,
		},
		"redirect URI trailing slash": {
			func(r *TokenRequest) { r.RedirectURI = testRedirect + "/" },
			ErrCodeBinding, ErrorInvalidGrant,
		},
		"wrong resource": {
			func(r *TokenRequest) { r.Resource = testOtherResource },
			ErrCodeBinding, ErrorInvalidTarget,
		},
		"no verifier": {
			func(r *TokenRequest) { r.CodeVerifier = "" },
			ErrInvalidCodeVerifier, ErrorInvalidGrant,
		},
		"wrong verifier": {
			func(r *TokenRequest) { r.CodeVerifier = testVerifier[:len(testVerifier)-1] + "X" },
			ErrPKCEVerificationFailed, ErrorInvalidGrant,
		},
		"malformed verifier": {
			func(r *TokenRequest) { r.CodeVerifier = "too-short" },
			ErrInvalidCodeVerifier, ErrorInvalidGrant,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			req := codeGrantRequest(h.issuedCode(t, validAuthorizeRequest()))
			tc.mutate(&req)

			_, err := h.exchange(t, req)

			if !errors.Is(err, tc.wantCause) {
				t.Fatalf("error = %v, want it to wrap %v", err, tc.wantCause)
			}
			if got := asTokenError(t, err).Code(); got != tc.wantCode {
				t.Fatalf("Code() = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

// TestCodeGrantRefusesACodeIssuedToAnotherClient is the cross-client binding: a code
// is useless to a client it was not issued to, even when that client authenticates
// correctly and presents the right verifier.
func TestCodeGrantRefusesACodeIssuedToAnotherClient(t *testing.T) {
	other := publicClientSpec()
	other.ID = testOtherClientID
	h := newHarness(t, publicClientSpec(), other)

	req := codeGrantRequest(h.issuedCode(t, validAuthorizeRequest()))
	req.ClientID = other.ID

	if _, err := h.exchange(t, req); !errors.Is(err, ErrCodeBinding) {
		t.Fatalf("error = %v, want ErrCodeBinding", err)
	}
}

func TestCodeGrantIsSingleUse(t *testing.T) {
	h := newHarness(t)
	req := codeGrantRequest(h.issuedCode(t, validAuthorizeRequest()))

	first, err := h.exchange(t, req)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if _, err := h.exchange(t, req); !errors.Is(err, ErrCodeAlreadyUsed) {
		t.Fatalf("a replayed code: error = %v, want ErrCodeAlreadyUsed", err)
	}

	// The tokens from the first, legitimate exchange stay valid: replaying a code is
	// the attacker's failure, not the honest client's.
	if _, err := h.store.AccessToken(context.Background(), first.AccessToken.Lookup()); err != nil {
		t.Fatalf("the first exchange's access token was invalidated: %v", err)
	}
}

func TestCodeGrantRefusesAnExpiredCode(t *testing.T) {
	h := newHarness(t)
	req := codeGrantRequest(h.issuedCode(t, validAuthorizeRequest()))

	h.advance(h.srv.CodeTTL())

	if _, err := h.exchange(t, req); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("error = %v, want ErrCodeExpired", err)
	}
	if _, err := h.exchange(t, req); !errors.Is(err, ErrCodeAlreadyUsed) {
		t.Fatalf("an expired code must also be consumed, got %v", err)
	}
}

func TestCodeGrantWithoutAResourceParameterUsesTheBoundResource(t *testing.T) {
	h := newHarness(t)
	req := codeGrantRequest(h.issuedCode(t, validAuthorizeRequest()))
	req.Resource = ""

	got, err := h.exchange(t, req)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if !got.Resource.Equal(mustResource(t, testResourceURI)) {
		t.Fatalf("Resource = %q, want the resource the code was bound to", got.Resource)
	}
}

func TestCodeGrantCannotWidenScope(t *testing.T) {
	h := newHarness(t)
	req := codeGrantRequest(h.issuedCode(t, validAuthorizeRequest()))
	req.Scope = testScopesBoth

	_, err := h.exchange(t, req)

	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("error = %v, want ErrInvalidScope", err)
	}
	if got := asTokenError(t, err).Code(); got != ErrorInvalidScope {
		t.Fatalf("Code() = %q, want %q", got, ErrorInvalidScope)
	}
}

func TestCodeGrantIssuesNothingWhenStorageFails(t *testing.T) {
	h := newHarness(t)
	req := codeGrantRequest(h.issuedCode(t, validAuthorizeRequest()))
	h.store.failOn["SaveTokenPair"] = errors.New("disk on fire")

	_, err := h.exchange(t, req)

	if !errors.Is(err, ErrStorage) {
		t.Fatalf("error = %v, want it to wrap ErrStorage", err)
	}
	tokenErr := asTokenError(t, err)
	if tokenErr.Code() != ErrorServerError || tokenErr.Status() != 500 {
		t.Fatalf("Code() = %q, Status() = %d", tokenErr.Code(), tokenErr.Status())
	}
	if tokenErr.Description() == "disk on fire" {
		t.Fatalf("the storage failure reached the client: %q", tokenErr.Description())
	}
}
