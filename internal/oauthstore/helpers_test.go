package oauthstore_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/oauthstore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Fixtures for the adapter tests. Every test here runs against a real migrated
// SQLite database, because the whole point of the package is the pairing: a fake
// store would assert only that the adapter calls itself correctly.
const (
	testEmail       = "rider@example.com"
	testClientName  = "Test MCP Client"
	testRedirectRaw = "https://client.example/callback"
	testResourceRaw = "https://mcp.example/"
	testScopeRaw    = "garmin.read"
	testChallenge   = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

// clock is a manually advanced clock guarded by a mutex, so the race tests can read
// it from several goroutines while a single-threaded test advances it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// staticClients is the operator-registered client source the adapter delegates to.
// Clients are configuration, not rows: they are registered by an operator and never
// read from SQLite.
type staticClients struct {
	client oauthserver.Client
}

func (s staticClients) Client(_ context.Context, clientID string) (oauthserver.Client, error) {
	if clientID != s.client.ID() {
		return oauthserver.Client{}, fmt.Errorf("no client %q: %w", clientID, oauthserver.ErrUnknownClient)
	}
	return s.client, nil
}

// fixture is one opened adapter over one real store, with a principal and a client
// that already exist as rows, so every foreign key the store enforces is satisfied.
type fixture struct {
	adapter   *oauthstore.Adapter
	sqlite    *store.SQLiteStore
	clock     *clock
	principal identity.Principal
	clientID  string
	redirect  oauthserver.RedirectURI
	resource  oauthserver.Resource
	scopes    oauthserver.ScopeSet
	challenge oauthserver.CodeChallenge
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	clk := newClock()
	sqlite := openStore(t, clk)

	principalRow, err := sqlite.CreatePrincipal(ctx, testEmail)
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	clientRow, err := sqlite.RegisterClient(ctx, store.ClientRegistration{
		Name:         testClientName,
		RedirectURIs: []string{testRedirectRaw},
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}

	principal, err := identity.NewPrincipal(principalRow.ID)
	if err != nil {
		t.Fatalf("NewPrincipal: %v", err)
	}
	adapter, err := oauthstore.New(sqlite, staticClients{client: newTestClient(t, clientRow.ID)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return fixture{
		adapter:   adapter,
		sqlite:    sqlite,
		clock:     clk,
		principal: principal,
		clientID:  clientRow.ID,
		redirect:  mustRedirect(t, testRedirectRaw),
		resource:  mustResource(t, testResourceRaw),
		scopes:    mustScopes(t, testScopeRaw),
		challenge: mustChallenge(t),
	}
}

func openStore(t *testing.T, clk *clock) *store.SQLiteStore {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	key, err := cryptostore.GenerateKey(1)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	opened, err := store.OpenSQLite(context.Background(), store.SQLiteConfig{
		Path: filepath.Join(resolved, "garmin-mcp.db"),
		Key:  key,
		Now:  clk.Now,
	})
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() {
		if err := opened.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return opened
}

func newTestClient(t *testing.T, id string) oauthserver.Client {
	t.Helper()
	client, err := oauthserver.NewClient(oauthserver.ClientSpec{
		ID:                      id,
		Name:                    testClientName,
		RedirectURIs:            []string{testRedirectRaw},
		Scopes:                  testScopeRaw,
		Resources:               []string{testResourceRaw},
		TokenEndpointAuthMethod: string(oauthserver.AuthMethodNone),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func mustRedirect(t *testing.T, raw string) oauthserver.RedirectURI {
	t.Helper()
	uri, err := oauthserver.ParseRedirectURI(raw)
	if err != nil {
		t.Fatalf("ParseRedirectURI: %v", err)
	}
	return uri
}

func mustResource(t *testing.T, raw string) oauthserver.Resource {
	t.Helper()
	resource, err := oauthserver.ParseResource(raw)
	if err != nil {
		t.Fatalf("ParseResource: %v", err)
	}
	return resource
}

func mustScopes(t *testing.T, raw string) oauthserver.ScopeSet {
	t.Helper()
	scopes, err := oauthserver.ParseScopeSet(raw)
	if err != nil {
		t.Fatalf("ParseScopeSet: %v", err)
	}
	return scopes
}

func mustChallenge(t *testing.T) oauthserver.CodeChallenge {
	t.Helper()
	challenge, err := oauthserver.ParseCodeChallenge(string(oauthserver.MethodS256), testChallenge)
	if err != nil {
		t.Fatalf("ParseCodeChallenge: %v", err)
	}
	return challenge
}

// lookupOf derives a distinct digest per label, standing in for the digest of a
// credential the server would have minted. No test needs the preimage.
func lookupOf(label string) oauthserver.Lookup {
	return oauthserver.Lookup(sha256.Sum256([]byte(label)))
}

// consentKey is the exact tuple the fixture's grants are recorded under.
func (f fixture) consentKey() oauthserver.ConsentKey {
	return oauthserver.ConsentKey{
		Principal:   f.principal,
		ClientID:    f.clientID,
		RedirectURI: f.redirect,
		Resource:    f.resource,
	}
}

// seedConsent records the consent every token grant needs.
func (f fixture) seedConsent(t *testing.T) {
	t.Helper()
	err := f.adapter.SaveConsent(context.Background(), oauthserver.Consent{
		Key:       f.consentKey(),
		Scopes:    f.scopes,
		GrantedAt: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("SaveConsent: %v", err)
	}
}

// transaction builds a pending transaction addressed by label's digest.
func (f fixture) transaction(label string, state oauthserver.ClientState) oauthserver.Transaction {
	now := f.clock.Now()
	return oauthserver.Transaction{
		Lookup:      lookupOf(label),
		ClientID:    f.clientID,
		RedirectURI: f.redirect,
		Scopes:      f.scopes,
		Resource:    f.resource,
		Challenge:   f.challenge,
		State:       state,
		Stage:       oauthserver.StagePending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(10 * time.Minute),
	}
}

// code builds an authorization code addressed by label's digest.
func (f fixture) code(label string) oauthserver.AuthorizationCode {
	now := f.clock.Now()
	return oauthserver.AuthorizationCode{
		Lookup:      lookupOf(label),
		ClientID:    f.clientID,
		RedirectURI: f.redirect,
		Scopes:      f.scopes,
		Resource:    f.resource,
		Challenge:   f.challenge,
		Principal:   f.principal,
		IssuedAt:    now,
		ExpiresAt:   now.Add(5 * time.Minute),
	}
}

// pair builds an access and refresh token of one family, at one generation.
func (f fixture) pair(family oauthserver.FamilyID, label string, generation uint64,
) (oauthserver.AccessToken, oauthserver.RefreshToken) {
	now := f.clock.Now()
	access := oauthserver.AccessToken{
		Lookup:    lookupOf(label + "/access"),
		ClientID:  f.clientID,
		Principal: f.principal,
		Scopes:    f.scopes,
		Resource:  f.resource,
		Family:    family,
		IssuedAt:  now,
		ExpiresAt: now.Add(10 * time.Minute),
	}
	refresh := oauthserver.RefreshToken{
		Lookup:     lookupOf(label + "/refresh"),
		ClientID:   f.clientID,
		Principal:  f.principal,
		Scopes:     f.scopes,
		Resource:   f.resource,
		Family:     family,
		Generation: generation,
		IssuedAt:   now,
		ExpiresAt:  now.Add(24 * time.Hour),
	}
	return access, refresh
}

// seedFamily records the consent and the first pair of a family.
func (f fixture) seedFamily(t *testing.T, family oauthserver.FamilyID, label string,
) (oauthserver.AccessToken, oauthserver.RefreshToken) {
	t.Helper()
	f.seedConsent(t)
	access, refresh := f.pair(family, label, 0)
	if err := f.adapter.SaveTokenPair(context.Background(), access, refresh); err != nil {
		t.Fatalf("SaveTokenPair: %v", err)
	}
	return access, refresh
}
