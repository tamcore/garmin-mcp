package oauthserver

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
)

const testPrincipalID = "11111111-2222-3333-4444-555555555555"

// authenticated runs a request through BeginAuthorization and attaches a principal,
// which is the state the consent step starts from.
func (h *harness) authenticated(t *testing.T, req AuthorizeRequest) (Secret, Transaction) {
	t.Helper()
	auth, err := h.begin(t, req)
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}
	tx, err := h.srv.AttachPrincipal(
		context.Background(), auth.Capability, mustPrincipal(t, testPrincipalID))
	if err != nil {
		t.Fatalf("AttachPrincipal: %v", err)
	}
	return auth.Capability, tx
}

func TestTransactionIsAddressedOnlyByItsCapability(t *testing.T) {
	h := newHarness(t)
	auth, err := h.begin(t, validAuthorizeRequest())
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}

	if _, err := h.srv.Transaction(context.Background(), auth.Capability); err != nil {
		t.Fatalf("Transaction: %v", err)
	}

	other, err := NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	for name, capability := range map[string]Secret{
		"another capability": other,
		"no capability":      {},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h.srv.Transaction(context.Background(), capability)
			if !errors.Is(err, ErrTransactionNotFound) {
				t.Fatalf("Transaction error = %v, want ErrTransactionNotFound", err)
			}
		})
	}
}

func TestTransactionExpiresAndBecomesUnusable(t *testing.T) {
	h := newHarness(t)
	auth, err := h.begin(t, validAuthorizeRequest())
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}

	h.advance(h.srv.TransactionTTL())

	if _, err := h.srv.Transaction(context.Background(), auth.Capability); !errors.Is(
		err, ErrTransactionExpired) {
		t.Fatalf("Transaction error = %v, want ErrTransactionExpired", err)
	}
	if h.store.transactionCount() != 0 {
		t.Fatal("an expired transaction was left behind")
	}
}

func TestAttachPrincipalAdvancesTheTransactionExactlyOnce(t *testing.T) {
	h := newHarness(t)
	capability, tx := h.authenticated(t, validAuthorizeRequest())

	if tx.Stage != StageAuthenticated {
		t.Fatalf("Stage = %v, want authenticated", tx.Stage)
	}
	if tx.Principal.ID() != testPrincipalID {
		t.Fatalf("Principal = %q", tx.Principal.ID())
	}
	if tx.Version == 0 {
		t.Fatal("the compare-and-set version was not advanced")
	}

	_, err := h.srv.AttachPrincipal(
		context.Background(), capability, mustPrincipal(t, testPrincipalID))
	if !errors.Is(err, ErrTransactionStage) {
		t.Fatalf("a second AttachPrincipal error = %v, want ErrTransactionStage", err)
	}
}

func TestAttachPrincipalRefusesAZeroPrincipal(t *testing.T) {
	h := newHarness(t)
	auth, err := h.begin(t, validAuthorizeRequest())
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}

	if _, err := h.srv.AttachPrincipal(
		context.Background(), auth.Capability, identityZeroPrincipal(),
	); err == nil {
		t.Fatal("AttachPrincipal accepted the zero principal")
	}
	if h.store.transactionCount() != 1 {
		t.Fatal("the transaction was disturbed by a refused principal")
	}
}

