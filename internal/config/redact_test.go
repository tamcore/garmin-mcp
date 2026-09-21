package config

import (
	"fmt"
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
	cfg.MetricsAddress = metricsLoopback
	return cfg
}

// configRenderings collects every path by which a Config can reach a human or a
// log sink. Each entry must be free of secret material.
func configRenderings(t *testing.T, cfg Config) map[string]string {
	t.Helper()

	out := renderAll(t, cfg)
	out["String()"] = cfg.String()
	out["GoString()"] = cfg.GoString()
	out["LogValue()"] = fmt.Sprint(cfg.LogValue())
	return out
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
		for _, want := range []string{"streamable-http", "mcp.example.test", metricsLoopback} {
			if !strings.Contains(rendering, want) {
				t.Errorf("%s rendering = %q, want it to contain %q", name, rendering, want)
			}
		}
		if !strings.Contains(rendering, redactedMarker) {
			t.Errorf("%s rendering = %q, want the %q marker for the set secrets", name, rendering, redactedMarker)
		}
	}
}

func TestRedactedConfigCarriesTheWildcardAcknowledgement(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.OAuthAllowRedirectWildcards = true

	rendered := cfg.String()
	if !strings.Contains(rendered, "oauthAllowRedirectWildcards") {
		t.Fatalf("redacted output does not name the acknowledgement: %s", rendered)
	}
}

// TestRedactedConfigCountsAllowedEmailsWithoutRenderingThem proves the login
// allowlist is redacted by count, never by value: an address configured into
// LoginAllowedEmails must never appear in rendered configuration output. It
// covers every rendering path — String, GoString, MarshalJSON, and LogValue —
// because TestNoCredentialFieldExists's LoginAllowedEmails exemption and
// TestSecretSettingsHaveNoFlag's login-allowed-emails case both cite this test
// as proof no address value is ever rendered, and a check of String alone would
// not prove that for the other three.
func TestRedactedConfigCountsAllowedEmailsWithoutRenderingThem(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.LoginAllowedEmails = []string{"secret@example.com"}

	renderings := configRenderings(t, cfg)

	for name, rendered := range renderings {
		if strings.Contains(rendered, "secret@example.com") {
			t.Errorf("%s rendered an allowlisted address:\n%s", name, rendered)
		}
	}
	if !strings.Contains(renderings["String()"], "loginAllowedEmailsLen") {
		t.Fatalf("redacted output does not report the allowlist size: %s", renderings["String()"])
	}
}

// TestNoCredentialFieldExists is the structural half of the credential rule: a
// password or MFA code must not be representable in configuration at all, so no
// amount of redaction is needed for one.
func TestNoCredentialFieldExists(t *testing.T) {
	t.Parallel()

	forbidden := []string{"password", "passwd", "mfa", "otp", "totp", "credential", "email", "username"}

	// LoginAllowedEmails is exempted by exact equality, not by dropping "email"
	// from forbidden: it carries an allowlist of account addresses, not a
	// credential, is never a secret input, and
	// TestRedactedConfigCountsAllowedEmailsWithoutRenderingThem proves no
	// address value is ever rendered. A new email-shaped field must still fail
	// this guard.
	const safeEmailField = "LoginAllowedEmails"

	for _, name := range fieldNames() {
		if name == safeEmailField {
			continue
		}
		lower := strings.ToLower(name)
		for _, bad := range forbidden {
			if strings.Contains(lower, bad) {
				t.Errorf("Config has field %q containing %q: credentials must never be configurable", name, bad)
			}
		}
	}
}
