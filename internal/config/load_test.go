package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// writeConfigFile writes content into a per-test temporary directory and returns
// its path. The file only ever contains synthetic, non-secret settings.
func writeConfigFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "garmin-mcp.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}

// parsedFlags returns a flag set with args applied, as the Cobra root command
// hands it to Load.
func parsedFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse flags %v: %v", args, err)
	}
	return fs
}

func TestLoadWithoutSourcesReturnsDefaults(t *testing.T) {
	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := Default()
	if cfg.Transport != want.Transport {
		t.Errorf("Transport = %q, want %q", cfg.Transport, want.Transport)
	}
	if cfg.LogLevel != want.LogLevel {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, want.LogLevel)
	}
	if cfg.RequestTimeout != want.RequestTimeout {
		t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, want.RequestTimeout)
	}
	if cfg.Region.Domain() != protocol.DomainGlobal {
		t.Errorf("Region = %q, want %q", cfg.Region.Domain(), protocol.DomainGlobal)
	}
}

// TestLoadPrecedence is the deterministic-precedence contract: flags beat the
// environment, the environment beats the config file, and the config file beats
// the defaults.
func TestLoadPrecedence(t *testing.T) {
	file := writeConfigFile(t, "log-level: "+levelWarn+"\n")

	tests := []struct {
		name string
		env  map[string]string
		args []string
		file string
		want string
	}{
		{name: "default wins when nothing else is set", want: "info"},
		{name: "config file beats the default", file: file, want: "warn"},
		{
			name: "environment beats the config file",
			file: file,
			env:  map[string]string{envLogLevel: levelError},
			want: levelError,
		},
		{
			name: "flag beats the environment",
			file: file,
			env:  map[string]string{envLogLevel: levelError},
			args: []string{"--log-level=" + levelDebug},
			want: levelDebug,
		},
		{
			name: "environment beats the default without a file",
			env:  map[string]string{envLogLevel: levelDebug},
			want: levelDebug,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for name, value := range tc.env {
				t.Setenv(name, value)
			}

			cfg, err := Load(LoadOptions{ConfigFile: tc.file, Flags: parsedFlags(t, tc.args...)})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.LogLevel != tc.want {
				t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, tc.want)
			}
		})
	}
}

func TestLoadReadsDurationFromConfigFile(t *testing.T) {
	file := writeConfigFile(t, "request-timeout: 45s\n")

	cfg, err := Load(LoadOptions{ConfigFile: file})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RequestTimeout != 45*time.Second {
		t.Errorf("RequestTimeout = %v, want 45s", cfg.RequestTimeout)
	}
}

