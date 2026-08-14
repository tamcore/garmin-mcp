package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Shared fixtures for the SQLite tests. Every value here is synthetic: no test
// touches a real Garmin account, a real client secret, or a real token.
const (
	testEmail           = "Rider@Example.COM"
	testEmailNormalized = "rider@example.com"
	testGarminAccount   = "garmin-account-0000-0000"
	testClientName      = "Test MCP Client"
	testRedirectURI     = "https://client.example/callback"
	testAudience        = "https://mcp.example/"
	testScope           = "garmin.read"
	testDisplayName     = "Test Rider"

	// testUnknownID is a well-formed UUID that is never inserted, so every lookup for
	// it must report the not-found sentinel rather than a validation error.
	testUnknownID = "00000000-0000-4000-8000-000000000000"

	// journalModeWAL is what a correctly configured connection reports.
	journalModeWAL = "wal"
)

// Names shared by the table-driven validation cases, so one spelling drift cannot make a
// case silently stop being exercised.
const (
	caseNoScopes     = "no scopes"
	caseZeroLifetime = "a zero lifetime"
	casePathIgnored  = "ignored.db"
	migrationFileOne = "0001_a.sql"
	migrationBodyOne = `CREATE TABLE a (id INTEGER PRIMARY KEY) STRICT;`
)

// testDBPath is the file a test database lives in. WAL mode needs a real file: an
// in-memory database cannot report journal_mode=wal, and the pragma assertions must
// observe what a deployment actually gets.
//
// The temporary directory is resolved through EvalSymlinks first, because the store
// refuses a symlinked ancestor and on macOS t.TempDir() sits under /var, which is a
// symlink to /private/var. The existing FileStore tests resolve it the same way.
func testDBPath(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return filepath.Join(resolved, "garmin-mcp.db")
}

// newTestDB opens a bare database with the shipped connection settings and no
// schema. The migrator tests need exactly that.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.OpenDatabase(testDBPath(t), store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return db
}

// testKey builds a throwaway encryption key. Version 1 matches the default the
// store records in schema_meta.
func testKey(t *testing.T) cryptostore.Key {
	t.Helper()
	key, err := cryptostore.GenerateKey(1)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return key
}

// fakeClock is a manually advanced clock, so expiry is tested by moving time
// rather than by sleeping. It is used single-threaded except where a test says
// otherwise.
type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// newTestStore opens a migrated SQLite store on a fresh database file, with the
// fake clock wired in, and returns both.
func newTestStore(t *testing.T) (*store.SQLiteStore, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	opened, err := store.OpenSQLite(context.Background(), store.SQLiteConfig{
		Path: testDBPath(t),
		Key:  testKey(t),
		Now:  clock.Now,
	})
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() {
		if err := opened.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return opened, clock
}

// Synthetic Garmin DI token material. The token is JWT-shaped so the document
// decoder's unverified-exp path is exercised, and the payload carries no real claim.
const (
	sqlTestToken        = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.c2lnbmF0dXJl"
	sqlTestRefreshToken = "synthetic-di-refresh-token"
	sqlTestClientID     = "synthetic-di-client"
)

// newSQLTestTokens builds a Garmin DI token set for the SQLite tests.
func newSQLTestTokens() store.TokenSet {
	return store.NewTokenSet(sqlTestToken, sqlTestRefreshToken, sqlTestClientID, time.Time{})
}

// seedPrincipal creates a principal with the shared test email.
func seedPrincipal(t *testing.T, s *store.SQLiteStore) store.Principal {
	t.Helper()
	principal, err := s.CreatePrincipal(context.Background(), testEmail)
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	return principal
}

// seedClient registers a confidential client with one exact redirect URI.
func seedClient(t *testing.T, s *store.SQLiteStore) store.Client {
	t.Helper()
	client, err := s.RegisterClient(context.Background(), store.ClientRegistration{
		Name:         testClientName,
		RedirectURIs: []string{testRedirectURI},
		Secret:       store.NewSecret("test-client-secret"),
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	return client
}

// seededGrant is the state most token tests start from: a principal, a client, a
// consent, and one token family holding an access and a refresh token.
type seededGrant struct {
	principal store.Principal
	client    store.Client
	familyID  string
	access    store.Secret
	refresh   store.Secret
}

func seedGrant(t *testing.T, s *store.SQLiteStore) seededGrant {
	t.Helper()
	ctx := context.Background()
	principal := seedPrincipal(t, s)
	client := seedClient(t, s)

	if err := s.GrantConsent(ctx, principal.ID, client.ID, []string{testScope}); err != nil {
		t.Fatalf("GrantConsent: %v", err)
	}

	grant := seededGrant{
		principal: principal,
		client:    client,
		access:    store.NewSecret("access-token-" + principal.ID),
		refresh:   store.NewSecret("refresh-token-" + principal.ID),
	}
	familyID, err := s.IssueTokenFamily(ctx, store.TokenGrant{
		PrincipalID:     principal.ID,
		ClientID:        client.ID,
		Scopes:          []string{testScope},
		Audience:        testAudience,
		AccessToken:     grant.access,
		RefreshToken:    grant.refresh,
		AccessLifetime:  10 * time.Minute,
		RefreshLifetime: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("IssueTokenFamily: %v", err)
	}
	grant.familyID = familyID
	return grant
}
