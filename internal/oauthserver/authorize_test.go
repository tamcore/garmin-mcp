package oauthserver

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func validAuthorizeRequest() AuthorizeRequest {
	return AuthorizeRequest{
		ResponseType:        paramCode,
		ClientID:            testClientID,
		RedirectURI:         testRedirect,
		Scope:               testScopeProfile,
		State:               testState,
		CodeChallenge:       testChallenge(),
		CodeChallengeMethod: string(MethodS256),
		Resource:            testResourceURI,
	}
}

func (h *harness) begin(t *testing.T, req AuthorizeRequest) (Authorization, error) {
	t.Helper()
	return h.srv.BeginAuthorization(context.Background(), req)
}

func asAuthorizeError(t *testing.T, err error) *AuthorizeError {
	t.Helper()
	var authErr *AuthorizeError
	if !errors.As(err, &authErr) {
		t.Fatalf("error is not an *AuthorizeError: %v", err)
	}
	return authErr
}

func TestBeginAuthorizationCreatesATransaction(t *testing.T) {
	h := newHarness(t)

	got, err := h.begin(t, validAuthorizeRequest())
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}

	if got.Capability.IsZero() {
		t.Fatal("no transaction capability was issued")
	}
	if want := testNow.Add(h.srv.TransactionTTL()); !got.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, want)
	}
	if h.store.transactionCount() != 1 {
		t.Fatalf("stored %d transactions, want 1", h.store.transactionCount())
	}

	tx, err := h.store.Transaction(context.Background(), got.Capability.Lookup())
	if err != nil {
		t.Fatalf("the transaction is not addressed by the capability digest: %v", err)
	}
	if tx.Stage != StagePending {
		t.Fatalf("Stage = %v, want pending", tx.Stage)
	}
	if tx.Principal.IsValid() {
		t.Fatal("a pending transaction must carry no principal")
	}
	if tx.State.Reveal() != testState {
		t.Fatalf("the client state was not preserved byte for byte: %q", tx.State.Reveal())
	}
	if !tx.RedirectURI.Equal(mustRedirectURI(t, testRedirect)) {
		t.Fatalf("RedirectURI = %q", tx.RedirectURI)
	}
	if !tx.Resource.Equal(mustResource(t, testResourceURI)) {
		t.Fatalf("Resource = %q", tx.Resource)
	}
	if err := tx.Challenge.Verify(testVerifier); err != nil {
		t.Fatalf("the PKCE challenge was not bound: %v", err)
	}
}

func TestBeginAuthorizationDisclosesTheClientWithoutBinding(t *testing.T) {
	h := newHarness(t)

	got, err := h.begin(t, validAuthorizeRequest())
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}

	disclosure := got.Disclosure
	if disclosure.ClientID != testClientID || disclosure.ClientName != testClientName {
		t.Fatalf("disclosure does not identify the client: %+v", disclosure)
	}
	if disclosure.RedirectHost != testRedirectHost {
		t.Fatalf("RedirectHost = %q, want client.example", disclosure.RedirectHost)
	}
	if disclosure.Resource != testResourceURI {
		t.Fatalf("Resource = %q", disclosure.Resource)
	}
	if len(disclosure.Scopes) != 1 || disclosure.Scopes[0] != testScopeProfile {
		t.Fatalf("Scopes = %v", disclosure.Scopes)
	}
}

