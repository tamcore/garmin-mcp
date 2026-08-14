package oauthserver

import (
	"errors"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		Issuer:                testIssuerURI,
		Resource:              testResourceURI,
		AuthorizationEndpoint: "https://mcp.example/oauth/authorize",
		TokenEndpoint:         "https://mcp.example/oauth/token",
		RevocationEndpoint:    "https://mcp.example/oauth/revoke",
		ResourceMetadataURL:   "https://mcp.example/.well-known/oauth-protected-resource",
		ResourceName:          "garmin-mcp",
		ScopesSupported:       testScopesBoth,
	}
}

func TestNewAppliesConservativeDefaults(t *testing.T) {
	srv, err := New(testConfig(), Deps{Store: newFakeStore()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := srv.CodeTTL(); got != DefaultCodeTTL {
		t.Fatalf("CodeTTL() = %v, want %v", got, DefaultCodeTTL)
	}
	if srv.CodeTTL() > MaxCodeTTL {
		t.Fatalf("CodeTTL() = %v, which exceeds the five minute ceiling", srv.CodeTTL())
	}
	if got := srv.AccessTokenTTL(); got != DefaultAccessTokenTTL {
		t.Fatalf("AccessTokenTTL() = %v, want %v", got, DefaultAccessTokenTTL)
	}
	if got := srv.RefreshTokenTTL(); got != DefaultRefreshTokenTTL {
		t.Fatalf("RefreshTokenTTL() = %v, want %v", got, DefaultRefreshTokenTTL)
	}
	if got := srv.TransactionTTL(); got != DefaultTransactionTTL {
		t.Fatalf("TransactionTTL() = %v, want %v", got, DefaultTransactionTTL)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	with := func(mutate func(*Config)) Config {
		cfg := testConfig()
		mutate(&cfg)
		return cfg
	}

	cases := map[string]struct {
		cfg  Config
		want error
	}{
		"no issuer":         {with(func(c *Config) { c.Issuer = "" }), ErrInvalidConfig},
		"issuer not https":  {with(func(c *Config) { c.Issuer = "http://mcp.example" }), ErrInvalidConfig},
		"issuer with query": {with(func(c *Config) { c.Issuer = testIssuerURI + "?x=1" }), ErrInvalidConfig},
		"issuer fragment":   {with(func(c *Config) { c.Issuer = testIssuerURI + "#f" }), ErrInvalidConfig},
		"no resource":       {with(func(c *Config) { c.Resource = "" }), ErrInvalidResource},
		"bad resource":      {with(func(c *Config) { c.Resource = "http://evil.example/mcp" }), ErrInvalidResource},
		"no authorize":      {with(func(c *Config) { c.AuthorizationEndpoint = "" }), ErrInvalidConfig},
		"no token":          {with(func(c *Config) { c.TokenEndpoint = "" }), ErrInvalidConfig},
		"authorize plain": {with(func(c *Config) {
			c.AuthorizationEndpoint = "http://x.example/a"
		}), ErrInvalidConfig},
		"no metadata URL":   {with(func(c *Config) { c.ResourceMetadataURL = "" }), ErrInvalidConfig},
		"bad scopes":        {with(func(c *Config) { c.ScopesSupported = testMalformedScope }), ErrInvalidScope},
		"no scopes":         {with(func(c *Config) { c.ScopesSupported = "" }), ErrInvalidConfig},
		"code TTL too long": {with(func(c *Config) { c.CodeTTL = MaxCodeTTL + time.Second }), ErrInvalidConfig},
		"negative TTL":      {with(func(c *Config) { c.AccessTokenTTL = -time.Second }), ErrInvalidConfig},
		"access outlives refresh": {with(func(c *Config) {
			c.AccessTokenTTL = 2 * time.Hour
			c.RefreshTokenTTL = time.Hour
		}), ErrInvalidConfig},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(tc.cfg, Deps{Store: newFakeStore()}); !errors.Is(err, tc.want) {
				t.Fatalf("New error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNewRequiresAStore(t *testing.T) {
	if _, err := New(testConfig(), Deps{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("New accepted a nil store")
	}
}

func TestNewDefaultsTheClockAndAcceptsAnInjectedOne(t *testing.T) {
	fixed := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	srv, err := New(testConfig(), Deps{Store: newFakeStore(), Now: func() time.Time { return fixed }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !srv.now().Equal(fixed) {
		t.Fatalf("now() = %v, want the injected clock %v", srv.now(), fixed)
	}

	defaulted, err := New(testConfig(), Deps{Store: newFakeStore()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if defaulted.now().IsZero() {
		t.Fatal("the default clock returned the zero time")
	}
}

func TestServerExposesTheCanonicalIssuerAndResource(t *testing.T) {
	srv, err := New(testConfig(), Deps{Store: newFakeStore()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := srv.Issuer(); got != testIssuerURI {
		t.Fatalf("Issuer() = %q", got)
	}
	if !srv.Resource().Equal(mustResource(t, testResourceURI)) {
		t.Fatalf("Resource() = %q", srv.Resource())
	}
}
