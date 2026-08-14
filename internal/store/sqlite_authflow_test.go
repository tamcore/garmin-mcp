package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// testChallenge is a base64url SHA-256 digest, the shape a PKCE S256 challenge has.
const testChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

const (
	testHandle = "transaction-handle-1"
	testCode   = "authorization-code-1"
)

// seedTransaction stores a transaction for a client and returns its handle.
func seedTransaction(t *testing.T, s *store.SQLiteStore, clientID string) store.Secret {
	t.Helper()
	handle := store.NewSecret(testHandle)
	err := s.PutAuthTransaction(context.Background(), store.AuthTransactionDraft{
		Handle:        handle,
		ClientID:      clientID,
		RedirectURI:   testRedirectURI,
		Scopes:        []string{testScope},
		CodeChallenge: testChallenge,
		Lifetime:      10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("PutAuthTransaction: %v", err)
	}
	return handle
}

// seedCode stores an authorization code for a principal and client.
func seedCode(t *testing.T, s *store.SQLiteStore, principalID, clientID, material string) store.Secret {
	t.Helper()
	code := store.NewSecret(material)
	err := s.PutAuthCode(context.Background(), store.AuthCodeDraft{
		Code:          code,
		PrincipalID:   principalID,
		ClientID:      clientID,
		RedirectURI:   testRedirectURI,
		Audience:      testAudience,
		Scopes:        []string{testScope},
		CodeChallenge: testChallenge,
		Lifetime:      10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("PutAuthCode: %v", err)
	}
	return code
}

func TestAuthTransactionRoundTripAndPrincipalAttachment(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)
	principal := seedPrincipal(t, opened)
	handle := seedTransaction(t, opened, client.ID)

	transaction, err := opened.AuthTransaction(ctx, handle)
	if err != nil {
		t.Fatalf("AuthTransaction: %v", err)
	}
	if transaction.ClientID != client.ID {
		t.Errorf("ClientID = %q, want %q", transaction.ClientID, client.ID)
	}
	if transaction.PrincipalID != "" {
		t.Errorf("PrincipalID = %q, want it empty before the browser flow identifies a user",
			transaction.PrincipalID)
	}
	if transaction.RedirectURI != testRedirectURI || transaction.CodeChallenge != testChallenge {
		t.Errorf("transaction = %+v, want the stored redirect uri and challenge", transaction)
	}

	if err := opened.AttachPrincipal(ctx, handle, principal.ID); err != nil {
		t.Fatalf("AttachPrincipal: %v", err)
	}
	attached, err := opened.AuthTransaction(ctx, handle)
	if err != nil {
		t.Fatalf("AuthTransaction after attach: %v", err)
	}
	if attached.PrincipalID != principal.ID {
		t.Errorf("PrincipalID = %q, want %q", attached.PrincipalID, principal.ID)
	}

	if err := opened.DeleteAuthTransaction(ctx, handle); err != nil {
		t.Fatalf("DeleteAuthTransaction: %v", err)
	}
	// Deleting an absent transaction is not an error.
	if err := opened.DeleteAuthTransaction(ctx, handle); err != nil {
		t.Fatalf("second DeleteAuthTransaction: %v", err)
	}
	if _, err := opened.AuthTransaction(ctx, handle); !errors.Is(err, store.ErrTransactionNotFound) {
		t.Errorf("AuthTransaction after delete: err = %v, want ErrTransactionNotFound", err)
	}
}

// TestExpiredTransactionIsNeverReturnedBeforeCleanup is the on-access expiry check for
// authorization state. Cleanup is not called.
func TestExpiredTransactionIsNeverReturnedBeforeCleanup(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)
	principal := seedPrincipal(t, opened)
	handle := seedTransaction(t, opened, client.ID)

	clock.advance(11 * time.Minute)

	if _, err := opened.AuthTransaction(ctx, handle); !errors.Is(err, store.ErrTransactionNotFound) {
		t.Fatalf("expired transaction: err = %v, want ErrTransactionNotFound", err)
	}
	// An expired transaction cannot be resumed by attaching a principal either.
	err := opened.AttachPrincipal(ctx, handle, principal.ID)
	if !errors.Is(err, store.ErrTransactionNotFound) {
		t.Fatalf("AttachPrincipal on an expired transaction: err = %v, want ErrTransactionNotFound", err)
	}
}

func TestPutAuthTransactionRefusesBadInput(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)

	base := func() store.AuthTransactionDraft {
		return store.AuthTransactionDraft{
			Handle:        store.NewSecret("another-handle"),
			ClientID:      client.ID,
			RedirectURI:   testRedirectURI,
			Scopes:        []string{testScope},
			CodeChallenge: testChallenge,
			Lifetime:      10 * time.Minute,
		}
	}

	cases := map[string]func(d *store.AuthTransactionDraft){
		"no handle":            func(d *store.AuthTransactionDraft) { d.Handle = store.Secret{} },
		"no client id":         func(d *store.AuthTransactionDraft) { d.ClientID = "" },
		"no challenge":         func(d *store.AuthTransactionDraft) { d.CodeChallenge = "" },
		"a challenge with '+'": func(d *store.AuthTransactionDraft) { d.CodeChallenge = "abc+def" },
		"a challenge with '='": func(d *store.AuthTransactionDraft) { d.CodeChallenge = "abcdef=" },
		"an oversized challenge": func(d *store.AuthTransactionDraft) {
			d.CodeChallenge = strings.Repeat("a", 200)
		},
		caseNoScopes:          func(d *store.AuthTransactionDraft) { d.Scopes = nil },
		caseZeroLifetime:      func(d *store.AuthTransactionDraft) { d.Lifetime = 0 },
		"an endless lifetime": func(d *store.AuthTransactionDraft) { d.Lifetime = 48 * time.Hour },
		"a negative lifetime": func(d *store.AuthTransactionDraft) { d.Lifetime = -time.Second },
	}
	for name, mutate := range cases {
		draft := base()
		mutate(&draft)
		if err := opened.PutAuthTransaction(ctx, draft); !errors.Is(err, store.ErrInvalidArgument) {
			t.Errorf("PutAuthTransaction with %s: err = %v, want ErrInvalidArgument", name, err)
		}
	}

	unknown := base()
	unknown.ClientID = testUnknownID
	if err := opened.PutAuthTransaction(ctx, unknown); !errors.Is(err, store.ErrClientNotFound) {
		t.Errorf("unknown client: err = %v, want ErrClientNotFound", err)
	}
}

