package config

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestEnvNameUsesTheDocumentedPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want string
	}{
		{key: "transport", want: "GARMIN_MCP_TRANSPORT"},
		{key: "bind-address", want: "GARMIN_MCP_BIND_ADDRESS"},
		{key: "master-key", want: "GARMIN_MCP_MASTER_KEY"},
		{key: "master-key-file", want: "GARMIN_MCP_MASTER_KEY_FILE"},
		{key: "garmin-tokens-file", want: "GARMIN_MCP_GARMIN_TOKENS_FILE"},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			if got := envName(tc.key); got != tc.want {
				t.Errorf("envName(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// TestEverySecretSettingHasAFileVariant is the `_FILE` half of the secret
// contract: no secret may be inline-only.
func TestEverySecretSettingHasAFileVariant(t *testing.T) {
	t.Parallel()

	known := make(map[string]struct{})
	for _, s := range settings() {
		known[s.key] = struct{}{}
	}

	secretCount := 0
	for _, s := range settings() {
		if !s.secret {
			continue
		}
		secretCount++
		if _, ok := known[s.key+fileSuffix]; !ok {
			t.Errorf("secret setting %q has no %q companion", s.key, s.key+fileSuffix)
		}
	}
	if secretCount == 0 {
		t.Fatal("no secret setting is declared: the test would pass vacuously")
	}
}

// TestSecretSettingsHaveNoFlag keeps secret material out of the process command
// line, where any local user can read it.
func TestSecretSettingsHaveNoFlag(t *testing.T) {
	t.Parallel()

	for _, s := range settings() {
		if s.secret && s.flag != "" {
			t.Errorf("secret setting %q exposes flag --%s", s.key, s.flag)
		}
	}
}

// TestNoCredentialSettingExists mirrors TestNoCredentialFieldExists on the input
// side: a password or MFA code must not be reachable through a flag, an
// environment variable, or a config-file key.
func TestNoCredentialSettingExists(t *testing.T) {
	t.Parallel()

	forbidden := []string{"password", "passwd", "mfa", "otp", "totp", "credential", "email", "username"}

	for _, s := range settings() {
		for _, bad := range forbidden {
			if strings.Contains(s.key, bad) {
				t.Errorf("setting %q contains %q: credentials must never be configurable", s.key, bad)
			}
			if s.flag != "" && strings.Contains(s.flag, bad) {
				t.Errorf("flag --%s contains %q: credentials must never be configurable", s.flag, bad)
			}
		}
	}
}

func TestRegisterFlagsCoversEveryFlaggedSetting(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(fs)

	for _, s := range settings() {
		flag := fs.Lookup(s.flag)
		if s.flag == "" {
			continue
		}
		if flag == nil {
			t.Errorf("setting %q declares flag --%s but it was not registered", s.key, s.flag)
			continue
		}
		if flag.Usage == "" {
			t.Errorf("flag --%s has no usage text", s.flag)
		}
	}
}

func TestRegisterFlagsIsIdempotentAcrossFlagSets(t *testing.T) {
	t.Parallel()

	first := pflag.NewFlagSet("first", pflag.ContinueOnError)
	second := pflag.NewFlagSet("second", pflag.ContinueOnError)
	RegisterFlags(first)
	RegisterFlags(second)

	if first.Lookup("transport") == nil || second.Lookup("transport") == nil {
		t.Fatal("--transport missing from an independently registered flag set")
	}
	if err := first.Set("transport", "streamable-http"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := second.GetString("transport")
	if err != nil {
		t.Fatalf("GetString: %v", err)
	}
	if got != string(TransportStdio) {
		t.Errorf("second flag set observed %q: the two share state", got)
	}
}

func TestSettingsHaveUniqueKeysAndFlags(t *testing.T) {
	t.Parallel()

	keys := make(map[string]struct{})
	flags := make(map[string]struct{})
	for _, s := range settings() {
		if _, dup := keys[s.key]; dup {
			t.Errorf("duplicate setting key %q", s.key)
		}
		keys[s.key] = struct{}{}
		if s.flag == "" {
			continue
		}
		if _, dup := flags[s.flag]; dup {
			t.Errorf("duplicate flag --%s", s.flag)
		}
		flags[s.flag] = struct{}{}
	}
}
