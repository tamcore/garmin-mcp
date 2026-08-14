package oauthserver

import (
	"context"
	"errors"
	"testing"
)

// firstTokens runs an authorization through to a token pair, which is where every
// refresh test starts.
func (h *harness) firstTokens(t *testing.T) TokenResponse {
	t.Helper()
	got, err := h.exchange(t, codeGrantRequest(h.issuedCode(t, validAuthorizeRequest())))
	if err != nil {
		t.Fatalf("code grant: %v", err)
	}
	return got
}

func refreshRequest(refresh Secret) TokenRequest {
	return TokenRequest{
		GrantType:    GrantRefreshToken,
		ClientID:     testClientID,
		RefreshToken: refresh.Reveal(),
		Resource:     testResourceURI,
	}
}

func TestRefreshRotatesOnEveryUse(t *testing.T) {
	h := newHarness(t)
	first := h.firstTokens(t)

	second, err := h.exchange(t, refreshRequest(first.RefreshToken))
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if second.RefreshToken.Reveal() == first.RefreshToken.Reveal() {
		t.Fatal("the refresh token was not rotated")
	}
	if second.AccessToken.Reveal() == first.AccessToken.Reveal() {
		t.Fatal("the access token was not replaced")
	}
	if !second.Scopes.Equal(first.Scopes) || !second.Resource.Equal(first.Resource) {
		t.Fatal("rotation changed the scopes or the resource")
	}

	rotated, err := h.store.RefreshToken(context.Background(), second.RefreshToken.Lookup())
	if err != nil {
		t.Fatalf("the rotated refresh token is not stored: %v", err)
	}
	original, err := h.store.AccessToken(context.Background(), first.AccessToken.Lookup())
	if err != nil {
		t.Fatalf("reading the original access token: %v", err)
	}
	if rotated.Family != original.Family {
		t.Fatal("rotation started a new family instead of continuing the old one")
	}
	if rotated.Generation != 1 {
		t.Fatalf("Generation = %d, want 1 after one rotation", rotated.Generation)
	}
	if rotated.Principal.ID() != testPrincipalID || rotated.ClientID != testClientID {
		t.Fatal("rotation lost the principal or client binding")
	}
	if h.store.rotations != 1 {
		t.Fatalf("the store recorded %d rotations, want 1", h.store.rotations)
	}
}

func TestRefreshRotationChainsAcrossGenerations(t *testing.T) {
	h := newHarness(t)
	current := h.firstTokens(t)

	for generation := uint64(1); generation <= 3; generation++ {
		next, err := h.exchange(t, refreshRequest(current.RefreshToken))
		if err != nil {
			t.Fatalf("rotation %d: %v", generation, err)
		}
		stored, err := h.store.RefreshToken(context.Background(), next.RefreshToken.Lookup())
		if err != nil {
			t.Fatalf("rotation %d is not stored: %v", generation, err)
		}
		if stored.Generation != generation {
			t.Fatalf("Generation = %d, want %d", stored.Generation, generation)
		}
		current = next
	}
}

// TestRefreshReuseRevokesTheWholeFamily is the reuse-detection requirement. The
// detection and the revocation happen inside one storage transaction, so replaying a
// rotated token kills the chain rather than handing the attacker a fresh pair.
func TestRefreshReuseRevokesTheWholeFamily(t *testing.T) {
	h := newHarness(t)
	first := h.firstTokens(t)
	second, err := h.exchange(t, refreshRequest(first.RefreshToken))
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	family, err := h.store.RefreshToken(context.Background(), second.RefreshToken.Lookup())
	if err != nil {
		t.Fatalf("reading the rotated token: %v", err)
	}

	_, err = h.exchange(t, refreshRequest(first.RefreshToken))

	if !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("error = %v, want ErrRefreshTokenReused", err)
	}
	if got := asTokenError(t, err).Code(); got != ErrorInvalidGrant {
		t.Fatalf("Code() = %q, want %q", got, ErrorInvalidGrant)
	}
	if !h.store.familyRevoked(family.Family) {
		t.Fatal("reuse did not revoke the token family")
	}

	// Everything descended from that authorization is now dead: the live refresh
	// token, and the access token issued alongside it.
	if _, err := h.exchange(t, refreshRequest(second.RefreshToken)); err == nil {
		t.Fatal("the live refresh token still worked after its family was revoked")
	}
	if _, err := h.store.AccessToken(
		context.Background(), second.AccessToken.Lookup(),
	); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("the access token survived family revocation: %v", err)
	}
}

