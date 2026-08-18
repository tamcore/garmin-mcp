package cmd_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cmd"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

const (
	cmdUnlink = "unlink"
	cmdRevoke = "revoke"
)

// revocationTestRedirectURI and revocationTestAudience are the fixed client shape
// every seeded principal in this file grants consent under.
const (
	revocationTestRedirectURI = "https://example.test/callback"
	revocationTestAudience    = "https://example.test/mcp"
)

// revocationStateDir returns a state directory inside a symlink-free ancestry,
// matching every other filesystem-touching command test in this package.
func revocationStateDir(t *testing.T) string {
	t.Helper()
	return rotateStateDir(t)
}

// openRevocationSeedStore opens the SQLite store at the same database and key
// locations the unlink and revoke commands themselves resolve to, so a test can
// seed state the command later reads, and read back state the command wrote.
func openRevocationSeedStore(t *testing.T, stateDir string) (*store.SQLiteStore, string) {
	t.Helper()

	keysDir := filepath.Join(stateDir, "keys")
	key, err := cryptostore.LoadOrCreateKey(keysDir, 1)
	if err != nil {
		t.Fatalf("creating the test key: %v", err)
	}

	dbPath := filepath.Join(stateDir, "state.db")
	opened, err := store.OpenSQLite(context.Background(), store.SQLiteConfig{
		Path: dbPath,
		Key:  key,
	})
	if err != nil {
		t.Fatalf("opening the seed store: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened, dbPath
}

// linkedTestPrincipal is what seedLinkedPrincipal builds: the principal plus
// the one OAuth client it granted consent to and issued a token family under,
// so a test can look both up again after a revocation.
type linkedTestPrincipal struct {
	store.Principal
	clientID string
}

// seedLinkedPrincipal creates a principal with a registered client, a granted
// consent, one issued token family, a Garmin token record and a Garmin account
// link — everything both UnlinkGarminAccount and RevokePrincipalTokens cascade
// over.
func seedLinkedPrincipal(t *testing.T, s *store.SQLiteStore, label string) linkedTestPrincipal {
	t.Helper()
	ctx := context.Background()

	principal, err := s.CreatePrincipal(ctx, label+"@example.test")
	if err != nil {
		t.Fatalf("CreatePrincipal(%s): %v", label, err)
	}

	client, err := s.RegisterClient(ctx, store.ClientRegistration{
		Name:         label + " client",
		RedirectURIs: []string{revocationTestRedirectURI},
	})
	if err != nil {
		t.Fatalf("RegisterClient(%s): %v", label, err)
	}

	if err := s.GrantConsent(ctx, principal.ID, client.ID, []string{"garmin:read"}); err != nil {
		t.Fatalf("GrantConsent(%s): %v", label, err)
	}

	if _, err := s.IssueTokenFamily(ctx, store.TokenGrant{
		PrincipalID:     principal.ID,
		ClientID:        client.ID,
		Scopes:          []string{"garmin:read"},
		Audience:        revocationTestAudience,
		AccessToken:     store.NewSecret(label + "-access"),
		RefreshToken:    store.NewSecret(label + "-refresh"),
		AccessLifetime:  10 * time.Minute,
		RefreshLifetime: 24 * time.Hour,
	}); err != nil {
		t.Fatalf("IssueTokenFamily(%s): %v", label, err)
	}

	if _, err := s.Save(ctx, principal.ID,
		store.NewTokenSet(label+"-garmin-token", label+"-garmin-refresh", "garmin-di", time.Now().Add(time.Hour)),
		0); err != nil {
		t.Fatalf("Save Garmin token record(%s): %v", label, err)
	}

	if err := s.LinkGarminAccount(ctx, principal.ID, store.GarminIdentity{
		AccountID:   store.NewSecret(label + "-garmin-account"),
		DisplayName: label + " display name",
	}); err != nil {
		t.Fatalf("LinkGarminAccount(%s): %v", label, err)
	}

	return linkedTestPrincipal{Principal: principal, clientID: client.ID}
}

// TestUnlinkRemovesTheGarminLinkageAndLeavesOtherPrincipalsAlone is the property
// that matters most: unlinking one principal must not touch another's state.
func TestUnlinkRemovesTheGarminLinkageAndLeavesOtherPrincipalsAlone(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)
	seed, dbPath := openRevocationSeedStore(t, stateDir)

	target := seedLinkedPrincipal(t, seed, "target")
	other := seedLinkedPrincipal(t, seed, "other")
	if err := seed.Close(); err != nil {
		t.Fatalf("closing the seed store: %v", err)
	}

	stdout, err := runCommand(t, cmdUnlink,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--principal="+target.ID)
	if err != nil {
		t.Fatalf("unlink = %v, want success", err)
	}
	if !strings.Contains(stdout, target.ID) {
		t.Errorf("stdout = %q, want it to name the principal", stdout)
	}

	verify, _ := openRevocationSeedStore(t, stateDir)
	ctx := context.Background()

	if _, _, err := verify.Load(ctx, target.ID); !errors.Is(err, store.ErrNoTokens) {
		t.Errorf("target's Garmin token record: err = %v, want ErrNoTokens", err)
	}
	if _, err := verify.GarminIdentity(ctx, target.ID); err == nil {
		t.Error("target's Garmin identity is still readable after unlink")
	}

	// Tenant isolation: the other principal's linkage and tokens must survive.
	if _, _, err := verify.Load(ctx, other.ID); err != nil {
		t.Errorf("other principal's Garmin token record was removed too: %v", err)
	}
	if _, err := verify.GarminIdentity(ctx, other.ID); err != nil {
		t.Errorf("other principal's Garmin identity was removed too: %v", err)
	}
}

// TestUnlinkIsIdempotent fixes running the command twice.
func TestUnlinkIsIdempotent(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)
	seed, dbPath := openRevocationSeedStore(t, stateDir)
	target := seedLinkedPrincipal(t, seed, "target")
	if err := seed.Close(); err != nil {
		t.Fatalf("closing the seed store: %v", err)
	}

	if _, err := runCommand(t, cmdUnlink,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--principal="+target.ID); err != nil {
		t.Fatalf("first unlink = %v, want success", err)
	}
	if _, err := runCommand(t, cmdUnlink,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--principal="+target.ID); err != nil {
		t.Fatalf("second unlink = %v, want success (idempotent)", err)
	}
}

// TestUnlinkReportsAnUnknownPrincipalDistinctly is the property that makes the
// command safe to script: a typo must not look like a no-op success.
func TestUnlinkReportsAnUnknownPrincipalDistinctly(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)
	_, dbPath := openRevocationSeedStore(t, stateDir)

	_, err := runCommand(t, cmdUnlink,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--principal=00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("unlink of an unknown principal succeeded, want a refusal")
	}
	if !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("err = %v, want it to wrap ErrPrincipalNotFound", err)
	}
}