// TestBeginAuthorizationRendersLocallyBeforeTheRedirectIsTrusted is the
// open-redirect matrix. Until the client and its exact redirect URI are both
// validated, an OAuth error must never be delivered by redirecting.
func TestBeginAuthorizationRendersLocallyBeforeTheRedirectIsTrusted(t *testing.T) {
	with := func(mutate func(*AuthorizeRequest)) AuthorizeRequest {
		req := validAuthorizeRequest()
		mutate(&req)
		return req
	}

	cases := map[string]struct {
		req       AuthorizeRequest
		wantCause error
	}{
		"no client id":     {with(func(r *AuthorizeRequest) { r.ClientID = "" }), ErrInvalidClient},
		"padded client id": {with(func(r *AuthorizeRequest) { r.ClientID = " x " }), ErrInvalidClient},
		"unknown client": {with(func(r *AuthorizeRequest) {
			r.ClientID = testUnknownClient
		}), ErrUnknownClient},
		"no redirect": {with(func(r *AuthorizeRequest) {
			r.RedirectURI = ""
		}), ErrRedirectURINotRegistered},
		"malformed": {with(func(r *AuthorizeRequest) {
			r.RedirectURI = "://"
		}), ErrRedirectURINotRegistered},
		"attacker redirect": {with(func(r *AuthorizeRequest) {
			r.RedirectURI = "https://evil.test/cb"
		}), ErrRedirectURINotRegistered},
		"trailing slash": {with(func(r *AuthorizeRequest) {
			r.RedirectURI = testRedirect + "/"
		}), ErrRedirectURINotRegistered},
		"added query": {with(func(r *AuthorizeRequest) {
			r.RedirectURI = testRedirect + "?x=1"
		}), ErrRedirectURINotRegistered},
		"appended fragment": {with(func(r *AuthorizeRequest) {
			r.RedirectURI = testRedirect + "#f"
		}), ErrRedirectURINotRegistered},
		"userinfo in the authority": {with(func(r *AuthorizeRequest) {
			r.RedirectURI = "https://u@client.example/cb"
		}), ErrRedirectURINotRegistered},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.begin(t, tc.req)

			authErr := asAuthorizeError(t, err)
			if authErr.IsRedirect() || authErr.Location() != "" {
				t.Fatalf("error was delivered by redirect to %q", authErr.Location())
			}
			if authErr.Status() != 400 {
				t.Fatalf("Status() = %d, want 400", authErr.Status())
			}
			if !errors.Is(err, tc.wantCause) {
				t.Fatalf("error = %v, want it to wrap %v", err, tc.wantCause)
			}
			if h.store.transactionCount() != 0 {
				t.Fatal("a refused authorization request created a transaction")
			}
		})
	}
}

// TestBeginAuthorizationRedirectsProtocolErrorsAfterValidation is the other half:
// once the client and the exact redirect URI are trusted, an OAuth error goes back
// to the client, with its state echoed byte for byte.
func TestBeginAuthorizationRedirectsProtocolErrorsAfterValidation(t *testing.T) {
	with := func(mutate func(*AuthorizeRequest)) AuthorizeRequest {
		req := validAuthorizeRequest()
		mutate(&req)
		return req
	}
	longState := strings.Repeat("s", MaxClientStateLen+1)

	cases := map[string]struct {
		req        AuthorizeRequest
		wantCode   string
		wantCause  error
		wantNoEcho bool
	}{
		"implicit grant": {
			with(func(r *AuthorizeRequest) { r.ResponseType = "token" }),
			ErrorUnsupportedResponseType, ErrUnsupportedResponseType, false,
		},
		"hybrid": {
			with(func(r *AuthorizeRequest) { r.ResponseType = paramCode + " token" }),
			ErrorUnsupportedResponseType, ErrUnsupportedResponseType, false,
		},
		"no response type": {
			with(func(r *AuthorizeRequest) { r.ResponseType = "" }),
			ErrorUnsupportedResponseType, ErrUnsupportedResponseType, false,
		},
		"pkce downgraded to plain": {
			with(func(r *AuthorizeRequest) {
				r.CodeChallengeMethod = challengeMethodPlain
				r.CodeChallenge = testVerifier
			}),
			ErrorInvalidRequest, ErrInvalidCodeChallenge, false,
		},
		"pkce absent": {
			with(func(r *AuthorizeRequest) {
				r.CodeChallenge = ""
				r.CodeChallengeMethod = ""
			}),
			ErrorInvalidRequest, ErrInvalidCodeChallenge, false,
		},
		"pkce malformed": {
			with(func(r *AuthorizeRequest) { r.CodeChallenge = "short" }),
			ErrorInvalidRequest, ErrInvalidCodeChallenge, false,
		},
		"scope not registered": {
			with(func(r *AuthorizeRequest) { r.Scope = "garmin.everything.write" }),
			ErrorInvalidScope, ErrInvalidScope, false,
		},
		"scope empty": {
			with(func(r *AuthorizeRequest) { r.Scope = "" }),
			ErrorInvalidScope, ErrInvalidScope, false,
		},
		"scope malformed": {
			with(func(r *AuthorizeRequest) { r.Scope = testMalformedScope }),
			ErrorInvalidScope, ErrInvalidScope, false,
		},
		"resource absent": {
			with(func(r *AuthorizeRequest) { r.Resource = "" }),
			ErrorInvalidTarget, ErrInvalidResource, false,
		},
		"resource other": {
			with(func(r *AuthorizeRequest) { r.Resource = testOtherResource }),
			ErrorInvalidTarget, ErrResourceNotAllowed, false,
		},
		"resource malformed": {
			with(func(r *AuthorizeRequest) { r.Resource = "http://mcp.example/mcp" }),
			ErrorInvalidTarget, ErrInvalidResource, false,
		},
		"state over long": {
			with(func(r *AuthorizeRequest) { r.State = longState }),
			ErrorInvalidRequest, ErrInvalidState, true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.begin(t, tc.req)

			authErr := asAuthorizeError(t, err)
			if !authErr.IsRedirect() {
				t.Fatalf("error should have been redirected, got a local %q", authErr.Code())
			}
			if authErr.Code() != tc.wantCode {
				t.Fatalf("Code() = %q, want %q", authErr.Code(), tc.wantCode)
			}
			if !errors.Is(err, tc.wantCause) {
				t.Fatalf("error = %v, want it to wrap %v", err, tc.wantCause)
			}
			assertRedirectShape(t, authErr, tc.wantCode, tc.wantNoEcho)
			if h.store.transactionCount() != 0 {
				t.Fatal("a refused authorization request created a transaction")
			}
		})
	}
}

