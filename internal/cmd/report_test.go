package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// TestDiagnoseClassifiesTheTokenStore covers the states an operator can actually be
// in: nothing set up yet, a directory another local account can read, and a path that
// is not a directory at all.
func TestDiagnoseClassifiesTheTokenStore(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, dir string)
		want    state
		detail  string
	}{
		{
			name:    "absent",
			prepare: func(*testing.T, string) {},
			want:    stateAbsent,
			detail:  "absent",
		},
		{
			name: "group readable",
			prepare: func(t *testing.T, dir string) {
				if err := os.MkdirAll(filepath.Join(dir, tokensDirName), 0o750); err != nil {
					t.Fatalf("create the token directory: %v", err)
				}
			},
			want:   stateUnsafe,
			detail: "owner-only",
		},
		{
			name: "not a directory",
			prepare: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, tokensDirName),
					[]byte("not a directory"), 0o600); err != nil {
					t.Fatalf("write the token path: %v", err)
				}
			},
			want:   stateUnsafe,
			detail: "not a directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := localConfig(t)
			tc.prepare(t, cfg.StateDir)

			report, err := diagnose(t.Context(), cfg)
			if err != nil {
				t.Fatalf("diagnose returned error: %v", err)
			}
			if report.StoreState != tc.want {
				t.Errorf("StoreState = %q, want %q (detail %q)",
					report.StoreState, tc.want, report.StoreDetail)
			}
			if !strings.Contains(report.StoreDetail, tc.detail) {
				t.Errorf("StoreDetail = %q, want it to mention %q", report.StoreDetail, tc.detail)
			}
			if report.failed() != (tc.want == stateUnsafe) {
				t.Errorf("failed() = %v for state %q", report.failed(), tc.want)
			}
		})
	}
}

// TestDiagnoseReportsAnUnusablePrincipalWithoutOpeningTheStore keeps the report
// coherent when the binding itself is wrong.
func TestDiagnoseReportsAnUnusablePrincipalWithoutOpeningTheStore(t *testing.T) {
	cfg := localConfig(t)
	cfg.PrincipalID = emailPrincipal

	report, err := diagnose(t.Context(), cfg)
	if err != nil {
		t.Fatalf("diagnose returned error: %v", err)
	}
	if report.PrincipalBound {
		t.Error("an email address was reported as a bound principal")
	}
	if !strings.Contains(report.render(), "not a usable principal identifier") {
		t.Errorf("the report does not say the principal is unusable:\n%s", report.render())
	}
}

// TestConfiguredMasterKeyFileSelectsTheKeyDirectory pins the documented behavior: the
// setting chooses the location, and the versioned file name inside it belongs to the
// key store.
func TestConfiguredMasterKeyFileSelectsTheKeyDirectory(t *testing.T) {
	cfg := localConfig(t)
	cfg.MasterKeyPath = filepath.Join(cfg.StateDir, "secrets", "master.key")

	paths, err := resolveStatePaths(cfg)
	if err != nil {
		t.Fatalf("resolveStatePaths returned error: %v", err)
	}
	if want := filepath.Join(cfg.StateDir, "secrets"); paths.keys != want {
		t.Errorf("keys = %q, want %q", paths.keys, want)
	}
	if !strings.HasSuffix(paths.keyFile(1), "key-v1.json") {
		t.Errorf("keyFile = %q, want the versioned key file", paths.keyFile(1))
	}
	if paths.tokens != filepath.Join(cfg.StateDir, tokensDirName) {
		t.Errorf("tokens = %q, want it under the state directory", paths.tokens)
	}
}

// TestDefaultStateDirectoryIsPerUser keeps a local deployment out of the working
// directory when the operator configured no location.
func TestDefaultStateDirectoryIsPerUser(t *testing.T) {
	paths, err := resolveStatePaths(config.Default())
	if err != nil {
		t.Fatalf("resolveStatePaths returned error: %v", err)
	}
	if !strings.HasSuffix(paths.root, stateDirName) {
		t.Errorf("root = %q, want it to end in %q", paths.root, stateDirName)
	}
	if !filepath.IsAbs(paths.root) {
		t.Errorf("root = %q, want an absolute path", paths.root)
	}
}

// TestInlineTokenDocumentIsReadOnlyThroughTheOverride keeps the insecure
// compatibility path explicit: the document is read only because the operator set the
// inline setting, which is what turns the store's override on.
func TestInlineTokenDocumentIsReadOnlyThroughTheOverride(t *testing.T) {
	cfg := localConfig(t)
	cfg.GarminTokens = config.NewSecret(`{"di_token":"synthetic-di-token",` +
		`"di_refresh_token":"synthetic-refresh","di_client_id":"synthetic-client"}`)

	deps, err := newDependencies(cfg, nil)
	if err != nil {
		t.Fatalf("newDependencies returned error: %v", err)
	}
	t.Cleanup(deps.close)

	if !deps.files.AllowsInlineTokens() {
		t.Error("the inline override is off, so the document would be refused")
	}
	if err := deps.importConfiguredTokens(t.Context()); err != nil {
		t.Fatalf("importConfiguredTokens returned error: %v", err)
	}
	stored, _, err := deps.files.Load(t.Context(), deps.principal.ID())
	if err != nil {
		t.Fatalf("the inline token set was not imported: %v", err)
	}
	if stored.RefreshToken() != "synthetic-refresh" {
		t.Error("the imported refresh token is not the one in the document")
	}
}

// TestReportOutcomeSeparatesALinkedAccountFromEverythingElse keeps the exit status
// honest: only a linked account is a success.
func TestReportOutcomeSeparatesALinkedAccountFromEverythingElse(t *testing.T) {
	var linked strings.Builder
	err := reportOutcome(&linked, loginweb.Outcome{State: auth.StateAuthenticated})
	if err != nil {
		t.Errorf("a linked account reported %v", err)
	}
	if !strings.Contains(linked.String(), "linked") {
		t.Errorf("the success line is missing: %q", linked.String())
	}

	var unfinished strings.Builder
	err = reportOutcome(&unfinished, loginweb.Outcome{})
	if !errors.Is(err, ErrLoginNotCompleted) {
		t.Errorf("error %v does not match ErrLoginNotCompleted", err)
	}
	if unfinished.Len() != 0 {
		t.Errorf("an unfinished run printed %q as a result", unfinished.String())
	}
}