func TestGrantConsentIssuesACodeBoundToEverything(t *testing.T) {
	h := newHarness(t)
	capability, tx := h.authenticated(t, validAuthorizeRequest())

	completion, err := h.srv.GrantConsent(context.Background(), capability)
	if err != nil {
		t.Fatalf("GrantConsent: %v", err)
	}

	code := assertRedirectCarriesCode(t, completion.RedirectTo)
	stored, err := h.store.ConsumeCode(context.Background(), SecretFromString(code).Lookup())
	if err != nil {
		t.Fatalf("the issued code is not stored under its digest: %v", err)
	}
	if stored.ClientID != testClientID {
		t.Fatalf("code is bound to client %q", stored.ClientID)
	}
	if !stored.RedirectURI.Equal(tx.RedirectURI) || !stored.Resource.Equal(tx.Resource) {
		t.Fatal("code is not bound to the redirect URI and resource of the request")
	}
	if !stored.Scopes.Equal(tx.Scopes) {
		t.Fatalf("code scopes = %q, want %q", stored.Scopes, tx.Scopes)
	}
	if stored.Principal.ID() != testPrincipalID {
		t.Fatalf("code principal = %q", stored.Principal.ID())
	}
	if err := stored.Challenge.Verify(testVerifier); err != nil {
		t.Fatalf("code is not bound to the PKCE challenge: %v", err)
	}
	if want := testNow.Add(h.srv.CodeTTL()); !stored.ExpiresAt.Equal(want) {
		t.Fatalf("code expiry = %v, want %v", stored.ExpiresAt, want)
	}
	if h.srv.CodeTTL() > MaxCodeTTL {
		t.Fatal("the code lifetime exceeds five minutes")
	}
	if h.store.transactionCount() != 0 {
		t.Fatal("the transaction survived a completed authorization")
	}
}

func assertRedirectCarriesCode(t *testing.T, location string) string {
	t.Helper()
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("redirect does not parse: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != testRedirectHost || parsed.Path != "/cb" {
		t.Fatalf("redirect = %q, want the registered redirect URI", location)
	}
	query := parsed.Query()
	if query.Get("state") != testState {
		t.Fatalf("state = %q, want it echoed byte for byte", query.Get("state"))
	}
	if query.Has("error") {
		t.Fatalf("a successful redirect carried an error: %q", location)
	}
	code := query.Get("code")
	if code == "" {
		t.Fatal("the redirect carried no code")
	}
	return code
}

func TestGrantConsentRequiresAResolvedPrincipal(t *testing.T) {
	h := newHarness(t)
	auth, err := h.begin(t, validAuthorizeRequest())
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}

	if _, err := h.srv.GrantConsent(context.Background(), auth.Capability); !errors.Is(
		err, ErrTransactionStage) {
		t.Fatalf("GrantConsent error = %v, want ErrTransactionStage", err)
	}
	if h.store.consentCount() != 0 {
		t.Fatal("consent was persisted before the principal was resolved")
	}
}

func TestGrantConsentIsSingleUse(t *testing.T) {
	h := newHarness(t)
	capability, _ := h.authenticated(t, validAuthorizeRequest())

	if _, err := h.srv.GrantConsent(context.Background(), capability); err != nil {
		t.Fatalf("GrantConsent: %v", err)
	}
	if _, err := h.srv.GrantConsent(context.Background(), capability); !errors.Is(
		err, ErrTransactionNotFound) {
		t.Fatalf("a replayed GrantConsent error = %v, want ErrTransactionNotFound", err)
	}
}

func TestGrantConsentUnderRaceCompletesExactlyOnce(t *testing.T) {
	h := newHarness(t)
	capability, _ := h.authenticated(t, validAuthorizeRequest())

	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Go(func() {
			_, results[i] = h.srv.GrantConsent(context.Background(), capability)
		})
	}
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("%d concurrent GrantConsent calls succeeded, want exactly 1", successes)
	}
}