func assertRedirectShape(t *testing.T, authErr *AuthorizeError, wantCode string, wantNoEcho bool) {
	t.Helper()
	location, err := url.Parse(authErr.Location())
	if err != nil {
		t.Fatalf("Location() does not parse: %v", err)
	}
	if location.Scheme != "https" || location.Host != testRedirectHost || location.Path != "/cb" {
		t.Fatalf("Location() = %q, want the registered redirect URI", authErr.Location())
	}
	query := location.Query()
	if query.Get("error") != wantCode {
		t.Fatalf("error parameter = %q, want %q", query.Get("error"), wantCode)
	}
	if query.Has("code") {
		t.Fatal("an error redirect must not carry a code")
	}
	switch {
	case wantNoEcho && query.Has("state"):
		t.Fatal("an unusable state must not be echoed")
	case !wantNoEcho && query.Get("state") != testState:
		t.Fatalf("state = %q, want it echoed byte for byte", query.Get("state"))
	}
}

func TestAuthorizeErrorsNeverCarrySecrets(t *testing.T) {
	h := newHarness(t)
	req := validAuthorizeRequest()
	req.Scope = "garmin.everything.write"

	_, err := h.begin(t, req)

	authErr := asAuthorizeError(t, err)
	for _, text := range []string{err.Error(), authErr.Description(), authErr.Error()} {
		for label, secret := range map[string]string{
			"client state":   testState,
			"code challenge": testChallenge(),
			"code verifier":  testVerifier,
		} {
			if strings.Contains(text, secret) {
				t.Fatalf("error text leaked the %s: %q", label, text)
			}
		}
	}
}

func authorizeValues() url.Values {
	return url.Values{
		paramResponseType:        {"code"},
		paramClientID:            {testClientID},
		paramRedirectURI:         {testRedirect},
		paramScope:               {testScopeProfile},
		paramState:               {testState},
		paramCodeChallenge:       {testChallenge()},
		paramCodeChallengeMethod: {string(MethodS256)},
		paramResource:            {testResourceURI},
	}
}

func TestParseAuthorizeQueryRejectsDuplicateSecurityParameters(t *testing.T) {
	for _, key := range []string{
		paramClientID, paramRedirectURI, paramResponseType, paramScope,
		paramState, paramCodeChallenge, paramCodeChallengeMethod, paramResource,
	} {
		t.Run(key, func(t *testing.T) {
			values := authorizeValues()
			values.Add(key, values.Get(key))

			if _, err := ParseAuthorizeQuery(values); !errors.Is(err, ErrDuplicateParameter) {
				t.Fatalf("ParseAuthorizeQuery error = %v, want ErrDuplicateParameter", err)
			}
		})
	}
}

func TestParseAuthorizeQueryReadsEveryParameter(t *testing.T) {
	got, err := ParseAuthorizeQuery(authorizeValues())
	if err != nil {
		t.Fatalf("ParseAuthorizeQuery: %v", err)
	}
	if got != validAuthorizeRequest() {
		t.Fatalf("ParseAuthorizeQuery() = %+v", got)
	}
}

func TestBeginAuthorizationSurfacesAStorageFailureWithoutATransaction(t *testing.T) {
	h := newHarness(t)
	h.store.failOn["CreateTransaction"] = errors.New("disk on fire")

	_, err := h.begin(t, validAuthorizeRequest())

	if !errors.Is(err, ErrStorage) {
		t.Fatalf("error = %v, want it to wrap ErrStorage", err)
	}
	authErr := asAuthorizeError(t, err)
	if authErr.Code() != ErrorServerError {
		t.Fatalf("Code() = %q, want %q", authErr.Code(), ErrorServerError)
	}
	if strings.Contains(authErr.Description(), "disk on fire") {
		t.Fatalf("the storage failure reached the client: %q", authErr.Description())
	}
}