func TestLoadReadsEveryTypedSetting(t *testing.T) {
	file := writeConfigFile(t, strings.Join([]string{
		"transport: streamable-http",
		"bind-address: 127.0.0.1:9001",
		"public-url: http://127.0.0.1:9001/mcp",
		"database-path: " + databasePath,
		"master-key-file: /run/secrets/master.key",
		"region: garmin.cn",
		"enable-write-tools: true",
		"tool-allowlist: [" + toolActivities + ", " + toolSleep + "]",
		"max-request-bytes: 2048",
		"read-rate-limit: 42",
		"session-timeout: 90s",
		"allowed-origins: [" + testOrigin + "]",
		"log-format: " + formatJSON,
		"oauth-clients:",
		"  - id: " + testClientID,
		"    name: " + testClientName,
		"    redirect-uris: [" + testRedirectURI + "]",
		"    scopes: [" + testScope + "]",
		"    resources: [http://127.0.0.1:9001/mcp]",
		"    public: true",
	}, "\n")+"\n")

	cfg, err := Load(LoadOptions{ConfigFile: file})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Transport != TransportStreamableHTTP {
		t.Errorf("Transport = %q", cfg.Transport)
	}
	if cfg.BindAddress != "127.0.0.1:9001" {
		t.Errorf("BindAddress = %q", cfg.BindAddress)
	}
	if cfg.Region.Domain() != protocol.DomainChina {
		t.Errorf("Region = %q, want %q", cfg.Region.Domain(), protocol.DomainChina)
	}
	if !cfg.EnableWriteTools {
		t.Error("EnableWriteTools = false, want true")
	}
	if len(cfg.ToolAllowlist) != 2 {
		t.Errorf("ToolAllowlist = %v, want two entries", cfg.ToolAllowlist)
	}
	if cfg.MaxRequestBytes != 2048 {
		t.Errorf("MaxRequestBytes = %d, want 2048", cfg.MaxRequestBytes)
	}
	if cfg.ReadRateLimitPerMinute != 42 {
		t.Errorf("ReadRateLimitPerMinute = %d, want 42", cfg.ReadRateLimitPerMinute)
	}
	if cfg.LogFormat != formatJSON {
		t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
	}
	if cfg.SessionTimeout != 90*time.Second {
		t.Errorf("SessionTimeout = %v, want 90s", cfg.SessionTimeout)
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != testOrigin {
		t.Errorf("AllowedOrigins = %v, want one entry", cfg.AllowedOrigins)
	}
	assertOneRegisteredClient(t, cfg)
}

// assertOneRegisteredClient checks the registry the configuration file declared,
// field by field, because a partially read registration is a client silently
// missing a redirect URI rather than a visible failure.
func assertOneRegisteredClient(t *testing.T, cfg Config) {
	t.Helper()

	if len(cfg.OAuthClients) != 1 {
		t.Fatalf("OAuthClients has %d entries, want 1", len(cfg.OAuthClients))
	}
	client := cfg.OAuthClients[0]
	switch {
	case client.ID != testClientID:
		t.Errorf("client ID = %q, want %q", client.ID, testClientID)
	case client.Name != testClientName:
		t.Errorf("client name = %q, want %q", client.Name, testClientName)
	case len(client.RedirectURIs) != 1 || client.RedirectURIs[0] != testRedirectURI:
		t.Errorf("client redirect URIs = %v", client.RedirectURIs)
	case len(client.Scopes) != 1 || client.Scopes[0] != testScope:
		t.Errorf("client scopes = %v", client.Scopes)
	case len(client.Resources) != 1:
		t.Errorf("client resources = %v", client.Resources)
	case !client.Public:
		t.Error("client is confidential, want public")
	case client.SecretHash.IsSet():
		t.Error("a public client carries a secret digest")
	}
}

// TestLoadSecretFileVariant proves the `_FILE` companion is a separate setting
// that carries a path, never the secret itself.
func TestLoadSecretFileVariant(t *testing.T) {
	t.Setenv("GARMIN_MCP_MASTER_KEY_FILE", "/run/secrets/master.key")
	t.Setenv("GARMIN_MCP_GARMIN_TOKENS_FILE", "/run/secrets/garmin_tokens.json")

	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MasterKeyPath != "/run/secrets/master.key" {
		t.Errorf("MasterKeyPath = %q", cfg.MasterKeyPath)
	}
	if cfg.GarminTokensPath != "/run/secrets/garmin_tokens.json" {
		t.Errorf("GarminTokensPath = %q", cfg.GarminTokensPath)
	}
	if cfg.MasterKey.IsSet() || cfg.GarminTokens.IsSet() {
		t.Error("a _FILE setting must not populate the inline secret")
	}
}

func TestLoadInlineSecretFromEnvironment(t *testing.T) {
	t.Setenv("GARMIN_MCP_MASTER_KEY", sentinelSecret)

	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.MasterKey.IsSet() {
		t.Fatal("MasterKey is unset, want the value from the environment")
	}
	if cfg.MasterKey.Reveal() != sentinelSecret {
		t.Error("MasterKey does not carry the value from the environment")
	}
}

func TestLoadRejectsConflictingInlineAndFileSecret(t *testing.T) {
	tests := []struct {
		name      string
		inlineEnv string
		fileEnv   string
	}{
		{name: "master key", inlineEnv: "GARMIN_MCP_MASTER_KEY", fileEnv: "GARMIN_MCP_MASTER_KEY_FILE"},
		{name: "garmin tokens", inlineEnv: "GARMIN_MCP_GARMIN_TOKENS", fileEnv: "GARMIN_MCP_GARMIN_TOKENS_FILE"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.inlineEnv, sentinelSecret)
			t.Setenv(tc.fileEnv, "/run/secrets/value")

			_, err := Load(LoadOptions{})
			if err == nil {
				t.Fatal("Load accepted a conflicting inline and file secret")
			}
			if !errors.Is(err, ErrSecretConflict) {
				t.Errorf("error %v does not match ErrSecretConflict", err)
			}
			if strings.Contains(err.Error(), sentinelSecret) {
				t.Errorf("error %q leaks the secret", err.Error())
			}
		})
	}
}