// TestUnlinkRequiresThePrincipalFlag documents that there is no "all" and no
// default.
func TestUnlinkRequiresThePrincipalFlag(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)
	_, dbPath := openRevocationSeedStore(t, stateDir)

	if _, err := runCommand(t, cmdUnlink, "--state-dir="+stateDir, "--database-path="+dbPath); err == nil {
		t.Fatal("unlink without --principal succeeded, want a refusal")
	}
}

// TestUnlinkRequiresADatabasePath is the same configuration-error rule migrate
// already follows: no guessed location. rotate-key is not the same rule — it
// supports the FileStore backend with no --database-path at all.
func TestUnlinkRequiresADatabasePath(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)

	_, err := runCommand(t, cmdUnlink, "--state-dir="+stateDir, "--principal=some-principal")
	if !errors.Is(err, cmd.ErrNoDatabasePath) {
		t.Errorf("err = %v, want ErrNoDatabasePath", err)
	}
}

// TestUnlinkOutputNeverNamesTheGarminAccount is the leak check: no email, no
// Garmin account identifier, no token.
func TestUnlinkOutputNeverNamesTheGarminAccount(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)
	seed, dbPath := openRevocationSeedStore(t, stateDir)
	target := seedLinkedPrincipal(t, seed, "target")
	if err := seed.Close(); err != nil {
		t.Fatalf("closing the seed store: %v", err)
	}

	stdout, err := runCommand(t, cmdUnlink,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--principal="+target.ID)
	if err != nil {
		t.Fatalf("unlink = %v, want success", err)
	}
	for _, leaked := range []string{
		"target@example.test", "target-garmin-account", "target-garmin-token",
		"target-garmin-refresh", "target-access", "target-refresh",
	} {
		if strings.Contains(stdout, leaked) {
			t.Errorf("stdout = %q, must not contain %q", stdout, leaked)
		}
	}
}

