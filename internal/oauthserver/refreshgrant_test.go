package oauthserver

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// TestRefreshReplayOfAConsumedAndExpiredTokenRevokesTheFamily is the defect this
// pass fixes: a presented refresh token that was already rotated away AND is now
// past its own expiry used to be refused with "expired" before ever reaching the
// in-transaction reuse check, so the replay was reported but the family was never
// revoked. It must be treated exactly like any other reuse: the whole family dies,
// through the very same RevokeFamily storage call the rest of this package uses,
// and the client sees nothing more informative than any other invalid refresh
// token.
func TestRefreshReplayOfAConsumedAndExpiredTokenRevokesTheFamily(t *testing.T) {
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

	// first is now consumed (rotation happened above) AND, after this advance, past
	// its own ExpiresAt: exactly the case the defect mishandled.
	h.advance(h.srv.RefreshTokenTTL() + time.Minute)

	_, err = h.exchange(t, refreshRequest(first.RefreshToken))

	if !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("error = %v, want ErrRefreshTokenReused", err)
	}
	if errors.Is(err, ErrTokenExpired) {
		t.Fatal("the expired-token cause leaked alongside the reuse cause")
	}
	tokenErr := asTokenError(t, err)
	if got := tokenErr.Code(); got != ErrorInvalidGrant {
		t.Fatalf("Code() = %q, want %q", got, ErrorInvalidGrant)
	}
	if got := tokenErr.Description(); got != descRefreshNoLongerValid {
		t.Fatalf("Description() = %q, want the existing rotation-path description %q",
			got, descRefreshNoLongerValid)
	}
	if !h.store.familyRevoked(family.Family) {
		t.Fatal("a consumed-and-expired replay did not revoke the token family")
	}
	// The pre-check must revoke through the same store.RevokeFamily call the rest
	// of the package uses, not a parallel implementation.
	if h.store.revokeFamilyCalls != 1 {
		t.Fatalf("store.RevokeFamily was called %d times, want exactly 1", h.store.revokeFamilyCalls)
	}
	// And it must never reach RotateRefreshToken: the transactional path is
	// reserved for a still-live presented token.
	if h.store.rotations != 1 {
		t.Fatalf("store recorded %d rotations, want 1 (only the earlier legitimate refresh)",
			h.store.rotations)
	}
	// Problem 3 of the round-two review: nothing previously asserted that the
	// pre-check passes RevokeReasonReplay rather than the generic
	// RevokeReasonClient, so mutating that argument passed every existing test.
	if h.store.lastRevokeReason != RevokeReasonReplay {
		t.Fatalf("lastRevokeReason = %v, want RevokeReasonReplay", h.store.lastRevokeReason)
	}
}

// TestRefreshRevocationFailureStillReportsInvalidGrantButIsRecoverable is problem 2
// of the review: the client answer for a consumed-and-expired replay must never
// change depending on whether the family's revocation itself succeeded — telling a
// caller that revocation failed would be telling an attacker their replay worked —
// but the failure must not become invisible to anything upstream that inspects the
// error chain with errors.Is.
func TestRefreshRevocationFailureStillReportsInvalidGrantButIsRecoverable(t *testing.T) {
	h := newHarness(t)
	first := h.firstTokens(t)
	if _, err := h.exchange(t, refreshRequest(first.RefreshToken)); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	h.advance(h.srv.RefreshTokenTTL() + time.Minute)

	revokeFailure := errors.New("revocation failure injected by test")
	h.store.failOn["RevokeFamily"] = revokeFailure

	_, err := h.exchange(t, refreshRequest(first.RefreshToken))

	tokenErr := asTokenError(t, err)
	if got := tokenErr.Code(); got != ErrorInvalidGrant {
		t.Fatalf("Code() = %q, want %q even when revocation failed", got, ErrorInvalidGrant)
	}
	// Problem 4 of the round-two review: nothing pinned the HTTP status, so a
	// mutant that returned the same code and description with a 500 survived —
	// and a 500 here is exactly how a caller could detect that its replay failed
	// to take effect, which invalid_grant is supposed to never disclose.
	if got := tokenErr.Status(); got != http.StatusBadRequest {
		t.Fatalf("Status() = %d, want %d even when revocation failed", got, http.StatusBadRequest)
	}
	if got := tokenErr.Description(); got != descRefreshNoLongerValid {
		t.Fatalf("Description() = %q, want the unchanged %q", got, descRefreshNoLongerValid)
	}
	if !errors.Is(err, revokeFailure) {
		t.Fatalf("error = %v, want it to wrap the revocation failure so it is not silently discarded", err)
	}
}

// TestRefreshGrantJudgesBothChecksAgainstOneReadOfTheClock is problem 3 of the
// review: refreshGrant used to call s.now() twice, once for the consumed-and-expired
// pre-check and again for the plain expiry check. A token that was live at the first
// read and expired by the second took the plain-expiry path — which never revokes
// anything — even though it had already been consumed. Capturing now once at the top
// of the function and reusing it for both checks closes that window; this test moves
// the clock between what used to be the two reads and asserts the outcome is still
// reuse, not a plain expiry.
func TestRefreshGrantJudgesBothChecksAgainstOneReadOfTheClock(t *testing.T) {
	h := newHarness(t)
	first := h.firstTokens(t)
	if _, err := h.exchange(t, refreshRequest(first.RefreshToken)); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// first is now consumed by the rotation above, but still comfortably live.

	h.mu.Lock()
	baseline := h.nowReads
	h.mu.Unlock()
	var advanced bool
	h.afterRead = func(reads int, hh *harness) {
		if reads == baseline+1 && !advanced {
			advanced = true
			// Push the clock past the token's own expiry between what used to be
			// refreshGrant's two separate s.now() calls.
			hh.advance(hh.srv.RefreshTokenTTL() + time.Minute)
		}
	}

	_, err := h.exchange(t, refreshRequest(first.RefreshToken))

	if !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("error = %v, want ErrRefreshTokenReused: a clock read that moved between "+
			"the consumed-and-expired pre-check and the plain expiry check must not let a "+
			"live consumed token slip onto the plain-expiry path", err)
	}
	if errors.Is(err, ErrTokenExpired) {
		t.Fatal("the plain-expired cause leaked in alongside the reuse cause")
	}
}