func TestPutAuthTransactionRefusesAReusedHandle(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	client := seedClient(t, opened)
	seedTransaction(t, opened, client.ID)

	err := opened.PutAuthTransaction(context.Background(), store.AuthTransactionDraft{
		Handle:        store.NewSecret(testHandle),
		ClientID:      client.ID,
		RedirectURI:   testRedirectURI,
		Scopes:        []string{testScope},
		CodeChallenge: testChallenge,
		Lifetime:      10 * time.Minute,
	})
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("reused handle: err = %v, want ErrInvalidArgument", err)
	}
}

func TestConsumeAuthCodeRedeemsExactlyOnce(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)
	code := seedCode(t, opened, principal.ID, client.ID, testCode)

	redeemed, err := opened.ConsumeAuthCode(ctx, code)
	if err != nil {
		t.Fatalf("ConsumeAuthCode: %v", err)
	}
	if redeemed.PrincipalID != principal.ID || redeemed.ClientID != client.ID {
		t.Errorf("redeemed = %+v, want the seeded principal and client", redeemed)
	}
	if redeemed.Audience != testAudience || redeemed.CodeChallenge != testChallenge {
		t.Errorf("redeemed = %+v, want the stored audience and challenge", redeemed)
	}
	if redeemed.RedirectURI != testRedirectURI {
		t.Errorf("RedirectURI = %q, want %q", redeemed.RedirectURI, testRedirectURI)
	}

	// The replay is reported distinctly, because it is a security event.
	if _, err := opened.ConsumeAuthCode(ctx, code); !errors.Is(err, store.ErrCodeAlreadyUsed) {
		t.Fatalf("replay: err = %v, want ErrCodeAlreadyUsed", err)
	}
}

// TestExpiredAuthCodeIsNeverRedeemedBeforeCleanup is the on-access expiry check for
// codes. An expired code reports ErrCodeNotFound rather than a distinct expiry error, so
// expiry is not an oracle for whether a code ever existed.
func TestExpiredAuthCodeIsNeverRedeemedBeforeCleanup(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)
	code := seedCode(t, opened, principal.ID, client.ID, testCode)

	clock.advance(11 * time.Minute)

	if _, err := opened.ConsumeAuthCode(ctx, code); !errors.Is(err, store.ErrCodeNotFound) {
		t.Fatalf("expired code: err = %v, want ErrCodeNotFound", err)
	}
}

func TestConsumeAuthCodeRefusesUnknownMaterial(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	_, err := opened.ConsumeAuthCode(ctx, store.NewSecret("never-issued"))
	if !errors.Is(err, store.ErrCodeNotFound) {
		t.Errorf("unknown code: err = %v, want ErrCodeNotFound", err)
	}
	if _, err := opened.ConsumeAuthCode(ctx, store.Secret{}); !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("zero code: err = %v, want ErrInvalidArgument", err)
	}
}

func TestPutAuthCodeRefusesBadInput(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)

	base := func() store.AuthCodeDraft {
		return store.AuthCodeDraft{
			Code:          store.NewSecret("another-code"),
			PrincipalID:   principal.ID,
			ClientID:      client.ID,
			RedirectURI:   testRedirectURI,
			Audience:      testAudience,
			Scopes:        []string{testScope},
			CodeChallenge: testChallenge,
			Lifetime:      10 * time.Minute,
		}
	}

	cases := map[string]func(d *store.AuthCodeDraft){
		"no code":         func(d *store.AuthCodeDraft) { d.Code = store.Secret{} },
		"no principal id": func(d *store.AuthCodeDraft) { d.PrincipalID = "" },
		"no client id":    func(d *store.AuthCodeDraft) { d.ClientID = "" },
		"no audience":     func(d *store.AuthCodeDraft) { d.Audience = "" },
		"no challenge":    func(d *store.AuthCodeDraft) { d.CodeChallenge = "" },
		caseNoScopes:      func(d *store.AuthCodeDraft) { d.Scopes = nil },
		caseZeroLifetime:  func(d *store.AuthCodeDraft) { d.Lifetime = 0 },
	}
	for name, mutate := range cases {
		draft := base()
		mutate(&draft)
		if err := opened.PutAuthCode(ctx, draft); !errors.Is(err, store.ErrInvalidArgument) {
			t.Errorf("PutAuthCode with %s: err = %v, want ErrInvalidArgument", name, err)
		}
	}

	unknown := base()
	unknown.PrincipalID = testUnknownID
	if err := opened.PutAuthCode(ctx, unknown); !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("unknown principal: err = %v, want ErrPrincipalNotFound", err)
	}
}
