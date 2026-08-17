//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// This file seeds rows directly into the SQLite database a remote deployment
// under test will open, using the same store package the binary links.
//
// It exists for one reason: completing an OAuth authorization requires a
// completed Garmin login, and this package must never let a subprocess reach
// the real Garmin service. Seeding a principal and an authorization code
// reproduces what a completed login and a granted consent would have left
// behind, so the tests that follow can drive /token, revocation and the MCP
// endpoint over real HTTP against the real binary — the parts of the flow that
// do not require Garmin at all.
//
// Redirecting the compiled binary's own Garmin traffic instead of seeding is
// not impossible. internal/cmd/components.go builds the login transport with
// http.ProxyFromEnvironment and no custom RootCAs, and on Linux crypto/x509
// honours SSL_CERT_FILE, so an HTTPS_PROXY pointed at a TLS-terminating MITM
// proxy in front of a fake Garmin, plus SSL_CERT_FILE naming that proxy's own
// CA, is a working seam there. It is not used here because it is expensive — it
// needs a CONNECT-capable proxy issuing a certificate per host on the fly — and
// it is silently unavailable on macOS, where crypto/x509's root verifier does
// not consult SSL_CERT_FILE. Seeding was chosen for that cost and platform
// asymmetry, not because the alternative cannot work at all.
//
// Every caller in this package opens the database, seeds everything a test
// will need — including the OAuth client row seedClient installs, and every
// authorization code a sub-test or a concurrent redemption will present — and
// closes it again before the server process starts. Two writers on one SQLite
// file was tried and measured: with the test process's connection and the
// running server's connection both open, a concurrent /token redemption
// produced a genuine SQLITE_IOERR_SHORT_READ (522) at the filesystem layer on
// the server's own read path, and this package's connection could not observe
// rows the server had itself written (LookupAccessToken reported
// ErrTokenNotFound for a token that demonstrably worked through the server).
// WAL mode and a busy_timeout do not prevent that: they serialize writers
// against each other, but a reader on one connection still is not guaranteed
// to see a writer's commit on another promptly, and this harness's own
// symptoms were reads, not lock contention. Single-writer — seed fully, then
// launch, then never write again — is the fix; see docs/implementation-status.md
// for what was measured.

// seedDatabasePath is where startRemoteServerSeeded's config points the binary,
// and where this file must open the same store.
func seedDatabasePath(dir string) string {
	return filepath.Join(dir, "garmin.db")
}

// openSeedStore opens the same database and key the binary will, so this file
// writes through the identical encryption and schema the running process reads.
func openSeedStore(t *testing.T, dir string) *store.SQLiteStore {
	t.Helper()

	// LoadKey, not LoadOrCreateKey: writeMasterKey (e2e/remote_test.go) always
	// writes the key file before this is called, so a missing file here is a
	// setup defect, not a case to mint quietly around. LoadOrCreateKey would
	// paper over that: a run where writeMasterKey stopped writing a key would
	// silently mint a second one, and this file would then encrypt under a
	// different key than the running process reads.
	key, err := cryptostore.LoadKey(dir, 1)
	if err != nil {
		t.Fatalf("load the seeded master key: %v", err)
	}
	sqlite, err := store.OpenSQLite(context.Background(), store.SQLiteConfig{
		Path: seedDatabasePath(dir),
		Key:  key,
	})
	if err != nil {
		t.Fatalf("open the seeded database: %v", err)
	}
	return sqlite
}

// remoteClientName is the display name writeRemoteConfig registers for
// remoteClientID. seedClient must install the identical value: the server's
// own start-up reconciliation (internal/cmd/remoteclients.go,
// reconcileConfiguredClients) turns that exact configuration entry into an
// identical store.ClientReconciliation, and a mismatch here would only be
// silently overwritten at start-up rather than caught.
const remoteClientName = "End-to-end client"