// TestRevokeKillsTokensAndConsentsButLeavesTheGarminLinkIntact is revoke's whole
// point: an operator revoking a compromised client grant must not force the
// account to be re-linked.
func TestRevokeKillsTokensAndConsentsButLeavesTheGarminLinkIntact(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)
	seed, dbPath := openRevocationSeedStore(t, stateDir)
	target := seedLinkedPrincipal(t, seed, "target")
	other := seedLinkedPrincipal(t, seed, "other")
	if err := seed.Close(); err != nil {
		t.Fatalf("closing the seed store: %v", err)
	}

	if _, err := runCommand(t, cmdRevoke,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--principal="+target.ID); err != nil {
		t.Fatalf("revoke = %v, want success", err)
	}

	verify, _ := openRevocationSeedStore(t, stateDir)
	ctx := context.Background()

	if _, _, err := verify.Load(ctx, target.ID); err != nil {
		t.Errorf("revoke removed the Garmin token record: %v", err)
	}
	if _, err := verify.GarminIdentity(ctx, target.ID); err != nil {
		t.Errorf("revoke removed the Garmin identity: %v", err)
	}

	// The property revoke exists for: the target's OAuth token is actually
	// revoked and its consent is actually gone. A mutant that skips
	// RevokePrincipalTokens entirely leaves both intact and must fail here.
	if _, err := verify.LookupAccessToken(ctx, store.NewSecret("target-access")); !errors.Is(err, store.ErrTokenRevoked) {
		t.Errorf("target's access token: err = %v, want ErrTokenRevoked", err)
	}
	if _, err := verify.Consent(ctx, target.ID, target.clientID); !errors.Is(err, store.ErrConsentNotFound) {
		t.Errorf("target's consent: err = %v, want ErrConsentNotFound", err)
	}

	// The other principal's tokens and consent must survive.
	if _, _, err := verify.Load(ctx, other.ID); err != nil {
		t.Errorf("other principal's Garmin token record was removed too: %v", err)
	}
	if _, err := verify.LookupAccessToken(ctx, store.NewSecret("other-access")); err != nil {
		t.Errorf("bystander's access token was revoked too: %v", err)
	}
	if _, err := verify.Consent(ctx, other.ID, other.clientID); err != nil {
		t.Errorf("bystander's consent was revoked too: %v", err)
	}
}

// TestRevokeIsIdempotent fixes running the command twice.
func TestRevokeIsIdempotent(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)
	seed, dbPath := openRevocationSeedStore(t, stateDir)
	target := seedLinkedPrincipal(t, seed, "target")
	if err := seed.Close(); err != nil {
		t.Fatalf("closing the seed store: %v", err)
	}

	if _, err := runCommand(t, cmdRevoke,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--principal="+target.ID); err != nil {
		t.Fatalf("first revoke = %v, want success", err)
	}
	if _, err := runCommand(t, cmdRevoke,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--principal="+target.ID); err != nil {
		t.Fatalf("second revoke = %v, want success (idempotent)", err)
	}
}

// TestRevokeReportsAnUnknownPrincipalDistinctly matters exactly as much for
// revoke as it does for unlink: RevokePrincipalTokens itself reports a zero
// result rather than an error for an unknown principal, so the command must add
// the distinguishing check itself.
func TestRevokeReportsAnUnknownPrincipalDistinctly(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)
	_, dbPath := openRevocationSeedStore(t, stateDir)

	_, err := runCommand(t, cmdRevoke,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--principal=00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("revoke of an unknown principal succeeded, want a refusal")
	}
	if !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("err = %v, want it to wrap ErrPrincipalNotFound", err)
	}
}

// TestRevokeRequiresThePrincipalFlag documents that there is no "all" and no
// default.
func TestRevokeRequiresThePrincipalFlag(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)
	_, dbPath := openRevocationSeedStore(t, stateDir)

	if _, err := runCommand(t, cmdRevoke, "--state-dir="+stateDir, "--database-path="+dbPath); err == nil {
		t.Fatal("revoke without --principal succeeded, want a refusal")
	}
}

// TestRevokeRequiresADatabasePath matches migrate's rule, not rotate-key's:
// rotate-key supports the FileStore backend with no --database-path at all.
func TestRevokeRequiresADatabasePath(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)

	_, err := runCommand(t, cmdRevoke, "--state-dir="+stateDir, "--principal=some-principal")
	if !errors.Is(err, cmd.ErrNoDatabasePath) {
		t.Errorf("err = %v, want ErrNoDatabasePath", err)
	}
}

// TestRevokeOutputNeverNamesTheGarminAccount is the leak check for revoke.
func TestRevokeOutputNeverNamesTheGarminAccount(t *testing.T) {
	clearGarminEnv(t)
	stateDir := revocationStateDir(t)
	seed, dbPath := openRevocationSeedStore(t, stateDir)
	target := seedLinkedPrincipal(t, seed, "target")
	if err := seed.Close(); err != nil {
		t.Fatalf("closing the seed store: %v", err)
	}

	stdout, err := runCommand(t, cmdRevoke,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--principal="+target.ID)
	if err != nil {
		t.Fatalf("revoke = %v, want success", err)
	}
	for _, leaked := range []string{
		"target@example.test", "target-garmin-account", "target-garmin-token",
		"target-garmin-refresh", "target-access", "target-refresh",
	} {
		if strings.Contains(stdout, leaked) {
			t.Errorf("stdout = %q, must not contain %q", stdout, leaked)
		}
	}
}