func TestLoadValidatesBeforeReturning(t *testing.T) {
	t.Setenv("GARMIN_MCP_TRANSPORT", "streamable-http")

	// No public URL, no database path, and no master key file: a listener must
	// never be opened for this.
	_, err := Load(LoadOptions{})
	if err == nil {
		t.Fatal("Load accepted an incomplete streamable-http configuration")
	}
	if !errors.Is(err, ErrMissingSetting) {
		t.Errorf("error %v does not match ErrMissingSetting", err)
	}
}

func TestLoadRejectsUnknownRegionThroughProtocol(t *testing.T) {
	t.Setenv("GARMIN_MCP_REGION", "garmin.example.test")

	_, err := Load(LoadOptions{})
	if err == nil {
		t.Fatal("Load accepted a region outside the Garmin allowlist")
	}
	if !errors.Is(err, protocol.ErrUnsupportedDomain) {
		t.Errorf("error %v does not match protocol.ErrUnsupportedDomain", err)
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("error %v does not match ErrInvalidConfig", err)
	}
}

func TestLoadRejectsUnknownTransportValue(t *testing.T) {
	t.Setenv("GARMIN_MCP_TRANSPORT", "sse")

	_, err := Load(LoadOptions{})
	if !errors.Is(err, ErrUnsupportedTransport) {
		t.Errorf("error %v does not match ErrUnsupportedTransport", err)
	}
}

func TestLoadRejectsMissingConfigFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")

	_, err := Load(LoadOptions{ConfigFile: missing})
	if err == nil {
		t.Fatal("Load accepted a config file that does not exist")
	}
	if !errors.Is(err, ErrConfigFile) {
		t.Errorf("error %v does not match ErrConfigFile", err)
	}
}

func TestLoadRejectsMalformedConfigFile(t *testing.T) {
	file := writeConfigFile(t, "transport: [unclosed\n")

	_, err := Load(LoadOptions{ConfigFile: file})
	if !errors.Is(err, ErrConfigFile) {
		t.Errorf("error %v does not match ErrConfigFile", err)
	}
}

// TestLoadConfigFileFlagBeatsTheOption keeps the operator's explicit --config in
// charge of which file is read.
func TestLoadConfigFileFlagBeatsTheOption(t *testing.T) {
	fromFlag := writeConfigFile(t, "log-level: "+levelDebug+"\n")
	fromOption := writeConfigFile(t, "log-level: "+levelWarn+"\n")

	cfg, err := Load(LoadOptions{
		ConfigFile: fromOption,
		Flags:      parsedFlags(t, "--config="+fromFlag),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != levelDebug {
		t.Errorf("LogLevel = %q, want debug from the --config file", cfg.LogLevel)
	}
	if cfg.ConfigFile != fromFlag {
		t.Errorf("ConfigFile = %q, want %q", cfg.ConfigFile, fromFlag)
	}
}

// TestLoadIgnoresACredentialEnvironmentVariable proves the credential rule holds
// at the loader boundary and not only in the settings table.
func TestLoadIgnoresACredentialEnvironmentVariable(t *testing.T) {
	t.Setenv("GARMIN_MCP_PASSWORD", "hunter2")
	t.Setenv("GARMIN_MCP_MFA_CODE", "123456")

	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	rendered := cfg.String()
	for _, forbidden := range []string{"hunter2", "123456"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("configuration absorbed a credential: %q", rendered)
		}
	}
}