// TestRefreshReplayOfAConsumedButLiveTokenStillUsesTheTransactionalPath proves the
// pre-check does not swallow the existing behaviour: a consumed token that has not
// yet reached its own expiry must still be caught by RotateRefreshToken, inside the
// transaction that makes reuse detection atomic against a concurrent refresher, and
// must NOT go through the new pre-check's direct RevokeFamily call.
func TestRefreshReplayOfAConsumedButLiveTokenStillUsesTheTransactionalPath(t *testing.T) {
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

	// No time advance: first is consumed but still well within its own lifetime.
	_, err = h.exchange(t, refreshRequest(first.RefreshToken))

	if !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("error = %v, want ErrRefreshTokenReused", err)
	}
	if !h.store.familyRevoked(family.Family) {
		t.Fatal("a consumed, live replay did not revoke the token family")
	}
	if h.store.revokeFamilyCalls != 0 {
		t.Fatalf("store.RevokeFamily was called %d times, want 0: a live token's reuse "+
			"must be caught by the transactional RotateRefreshToken path, not the pre-check",
			h.store.revokeFamilyCalls)
	}
}

// TestRefreshExpiredButNeverConsumedTokenIsNotTreatedAsReuse is the negative test
// the reordering could most easily break: a token that simply outlived its own
// lifetime, and was never rotated away, must still be refused with the plain
// expired description, must never revoke its family, and a still-live sibling
// generation in the same family must go on working.
func TestRefreshExpiredButNeverConsumedTokenIsNotTreatedAsReuse(t *testing.T) {
	h := newHarness(t)
	first := h.firstTokens(t)

	h.advance(h.srv.RefreshTokenTTL())

	_, err := h.exchange(t, refreshRequest(first.RefreshToken))

	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("error = %v, want ErrTokenExpired", err)
	}
	if errors.Is(err, ErrRefreshTokenReused) {
		t.Fatal("a merely expired, never-consumed token was treated as reuse")
	}
	tokenErr := asTokenError(t, err)
	if got := tokenErr.Description(); got != "The refresh token has expired." {
		t.Fatalf("Description() = %q, want the plain expired description", got)
	}
	stored, err := h.store.RefreshToken(context.Background(), first.RefreshToken.Lookup())
	if err != nil {
		t.Fatalf("reading the refresh token: %v", err)
	}
	if h.store.familyRevoked(stored.Family) {
		t.Fatal("an expired, never-consumed token replay revoked its family")
	}
	if h.store.revokeFamilyCalls != 0 {
		t.Fatalf("store.RevokeFamily was called %d times, want 0", h.store.revokeFamilyCalls)
	}
	if h.store.rotations != 0 {
		t.Fatal("an expired refresh token was rotated")
	}
}

// TestConcurrentRefreshGrantCallsStillProduceExactlyOneWinner proves the reordering
// in refreshGrant did not change how many callers of the GRANT see a success when
// several race to refresh one still-live token.
//
// This is a fake store guarded by one mutex with no start barrier, so it proves the
// grant's own behavior under that serialization, not storage-layer atomicity: it
// cannot catch a non-atomic RotateRefreshToken, and it can pass under scheduling
// that never truly overlaps. The real proof that RotateRefreshToken itself is atomic
// under genuine contention is internal/oauthstore/race_test.go's
// TestRotateRefreshTokenElectsOneWinnerAndKillsTheFamily, which drives the real
// SQLite-backed store.
func TestConcurrentRefreshGrantCallsStillProduceExactlyOneWinner(t *testing.T) {
	h := newHarness(t)
	first := h.firstTokens(t)

	const attempts = 8
	var (
		wg        sync.WaitGroup
		successes int32
	)
	wg.Add(attempts)
	for range attempts {
		go func() {
			defer wg.Done()
			if _, err := h.exchange(t, refreshRequest(first.RefreshToken)); err == nil {
				atomic.AddInt32(&successes, 1)
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}
	if h.store.rotations != 1 {
		t.Fatalf("store recorded %d rotations, want exactly 1", h.store.rotations)
	}
}

func TestRefreshRefusesARevokedFamily(t *testing.T) {
	h := newHarness(t)
	first := h.firstTokens(t)
	stored, err := h.store.RefreshToken(context.Background(), first.RefreshToken.Lookup())
	if err != nil {
		t.Fatalf("reading the refresh token: %v", err)
	}
	if err := h.store.RevokeFamily(context.Background(), stored.Family, RevokeReasonClient); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}

	if _, err := h.exchange(t, refreshRequest(first.RefreshToken)); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("error = %v, want ErrTokenRevoked", err)
	}
}