func TestRefreshVerifiesEveryBinding(t *testing.T) {
	other := publicClientSpec()
	other.ID = testOtherClientID

	cases := map[string]struct {
		mutate    func(*TokenRequest)
		wantCause error
		wantCode  string
	}{
		"no token": {
			func(r *TokenRequest) { r.RefreshToken = "" }, ErrTokenNotFound, ErrorInvalidGrant,
		},
		"token not stored": {
			func(r *TokenRequest) { r.RefreshToken = "not-a-real-token" },
			ErrTokenNotFound, ErrorInvalidGrant,
		},
		"another client": {
			func(r *TokenRequest) { r.ClientID = other.ID }, ErrRefreshBinding, ErrorInvalidGrant,
		},
		"changed resource": {
			func(r *TokenRequest) { r.Resource = testOtherResource },
			ErrRefreshBinding, ErrorInvalidTarget,
		},
		"widened scope": {
			func(r *TokenRequest) { r.Scope = testScopesBoth },
			ErrRefreshBinding, ErrorInvalidScope,
		},
		"unrelated scope": {
			func(r *TokenRequest) { r.Scope = testScopeHealth },
			ErrRefreshBinding, ErrorInvalidScope,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, publicClientSpec(), other)
			req := refreshRequest(h.firstTokens(t).RefreshToken)
			tc.mutate(&req)

			_, err := h.exchange(t, req)

			if !errors.Is(err, tc.wantCause) {
				t.Fatalf("error = %v, want it to wrap %v", err, tc.wantCause)
			}
			if got := asTokenError(t, err).Code(); got != tc.wantCode {
				t.Fatalf("Code() = %q, want %q", got, tc.wantCode)
			}
			if h.store.rotations != 0 {
				t.Fatal("a refused refresh rotated the token anyway")
			}
		})
	}
}

func TestRefreshMayNarrowScope(t *testing.T) {
	h := newHarness(t)
	wide := validAuthorizeRequest()
	wide.Scope = testScopesBoth
	got, err := h.exchange(t, codeGrantRequest(h.issuedCode(t, wide)))
	if err != nil {
		t.Fatalf("code grant: %v", err)
	}

	req := refreshRequest(got.RefreshToken)
	req.Scope = testScopeProfile
	narrowed, err := h.exchange(t, req)
	if err != nil {
		t.Fatalf("narrowing refresh: %v", err)
	}

	if narrowed.Scopes.String() != testScopeProfile {
		t.Fatalf("Scopes = %q, want the narrowed set", narrowed.Scopes)
	}
	stored, err := h.store.AccessToken(context.Background(), narrowed.AccessToken.Lookup())
	if err != nil {
		t.Fatalf("reading the narrowed access token: %v", err)
	}
	if stored.Scopes.String() != testScopeProfile {
		t.Fatalf("the stored access token kept %q", stored.Scopes)
	}
}

func TestRefreshRefusesAnExpiredToken(t *testing.T) {
	h := newHarness(t)
	first := h.firstTokens(t)

	h.advance(h.srv.RefreshTokenTTL())

	_, err := h.exchange(t, refreshRequest(first.RefreshToken))

	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("error = %v, want ErrTokenExpired", err)
	}
	if h.store.rotations != 0 {
		t.Fatal("an expired refresh token was rotated")
	}
}

func TestRefreshRefusesARevokedFamily(t *testing.T) {
	h := newHarness(t)
	first := h.firstTokens(t)
	stored, err := h.store.RefreshToken(context.Background(), first.RefreshToken.Lookup())
	if err != nil {
		t.Fatalf("reading the refresh token: %v", err)
	}
	if err := h.store.RevokeFamily(context.Background(), stored.Family); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}

	if _, err := h.exchange(t, refreshRequest(first.RefreshToken)); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("error = %v, want ErrTokenRevoked", err)
	}
}
