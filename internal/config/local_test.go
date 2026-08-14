package config

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// clearLocalStateEnv removes every GARMIN_MCP_ variable for the duration of the
// test, so a developer's or a CI runner's environment cannot change what Load
// resolves. An empty value counts as unset.
func clearLocalStateEnv(t *testing.T) {
	t.Helper()

	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.HasPrefix(name, envPrefix) {
			t.Setenv(name, "")
		}
	}
}

// TestLocalStateSettingsResolveThroughEveryLayer covers the two settings the local
// stdio deployment needs to find its own state: where the encrypted token store
// and key material live, and which principal the process is bound to.
func TestLocalStateSettingsResolveThroughEveryLayer(t *testing.T) {
	clearLocalStateEnv(t)

	cfg := Default()
	if cfg.PrincipalID != DefaultPrincipalID {
		t.Errorf("Default().PrincipalID = %q, want %q", cfg.PrincipalID, DefaultPrincipalID)
	}
	if cfg.StateDir != "" {
		t.Errorf("Default().StateDir = %q, want empty so the caller resolves it", cfg.StateDir)
	}

	t.Setenv(envName(keyStateDir), "/var/lib/garmin-mcp-env")
	t.Setenv(envName(keyPrincipalID), "from-env")

	fromEnv, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("Load with the environment layer: %v", err)
	}
	if fromEnv.StateDir != "/var/lib/garmin-mcp-env" {
		t.Errorf("StateDir = %q, want the environment value", fromEnv.StateDir)
	}
	if fromEnv.PrincipalID != "from-env" {
		t.Errorf("PrincipalID = %q, want the environment value", fromEnv.PrincipalID)
	}

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	if err := flags.Parse([]string{
		"--" + keyStateDir + "=/var/lib/garmin-mcp-flag",
		"--" + keyPrincipalID + "=from-flag",
	}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	fromFlags, err := Load(LoadOptions{Flags: flags})
	if err != nil {
		t.Fatalf("Load with the flag layer: %v", err)
	}
	if fromFlags.StateDir != "/var/lib/garmin-mcp-flag" {
		t.Errorf("StateDir = %q, want the flag to win", fromFlags.StateDir)
	}
	if fromFlags.PrincipalID != "from-flag" {
		t.Errorf("PrincipalID = %q, want the flag to win", fromFlags.PrincipalID)
	}
}

// TestPrincipalIDIsValidatedLexically keeps an unusable principal a start-up
// failure. An email address is refused because a principal is an opaque internal
// identifier, and personal data must never key isolation.
func TestPrincipalIDIsValidatedLexically(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "blank", value: "   "},
		{name: "an email address", value: "person@example.test"},
		{name: "control character", value: "local\x00id"},
		{name: "over long", value: strings.Repeat("a", MaxPrincipalIDLen+1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.PrincipalID = tc.value

			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate accepted principal-id %q", tc.value)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("error %v does not match ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), "example.test") {
				t.Errorf("error %q echoes the rejected value", err)
			}
		})
	}
}

// TestStateDirIsValidatedLikeEveryOtherPath keeps the traversal rule uniform.
func TestStateDirIsValidatedLikeEveryOtherPath(t *testing.T) {
	cfg := Default()
	cfg.StateDir = "/var/lib/../../etc"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted a state-dir with a parent-directory segment")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("error %v does not match ErrInvalidConfig", err)
	}
}

// TestRedactedConfigCarriesTheLocalStateSettings keeps the printed effective
// configuration complete: neither setting is secret, and doctor prints both.
func TestRedactedConfigCarriesTheLocalStateSettings(t *testing.T) {
	cfg := Default()
	cfg.StateDir = "/var/lib/garmin-mcp"
	cfg.PrincipalID = "principal-a"

	rendered := cfg.String()
	for _, want := range []string{"/var/lib/garmin-mcp", "principal-a"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("String() = %q, want it to contain %q", rendered, want)
		}
	}
}
