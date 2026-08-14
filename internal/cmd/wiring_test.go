package cmd

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/identity"
)

// emailPrincipal is the personal-data value a principal identifier must never be.
const emailPrincipal = "person@example.test"

// localConfig returns a validated stdio configuration rooted at a private,
// symlink-free state directory. Every path component must be real, because the
// secure file layer refuses a symlinked ancestor and macOS puts the temporary
// directory under /var, which is a symlink.
func localConfig(t *testing.T) config.Config {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}

	cfg := config.Default()
	cfg.StateDir = dir
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the test configuration does not validate: %v", err)
	}
	return cfg
}

func buildDependencies(t *testing.T, cfg config.Config) *dependencies {
	t.Helper()

	deps, err := newDependencies(cfg, nil)
	if err != nil {
		t.Fatalf("newDependencies returned error: %v", err)
	}
	t.Cleanup(deps.close)
	return deps
}

// TestServeSharesOneTokenGateBetweenLoginAndRefresh is the wiring guarantee the
// gate exists for. Login and refresh both end in a compare-and-set write of one
// principal's DI token set. Each config falls back to a private gate when its
// field is nil, so two gates compile, pass their own tests, and silently restore
// the rotated-token overwrite: login then serializes only against login, and
// refresh only against refresh.
//
// The assertion is on the wiring itself — the two configuration values the
// composition root built — so a refactor that splits the gate fails here rather
// than in production.
func TestServeSharesOneTokenGateBetweenLoginAndRefresh(t *testing.T) {
	deps := buildDependencies(t, localConfig(t))

	login := deps.tokenConfigs.login.TokenGate
	refresh := deps.tokenConfigs.refresh.TokenGate

	if login == nil {
		t.Fatal("auth.Config.TokenGate is nil: the Authenticator built a private gate")
	}
	if refresh == nil {
		t.Fatal("auth.RefreshConfig.TokenGate is nil: the Refresher built a private gate")
	}
	if login != refresh {
		t.Fatal("the Authenticator and the Refresher hold different token gates, " +
			"so a login can overwrite a token set a concurrent refresh just rotated")
	}
	if deps.tokenGate != login {
		t.Error("the shared gate the composition root recorded is not the one it passed on")
	}
}

// TestDependenciesShareOneStoreBetweenLoginAndRefresh keeps the gate meaningful:
// serializing two components that write different stores would protect nothing.
func TestDependenciesShareOneStoreBetweenLoginAndRefresh(t *testing.T) {
	deps := buildDependencies(t, localConfig(t))

	if deps.tokenConfigs.login.Store == nil {
		t.Fatal("no token store was wired")
	}
	if deps.tokenConfigs.login.Store != deps.tokenConfigs.refresh.Store {
		t.Error("login and refresh write different token stores")
	}
}

// TestDependenciesBindExactlyOnePrincipal covers the local single-user rule: the
// resolver answers the configured principal, and nothing a request carries can
// change the answer.
func TestDependenciesBindExactlyOnePrincipal(t *testing.T) {
	cfg := localConfig(t)
	cfg.PrincipalID = "principal-a"
	deps := buildDependencies(t, cfg)

	resolved, err := deps.principals.Resolve(t.Context())
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.ID() != "principal-a" {
		t.Errorf("principal = %q, want %q", resolved.ID(), "principal-a")
	}
}

// TestDependenciesRejectAnEmailPrincipal keeps personal data out of the isolation
// key even if configuration validation is ever loosened.
func TestDependenciesRejectAnEmailPrincipal(t *testing.T) {
	cfg := localConfig(t)
	cfg.PrincipalID = emailPrincipal

	_, err := newDependencies(cfg, nil)
	if err == nil {
		t.Fatal("newDependencies accepted an email address as a principal")
	}
	if !errors.Is(err, identity.ErrEmailNotAPrincipal) {
		t.Errorf("error %v does not match identity.ErrEmailNotAPrincipal", err)
	}
}

// TestDependenciesRefuseInlineMasterKeyMaterial fails closed on key material this
// build cannot honor, and says so without echoing the material.
func TestDependenciesRefuseInlineMasterKeyMaterial(t *testing.T) {
	cfg := localConfig(t)
	cfg.MasterKey = config.NewSecret("c2VjcmV0LW1hdGVyaWFs")

	_, err := newDependencies(cfg, nil)
	if err == nil {
		t.Fatal("newDependencies accepted inline master key material")
	}
	if !errors.Is(err, ErrUnsupportedKeyMaterial) {
		t.Errorf("error %v does not match ErrUnsupportedKeyMaterial", err)
	}
	if strings.Contains(err.Error(), "c2VjcmV0LW1hdGVyaWFs") {
		t.Error("the error echoes the supplied key material")
	}
}

// TestWiredLoggerHasASinkAndItIsNotStdout keeps the stdio frame stream
// inviolable. mcplog refuses os.Stdout outright; this checks the composition root
// gave it a sink at all.
func TestWiredLoggerHasASinkAndItIsNotStdout(t *testing.T) {
	deps := buildDependencies(t, localConfig(t))

	if deps.logger == nil {
		t.Fatal("no logger was wired")
	}
	if deps.logger.Sink() == nil {
		t.Error("the logger has no sink")
	}
}