// seedClient installs the row the server's own start-up reconciliation would
// install for remoteClientID, so an authorization code naming that client can
// be seeded before the server process ever starts.
//
// This is the piece that used to force codes to be seeded after launch: the
// FK auth_codes.client_id -> oauth_clients.id (migrations/0001_initial.sql)
// only had a target once the running process reconciled its configured
// client into the database. store.SQLiteStore.ReconcileClient
// (internal/store/sqlite_clientreconcile.go) is keyed on the caller-chosen
// ID rather than minting one the way RegisterClient does, and it is
// idempotent: the server's own reconciliation at start-up finds this row
// already present, applies the identical name, public flag and redirect URI
// list, and never changes oauth_clients.id itself. The FK a pre-launch code
// depends on therefore survives start-up untouched.
func seedClient(t *testing.T, sqlite *store.SQLiteStore) {
	t.Helper()

	_, err := sqlite.ReconcileClient(context.Background(), store.ClientReconciliation{
		ID:           remoteClientID,
		Name:         remoteClientName,
		RedirectURIs: []string{remoteRedirectURI},
		IsPublic:     true,
	})
	if err != nil {
		t.Fatalf("seed the e2e OAuth client: %v", err)
	}
}

// seedPrincipal mints a principal directly, standing in for what a completed
// Garmin login would have resolved or created. It is deliberately not linked to
// any Garmin account: nothing in this package ever calls Garmin.
func seedPrincipal(t *testing.T, sqlite *store.SQLiteStore, email string) string {
	t.Helper()

	principal, err := sqlite.CreatePrincipal(context.Background(), email)
	if err != nil {
		t.Fatalf("seed principal %s: %v", email, err)
	}
	return principal.ID
}

// pkcePair returns a fresh PKCE verifier and its S256 challenge, both already in
// the exact shape the authorization server requires: 43 characters of unpadded
// base64url.
func pkcePair(t *testing.T) (verifier, challenge string) {
	t.Helper()

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate a PKCE verifier: %v", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge
}

// seedConsent grants the consent SaveTokenPair requires before it will write a
// token family, standing in for the consent a browser's Allow decision would
// have recorded. The tuple must match the authorization code seeded alongside
// it exactly: principal, client, redirect URI and resource are the whole key.
func seedConsent(t *testing.T, sqlite *store.SQLiteStore, p seedAuthCodeParams) {
	t.Helper()

	err := sqlite.GrantConsentFor(context.Background(), store.ConsentKey{
		PrincipalID: p.principalID,
		ClientID:    p.clientID,
		RedirectURI: p.redirectURI,
		Resource:    p.resource,
	}, p.scopes)
	if err != nil {
		t.Fatalf("seed consent for principal %s: %v", p.principalID, err)
	}
}

// seedAuthCodeParams is the binding a seeded authorization code carries. It
// mirrors what BeginAuthorization plus a granted consent would have persisted.
type seedAuthCodeParams struct {
	principalID string
	clientID    string
	redirectURI string
	resource    string
	scopes      []string
	challenge   string
}

// storeLookupSecret converts a raw bearer token into the store.Secret handle
// the running server's own oauthstore adapter presents to
// SQLiteStore.LookupAccessToken. oauthstore.material (internal/oauthstore/convert.go)
// wraps oauthserver.Lookup.Hex() as a store.Secret before the value ever
// reaches the store, so a caller reading a minted token back must apply the
// identical conversion — the same one seedAuthCode already applies to a
// seeded authorization code — or it looks up a value the store never indexed
// under.
func storeLookupSecret(token string) store.Secret {
	return store.NewSecret(oauthserver.SecretFromString(token).Lookup().Hex())
}

// seedAuthCode installs a single-use authorization code and returns the raw
// value a client presents at /token. The code is hashed on the way in exactly
// as the running authorization server hashes a presented code on the way out,
// so the two sides agree without either one ever seeing the other's copy.
func seedAuthCode(t *testing.T, sqlite *store.SQLiteStore, p seedAuthCodeParams) string {
	t.Helper()

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate an authorization code: %v", err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	// The digest is computed through the production helper, not hand-rolled:
	// oauthstore.material does exactly this conversion (oauthserver.Lookup.Hex()
	// wrapped as a store.Secret) before calling PutAuthCode for a real grant, so
	// this seed agrees with the server's own digest scheme even if that scheme
	// ever changes.
	handle := store.NewSecret(oauthserver.SecretFromString(code).Lookup().Hex())

	now := time.Now()
	err := sqlite.PutAuthCode(context.Background(), store.AuthCodeDraft{
		Code:          handle,
		PrincipalID:   p.principalID,
		ClientID:      p.clientID,
		RedirectURI:   p.redirectURI,
		Audience:      p.resource,
		Scopes:        p.scopes,
		CodeChallenge: p.challenge,
		IssuedAt:      now,
		ExpiresAt:     now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("seed authorization code: %v", err)
	}
	return code
}
