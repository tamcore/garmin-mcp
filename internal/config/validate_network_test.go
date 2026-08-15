package config

import (
	"errors"
	"strings"
	"testing"
)

// The network-facing half of the configuration tests: the Streamable HTTP
// listener, the canonical public origin, TLS and proxy trust, and the persistent
// state a remote deployment must have. The stdio and transport-independent cases
// live in validate_test.go.

// validHTTPConfig is the smallest streamable-http configuration that validates.
// Every negative case below is this value with exactly one field changed.
func validHTTPConfig() Config {
	cfg := Default()
	cfg.Transport = TransportStreamableHTTP
	cfg.BindAddress = "127.0.0.1:8180"
	cfg.PublicURL = "http://127.0.0.1:8180"
	cfg.DatabasePath = databasePath
	cfg.MasterKeyPath = masterKeyPath
	cfg.OAuthClients = []OAuthClient{publicClient()}
	return cfg
}

func TestValidateAcceptsSafeHTTPConfigurations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "loopback baseline", mutate: func(*Config) {}},
		{
			name: "public https with tls material",
			mutate: func(c *Config) {
				c.BindAddress = "0.0.0.0:8443"
				c.PublicURL = publicHTTPS
				c.TLSCertFile = tlsCertPath
				c.TLSKeyFile = tlsKeyPath
			},
		},
		{
			name: "public https behind a trusted proxy",
			mutate: func(c *Config) {
				c.BindAddress = "0.0.0.0:8180"
				c.PublicURL = publicHTTPS
				c.TrustedProxyCIDRs = []string{cidrPrivate, "fd00::/8"}
			},
		},
		{
			name:   "public url with a path prefix",
			mutate: func(c *Config) { c.PublicURL = "http://127.0.0.1:8180/mcp" },
		},
		{
			name: "tool allowlist and denylist without overlap",
			mutate: func(c *Config) {
				c.ToolAllowlist = []string{toolActivities, toolSleep}
				c.ToolDenylist = []string{toolDelete}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := validHTTPConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestValidateRejectsUnsafeNetworkConfigurations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		base     func() Config
		mutate   func(*Config)
		sentinel error
		field    string
	}{
		{
			name:     "http without a public url",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.PublicURL = "" },
			sentinel: ErrMissingSetting,
			field:    keyPublicURL,
		},
		{
			name:     "http without a database path",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.DatabasePath = "" },
			sentinel: ErrMissingSetting,
			field:    keyDatabasePath,
		},
		{
			name:     "http without a master key file",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.MasterKeyPath = "" },
			sentinel: ErrMissingSetting,
			field:    keyMasterKeyFile,
		},
		{
			name: "http with an inline master key",
			base: validHTTPConfig,
			mutate: func(c *Config) {
				c.MasterKeyPath = ""
				c.MasterKey = NewSecret(sentinelSecret)
			},
			sentinel: ErrInsecureSetting,
			field:    keyMasterKey,
		},
		{
			name:     "http with inline garmin tokens",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.GarminTokens = NewSecret(sentinelTokens) },
			sentinel: ErrInsecureSetting,
			field:    keyGarminTokens,
		},
		{
			name:     "http bind address without a port",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.BindAddress = "127.0.0.1" },
			sentinel: ErrInvalidConfig,
			field:    keyBindAddress,
		},
		{
			name:     "http bind address with port zero",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.BindAddress = "127.0.0.1:0" },
			sentinel: ErrInvalidConfig,
			field:    keyBindAddress,
		},
		{
			name:     "http bind address is empty",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.BindAddress = "" },
			sentinel: ErrMissingSetting,
			field:    keyBindAddress,
		},
		{
			name:     "public url is relative",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.PublicURL = "/mcp" },
			sentinel: ErrInvalidConfig,
			field:    keyPublicURL,
		},
		{
			name:     "public url carries userinfo",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.PublicURL = "https://user:pw@mcp.example.test" },
			sentinel: ErrInvalidConfig,
			field:    keyPublicURL,
		},
		{
			name:     "public url carries a query",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.PublicURL = "https://mcp.example.test/?token=x" },
			sentinel: ErrInvalidConfig,
			field:    keyPublicURL,
		},
		{
			name:     "public url carries a fragment",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.PublicURL = "https://mcp.example.test/#f" },
			sentinel: ErrInvalidConfig,
			field:    keyPublicURL,
		},
		{
			name:     "public url uses an unsupported scheme",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.PublicURL = "ftp://mcp.example.test" },
			sentinel: ErrInvalidConfig,
			field:    keyPublicURL,
		},
		{
			name:     "cleartext public url without the override",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.PublicURL = "http://mcp.example.test" },
			sentinel: ErrInsecureSetting,
			field:    keyPublicURL,
		},
		{
			name: "public bind without tls, proxy trust, or override",
			base: validHTTPConfig,
			mutate: func(c *Config) {
				c.BindAddress = "0.0.0.0:8180"
				c.PublicURL = publicHTTPS
			},
			sentinel: ErrInsecureSetting,
			field:    keyBindAddress,
		},
		{
			name:     "tls certificate without a key",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.TLSCertFile = tlsCertPath },
			sentinel: ErrMissingSetting,
			field:    keyTLSKeyFile,
		},
		{
			name:     "tls key without a certificate",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.TLSKeyFile = tlsKeyPath },
			sentinel: ErrMissingSetting,
			field:    keyTLSCertFile,
		},
		{
			name:     "trusted proxy is not a cidr",
			base:     validHTTPConfig,
			mutate:   func(c *Config) { c.TrustedProxyCIDRs = []string{"10.0.0.1"} },
			sentinel: ErrInvalidConfig,
			field:    keyTrustedProxyCIDRs,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := tc.base()
			tc.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("error %v does not match ErrInvalidConfig", err)
			}
			if !errors.Is(err, tc.sentinel) {
				t.Errorf("error %v does not match the expected sentinel %v", err, tc.sentinel)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error %q does not name the offending setting %q", err.Error(), tc.field)
			}
		})
	}
}

// TestValidateReportsEveryProblem proves validation is complete rather than
// first-failure: an operator fixing a deployment sees all of it at once.
func TestValidateReportsEveryProblem(t *testing.T) {
	t.Parallel()

	cfg := validHTTPConfig()
	cfg.PublicURL = ""
	cfg.DatabasePath = ""
	cfg.LogLevel = "trace"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error")
	}
	for _, field := range []string{keyPublicURL, keyDatabasePath, keyLogLevel} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("error %q does not report %q", err.Error(), field)
		}
	}
}

// TestValidateNeverLeaksSecrets keeps rejected configuration out of the log with
// its secret material attached.
func TestValidateNeverLeaksSecrets(t *testing.T) {
	t.Parallel()

	cfg := validHTTPConfig()
	cfg.MasterKey = NewSecret(sentinelSecret)
	cfg.GarminTokens = NewSecret(sentinelTokens)

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error for inline secrets in remote mode")
	}
	for _, secret := range []string{sentinelSecret, sentinelTokens} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q leaks %q", err.Error(), secret)
		}
	}
}
