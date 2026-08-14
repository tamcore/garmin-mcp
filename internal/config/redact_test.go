package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const sentinelTokens = "SENTINEL-TOKEN-JSON-9a1c"

// populatedConfig returns a Config whose every secret-bearing field carries
// synthetic sentinel material, so any rendering that reveals a field fails a
// substring assertion.
func populatedConfig(t *testing.T) Config {
	t.Helper()

	cfg := Default()
	cfg.Transport = TransportStreamableHTTP
	cfg.BindAddress = "127.0.0.1:8180"
	cfg.PublicURL = publicHTTPS
	cfg.DatabasePath = databasePath
	cfg.MasterKeyPath = "/run/secrets/master.key"
	cfg.MasterKey = NewSecret(sentinelSecret)
	cfg.GarminTokensPath = "/run/secrets/garmin_tokens.json"
	cfg.GarminTokens = NewSecret(sentinelTokens)
	return cfg
}

// configRenderings collects every path by which a Config can reach a human or a
// log sink. Each entry must be free of secret material.
func configRenderings(t *testing.T, cfg Config) map[string]string {
	t.Helper()

	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal(Config): %v", err)
	}
	encodedPtr, err := json.Marshal(&cfg)
	if err != nil {
		t.Fatalf("json.Marshal(*Config): %v", err)
	}

	var jsonBuf, textBuf, jsonGroup, textGroup bytes.Buffer
	slog.New(slog.NewJSONHandler(&jsonBuf, nil)).Info("effective configuration", "config", cfg)
	slog.New(slog.NewTextHandler(&textBuf, nil)).Info("effective configuration", "config", cfg)
	slog.New(slog.NewJSONHandler(&jsonGroup, nil)).With("config", &cfg).Info("effective configuration")
	slog.New(slog.NewTextHandler(&textGroup, nil)).With("config", &cfg).Info("effective configuration")

	return map[string]string{
		"%v":                fmt.Sprintf("%v", cfg),
		"%+v":               fmt.Sprintf("%+v", cfg),
		"%#v":               fmt.Sprintf("%#v", cfg),
		"%s":                fmt.Sprintf("[%s]", cfg),
		"pointer %v":        fmt.Sprintf("%v", &cfg),
		"pointer %+v":       fmt.Sprintf("%+v", &cfg),
		"pointer %#v":       fmt.Sprintf("%#v", &cfg),
		"json.Marshal":      string(encoded),
		"json.Marshal ptr":  string(encodedPtr),
		"slog json":         jsonBuf.String(),
		"slog text":         textBuf.String(),
		"slog json pointer": jsonGroup.String(),
		"slog text pointer": textGroup.String(),
		"String()":          cfg.String(),
		"GoString()":        cfg.GoString(),
		"LogValue()":        fmt.Sprint(cfg.LogValue()),
		"in slice":          fmt.Sprintf("%v", []Config{cfg}),
		"in map":            fmt.Sprintf("%v", map[string]Config{"cfg": cfg}),
	}
}

func TestConfigNeverRendersSecretMaterial(t *testing.T) {
	t.Parallel()

	cfg := populatedConfig(t)

	for name, rendering := range configRenderings(t, cfg) {
		for _, secret := range []string{sentinelSecret, sentinelTokens} {
			if strings.Contains(rendering, secret) {
				t.Errorf("%s rendering leaks %q:\n%s", name, secret, rendering)
			}
		}
	}
}

// TestConfigRenderingStaysUseful guards against passing the leak test by
// rendering nothing at all: the operator-facing fields must still be present.
func TestConfigRenderingStaysUseful(t *testing.T) {
	t.Parallel()

	cfg := populatedConfig(t)

	for name, rendering := range configRenderings(t, cfg) {
		for _, want := range []string{"streamable-http", "mcp.example.test"} {
			if !strings.Contains(rendering, want) {
				t.Errorf("%s rendering = %q, want it to contain %q", name, rendering, want)
			}
		}
		if !strings.Contains(rendering, redactedMarker) {
			t.Errorf("%s rendering = %q, want the %q marker for the set secrets", name, rendering, redactedMarker)
		}
	}
}

// TestNoCredentialFieldExists is the structural half of the credential rule: a
// password or MFA code must not be representable in configuration at all, so no
// amount of redaction is needed for one.
func TestNoCredentialFieldExists(t *testing.T) {
	t.Parallel()

	forbidden := []string{"password", "passwd", "mfa", "otp", "totp", "credential", "email", "username"}

	for _, name := range fieldNames() {
		lower := strings.ToLower(name)
		for _, bad := range forbidden {
			if strings.Contains(lower, bad) {
				t.Errorf("Config has field %q containing %q: credentials must never be configurable", name, bad)
			}
		}
	}
}