// TestConsentRequiredIsTheConfusedDeputyMitigation is the direct test the brief asks
// for: consent is bound to the whole tuple, and a scope increase or a redirect change
// forces a fresh decision.
func TestConsentRequiredIsTheConfusedDeputyMitigation(t *testing.T) {
	spec := publicClientSpec()
	spec.RedirectURIs = []string{testRedirect, "https://client.example/second"}
	h := newHarness(t, spec)

	first := validAuthorizeRequest()
	first.Scope = testScopeProfile
	capability, _ := h.authenticated(t, first)
	required, err := h.srv.ConsentRequired(context.Background(), capability)
	if err != nil {
		t.Fatalf("ConsentRequired: %v", err)
	}
	if !required {
		t.Fatal("the first authorization for a client must require consent")
	}
	if _, err := h.srv.GrantConsent(context.Background(), capability); err != nil {
		t.Fatalf("GrantConsent: %v", err)
	}

	cases := map[string]struct {
		mutate       func(*AuthorizeRequest)
		wantRequired bool
	}{
		"identical request": {func(*AuthorizeRequest) {}, false},
		"narrower scope": {
			func(r *AuthorizeRequest) { r.Scope = testScopeProfile }, false,
		},
		"wider scope": {
			func(r *AuthorizeRequest) { r.Scope = testScopesBoth }, true,
		},
		"different scope": {
			func(r *AuthorizeRequest) { r.Scope = testScopeHealth }, true,
		},
		"changed redirect URI": {
			func(r *AuthorizeRequest) { r.RedirectURI = "https://client.example/second" }, true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := validAuthorizeRequest()
			tc.mutate(&req)
			capability, _ := h.authenticated(t, req)

			required, err := h.srv.ConsentRequired(context.Background(), capability)
			if err != nil {
				t.Fatalf("ConsentRequired: %v", err)
			}
			if required != tc.wantRequired {
				t.Fatalf("ConsentRequired() = %v, want %v", required, tc.wantRequired)
			}
		})
	}
}

func TestConsentRequiredIsBoundToThePrincipal(t *testing.T) {
	h := newHarness(t)
	capability, _ := h.authenticated(t, validAuthorizeRequest())
	if _, err := h.srv.GrantConsent(context.Background(), capability); err != nil {
		t.Fatalf("GrantConsent: %v", err)
	}

	auth, err := h.begin(t, validAuthorizeRequest())
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}
	other := mustPrincipal(t, "99999999-0000-0000-0000-000000000000")
	if _, err := h.srv.AttachPrincipal(context.Background(), auth.Capability, other); err != nil {
		t.Fatalf("AttachPrincipal: %v", err)
	}

	required, err := h.srv.ConsentRequired(context.Background(), auth.Capability)
	if err != nil {
		t.Fatalf("ConsentRequired: %v", err)
	}
	if !required {
		t.Fatal("another principal must not inherit the first principal's consent")
	}
}

func TestConsentRequiredNeedsAResolvedPrincipal(t *testing.T) {
	h := newHarness(t)
	auth, err := h.begin(t, validAuthorizeRequest())
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}

	if _, err := h.srv.ConsentRequired(context.Background(), auth.Capability); !errors.Is(
		err, ErrTransactionStage) {
		t.Fatalf("ConsentRequired error = %v, want ErrTransactionStage", err)
	}
}

func TestDenyAuthorizationRedirectsAccessDeniedAndDiscardsEverything(t *testing.T) {
	h := newHarness(t)
	capability, _ := h.authenticated(t, validAuthorizeRequest())

	completion, err := h.srv.DenyAuthorization(context.Background(), capability)
	if err != nil {
		t.Fatalf("DenyAuthorization: %v", err)
	}

	parsed, err := url.Parse(completion.RedirectTo)
	if err != nil {
		t.Fatalf("redirect does not parse: %v", err)
	}
	query := parsed.Query()
	if query.Get("error") != ErrorAccessDenied {
		t.Fatalf("error = %q, want %q", query.Get("error"), ErrorAccessDenied)
	}
	if query.Has("code") {
		t.Fatal("a denial must not carry a code")
	}
	if query.Get("state") != testState {
		t.Fatalf("state = %q, want it echoed byte for byte", query.Get("state"))
	}
	if h.store.consentCount() != 0 {
		t.Fatal("a denial persisted consent")
	}
	if h.store.transactionCount() != 0 {
		t.Fatal("a denial left the transaction usable")
	}
}

func TestGrantConsentOnAnExpiredTransactionIssuesNothing(t *testing.T) {
	h := newHarness(t)
	capability, _ := h.authenticated(t, validAuthorizeRequest())

	h.advance(h.srv.TransactionTTL())

	if _, err := h.srv.GrantConsent(context.Background(), capability); !errors.Is(
		err, ErrTransactionExpired) {
		t.Fatalf("GrantConsent error = %v, want ErrTransactionExpired", err)
	}
	if h.store.consentCount() != 0 {
		t.Fatal("an expired transaction persisted consent")
	}
}
