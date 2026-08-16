package config

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

func TestValidateAcceptsSafeStdioConfigurations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "defaults", mutate: func(*Config) {}},
		{
			name:   "token file",
			mutate: func(c *Config) { c.GarminTokensPath = tokenFilePath },
		},
		{name: "inline tokens", mutate: func(c *Config) { c.GarminTokens = NewSecret(sentinelTokens) }},
		{name: "inline master key", mutate: func(c *Config) { c.MasterKey = NewSecret(sentinelSecret) }},
		{name: "china region", mutate: func(c *Config) { c.Region = mustRegion(protocol.DomainChina) }},
		{name: "write tools enabled", mutate: func(c *Config) { c.EnableWriteTools = true }},
		{
			name: "both tiers enabled",
			mutate: func(c *Config) {
				c.EnableWriteTools = true
				c.EnableDestructiveTools = true
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := Default()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestFieldErrorUnwrapsBothSentinels(t *testing.T) {
	t.Parallel()

	err := newFieldError(keyBindAddress, "must include a port", ErrMissingSetting)

	if !errors.Is(err, ErrInvalidConfig) {
		t.Error("FieldError does not match ErrInvalidConfig")
	}
	if !errors.Is(err, ErrMissingSetting) {
		t.Error("FieldError does not match its own sentinel")
	}

	var fieldErr *FieldError
	if !errors.As(err, &fieldErr) {
		t.Fatal("errors.As does not extract *FieldError")
	}
	if fieldErr.Field != keyBindAddress {
		t.Errorf("Field = %q, want %q", fieldErr.Field, keyBindAddress)
	}
}

func mustRegion(domain protocol.Domain) protocol.ValidatedDomain {
	validated, err := domain.Validate()
	if err != nil {
		panic(err)
	}
	return validated
}

func TestValidateRejectsUnsafeConfigurations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		base     func() Config
		mutate   func(*Config)
		sentinel error
		field    string
	}{
		{
			name:     "unknown transport",
			base:     Default,
			mutate:   func(c *Config) { c.Transport = "sse" },
			sentinel: ErrUnsupportedTransport,
			field:    keyTransport,
		},
		{
			name:     "unvalidated region",
			base:     Default,
			mutate:   func(c *Config) { c.Region = protocol.ValidatedDomain{} },
			sentinel: protocol.ErrUnsupportedDomain,
			field:    keyRegion,
		},
		{
			name:     "stdio with a public url",
			base:     Default,
			mutate:   func(c *Config) { c.PublicURL = publicHTTPS },
			sentinel: ErrInapplicableSetting,
			field:    keyPublicURL,
		},
		{
			name:     "stdio with tls material",
			base:     Default,
			mutate:   func(c *Config) { c.TLSCertFile = tlsCertPath },
			sentinel: ErrInapplicableSetting,
			field:    keyTLSCertFile,
		},
		{
			name:     "stdio with trusted proxies",
			base:     Default,
			mutate:   func(c *Config) { c.TrustedProxyCIDRs = []string{cidrPrivate} },
			sentinel: ErrInapplicableSetting,
			field:    keyTrustedProxyCIDRs,
		},
		{
			name:     "stdio with the insecure http override",
			base:     Default,
			mutate:   func(c *Config) { c.AllowInsecureHTTP = true },
			sentinel: ErrInapplicableSetting,
			field:    keyAllowInsecureHTTP,
		},
		{
			name:     "destructive tier without the write tier",
			base:     Default,
			mutate:   func(c *Config) { c.EnableDestructiveTools = true },
			sentinel: ErrInvalidConfig,
			field:    keyEnableDestructiveTools,
		},
		{
			name: "the same tool is allowed and denied",
			base: Default,
			mutate: func(c *Config) {
				c.ToolAllowlist = []string{toolActivities}
				c.ToolDenylist = []string{toolActivities}
			},
			sentinel: ErrInvalidConfig,
			field:    keyToolDenylist,
		},
		{
			name:     "tool name is not a valid identifier",
			base:     Default,
			mutate:   func(c *Config) { c.ToolAllowlist = []string{"Get Activities"} },
			sentinel: ErrInvalidConfig,
			field:    keyToolAllowlist,
		},
		{
			name:     "duplicate tool name",
			base:     Default,
			mutate:   func(c *Config) { c.ToolAllowlist = []string{toolActivities, toolActivities} },
			sentinel: ErrInvalidConfig,
			field:    keyToolAllowlist,
		},
		{
			name: "inline master key conflicts with the key file",
			base: Default,
			mutate: func(c *Config) {
				c.MasterKey = NewSecret(sentinelSecret)
				c.MasterKeyPath = masterKeyPath
			},
			sentinel: ErrSecretConflict,
			field:    keyMasterKey,
		},
		{
			name: "inline tokens conflict with the token file",
			base: Default,
			mutate: func(c *Config) {
				c.GarminTokens = NewSecret(sentinelTokens)
				c.GarminTokensPath = tokenFilePath
			},
			sentinel: ErrSecretConflict,
			field:    keyGarminTokens,
		},
		{
			name:     "path escapes with a traversal segment",
			base:     Default,
			mutate:   func(c *Config) { c.GarminTokensPath = "/var/lib/../../etc/shadow" },
			sentinel: ErrInvalidConfig,
			field:    keyGarminTokensFile,
		},
		{
			name:     "request timeout is zero",
			base:     Default,
			mutate:   func(c *Config) { c.RequestTimeout = 0 },
			sentinel: ErrInvalidConfig,
			field:    keyRequestTimeout,
		},
		{
			name:     "request timeout exceeds the cap",
			base:     Default,
			mutate:   func(c *Config) { c.RequestTimeout = time.Hour },
			sentinel: ErrInvalidConfig,
			field:    keyRequestTimeout,
		},
		{
			name:     "safety delay is negative",
			base:     Default,
			mutate:   func(c *Config) { c.SafetyDelay = -time.Second },
			sentinel: ErrInvalidConfig,
			field:    keySafetyDelay,
		},
		{
			name:     "safety delay exceeds the cap",
			base:     Default,
			mutate:   func(c *Config) { c.SafetyDelay = MaxSafetyDelay + time.Second },
			sentinel: ErrInvalidConfig,
			field:    keySafetyDelay,
		},
		{
			name:     "request bytes is negative",
			base:     Default,
			mutate:   func(c *Config) { c.MaxRequestBytes = -1 },
			sentinel: ErrInvalidConfig,
			field:    keyMaxRequestBytes,
		},
		{
			name:     "response bytes exceeds the cap",
			base:     Default,
			mutate:   func(c *Config) { c.MaxResponseBytes = MaxResponseBytesCap + 1 },
			sentinel: ErrInvalidConfig,
			field:    keyMaxResponseBytes,
		},
		{
			name:     "read rate limit is zero",
			base:     Default,
			mutate:   func(c *Config) { c.ReadRateLimitPerMinute = 0 },
			sentinel: ErrInvalidConfig,
			field:    keyReadRateLimit,
		},
		{
			name:     "write rate limit is negative",
			base:     Default,
			mutate:   func(c *Config) { c.WriteRateLimitPerMinute = -5 },
			sentinel: ErrInvalidConfig,
			field:    keyWriteRateLimit,
		},
		{
			name:     "unknown log level",
			base:     Default,
			mutate:   func(c *Config) { c.LogLevel = "trace" },
			sentinel: ErrInvalidConfig,
			field:    keyLogLevel,
		},
		{
			name:     "unknown log format",
			base:     Default,
			mutate:   func(c *Config) { c.LogFormat = "logfmt" },
			sentinel: ErrInvalidConfig,
			field:    keyLogFormat,
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
