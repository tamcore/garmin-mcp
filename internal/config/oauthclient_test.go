package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The synthetic client registry every test in this file starts from. The digest
// sentinel is material that must never reach an error or a rendering.
const (
	testClientID    = "example-client"
	testClientName  = "Example MCP client"
	testRedirectURI = "https://client.example.test/callback"
	testResource    = "https://mcp.example.test/mcp"
	testScope       = "garmin:read"
	testDigestPath  = "/run/secrets/example-client.sha256"
	sentinelDigest  = "SENTINEL-CLIENT-DIGEST-4c7e"
	testOrigin      = "https://client.example.test"
)

// publicClient is the smallest registration that validates.
func publicClient() OAuthClient {
	return OAuthClient{
		ID:           testClientID,
		Name:         testClientName,
		RedirectURIs: []string{testRedirectURI},
		Scopes:       []string{testScope},
		Resources:    []string{testResource},
		Public:       true,
	}
}

// confidentialClient is the same registration with a file-supplied digest.
func confidentialClient() OAuthClient {
	client := publicClient()
	client.Public = false
	client.SecretHashPath = testDigestPath
	return client
}

// remoteConfig is the smallest streamable-http configuration with a registry.
func remoteConfig() Config {
	cfg := validHTTPConfig()
	cfg.OAuthClients = []OAuthClient{publicClient()}
	return cfg
}

func TestValidateAcceptsRemoteRegistries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "one public client", mutate: func(*Config) {}},
		{
			name:   "one confidential client with a digest file",
			mutate: func(c *Config) { c.OAuthClients = []OAuthClient{confidentialClient()} },
		},
		{
			name: "one confidential client with an inline digest",
			mutate: func(c *Config) {
				client := confidentialClient()
				client.SecretHashPath = ""
				client.SecretHash = NewSecret(sentinelDigest)
				c.OAuthClients = []OAuthClient{client}
			},
		},
		{
			name: "a loopback redirect over cleartext",
			mutate: func(c *Config) {
				client := publicClient()
				client.RedirectURIs = []string{"http://127.0.0.1:41234/callback"}
				c.OAuthClients = []OAuthClient{client}
			},
		},
		{
			name:   "an origin allowlist",
			mutate: func(c *Config) { c.AllowedOrigins = []string{testOrigin} },
		},
		{
			name:   "an explicit session timeout",
			mutate: func(c *Config) { c.SessionTimeout = 2 * time.Minute },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := remoteConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestValidateRejectsUnusableRegistries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*Config)
		sentinel error
	}{
		{
			name:     "no client at all",
			mutate:   func(c *Config) { c.OAuthClients = nil },
			sentinel: ErrMissingSetting,
		},
		{
			name: "a client with no redirect uri",
			mutate: func(c *Config) {
				client := publicClient()
				client.RedirectURIs = nil
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrMissingSetting,
		},
		{
			name: "a wildcard redirect uri",
			mutate: func(c *Config) {
				client := publicClient()
				client.RedirectURIs = []string{"https://*.example.test/callback"}
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrInvalidConfig,
		},
		{
			name: "a redirect uri with a fragment",
			mutate: func(c *Config) {
				client := publicClient()
				client.RedirectURIs = []string{"https://client.example.test/cb#done"}
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrInvalidConfig,
		},
		{
			name: "a redirect uri with userinfo",
			mutate: func(c *Config) {
				client := publicClient()
				client.RedirectURIs = []string{"https://user:pass@client.example.test/cb"}
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrInvalidConfig,
		},
		{
			name: "a relative redirect uri",
			mutate: func(c *Config) {
				client := publicClient()
				client.RedirectURIs = []string{"/callback"}
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrInvalidConfig,
		},
		{
			name: "a cleartext non-loopback redirect uri",
			mutate: func(c *Config) {
				client := publicClient()
				client.RedirectURIs = []string{"http://client.example.test/cb"}
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrInsecureSetting,
		},
		{
			name: "a confidential client without a digest",
			mutate: func(c *Config) {
				client := publicClient()
				client.Public = false
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrMissingSetting,
		},
		{
			name: "a public client carrying a digest",
			mutate: func(c *Config) {
				client := publicClient()
				client.SecretHashPath = testDigestPath
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrInvalidConfig,
		},
		{
			name: "a confidential client with both an inline digest and a digest file",
			mutate: func(c *Config) {
				client := confidentialClient()
				client.SecretHash = NewSecret(sentinelDigest)
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrSecretConflict,
		},
		{
			name: "a client with no scope",
			mutate: func(c *Config) {
				client := publicClient()
				client.Scopes = nil
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrMissingSetting,
		},
		{
			name: "a client with no resource",
			mutate: func(c *Config) {
				client := publicClient()
				client.Resources = nil
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrMissingSetting,
		},
		{
			name: "two clients with one identifier",
			mutate: func(c *Config) {
				c.OAuthClients = []OAuthClient{publicClient(), publicClient()}
			},
			sentinel: ErrInvalidConfig,
		},
		{
			name: "a blank client identifier",
			mutate: func(c *Config) {
				client := publicClient()
				client.ID = "  "
				c.OAuthClients = []OAuthClient{client}
			},
			sentinel: ErrMissingSetting,
		},
		{
			name:     "an origin that is not an origin",
			mutate:   func(c *Config) { c.AllowedOrigins = []string{"client.example.test"} },
			sentinel: ErrInvalidConfig,
		},
		{
			name:     "an origin carrying a path",
			mutate:   func(c *Config) { c.AllowedOrigins = []string{"https://client.example.test/app"} },
			sentinel: ErrInvalidConfig,
		},
		{
			name:     "a session timeout below the floor",
			mutate:   func(c *Config) { c.SessionTimeout = time.Millisecond },
			sentinel: ErrInvalidConfig,
		},
		{
			name:     "a session timeout above the ceiling",
			mutate:   func(c *Config) { c.SessionTimeout = MaxSessionTimeout + time.Second },
			sentinel: ErrInvalidConfig,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := remoteConfig()
			tc.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("error %v does not match ErrInvalidConfig", err)
			}
			if !errors.Is(err, tc.sentinel) {
				t.Errorf("error %v does not match %v", err, tc.sentinel)
			}
			if strings.Contains(err.Error(), sentinelDigest) {
				t.Error("the error echoes the supplied digest")
			}
		})
	}
}

// TestRegistryKeysMatchTheWireShape keeps the documented sub-keys and the decoded
// document from drifting apart. A struct tag cannot be a constant, so the two
// spellings are compared here instead of shared.
func TestRegistryKeysMatchTheWireShape(t *testing.T) {
	t.Parallel()

	want := []string{
		keyClientID, keyClientName, keyClientRedirectURIs, keyClientScopes,
		keyClientResources, keyClientPublic, keyClientSecretHash, keyClientSecretHashFile,
	}

	typ := reflect.TypeFor[clientWire]()
	if typ.NumField() != len(want) {
		t.Fatalf("clientWire has %d fields, the documented keys name %d", typ.NumField(), len(want))
	}
	for i, key := range want {
		if got := typ.Field(i).Tag.Get("json"); got != key {
			t.Errorf("field %d is tagged %q, want the documented key %q", i, got, key)
		}
	}
}

// TestStdioRejectsRemoteOnlySettings keeps a setting nothing reads from looking
// like it is in force.
func TestStdioRejectsRemoteOnlySettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name:   "a client registry",
			mutate: func(c *Config) { c.OAuthClients = []OAuthClient{publicClient()} },
		},
		{
			name:   "an origin allowlist",
			mutate: func(c *Config) { c.AllowedOrigins = []string{testOrigin} },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := Default()
			tc.mutate(&cfg)

			err := cfg.Validate()
			if !errors.Is(err, ErrInapplicableSetting) {
				t.Errorf("error %v does not match ErrInapplicableSetting", err)
			}
		})
	}
}

// TestCloneCopiesTheRegistry keeps the immutability promise: a caller that adjusts
// its own copy must not reach into another's.
func TestCloneCopiesTheRegistry(t *testing.T) {
	t.Parallel()

	cfg := remoteConfig()
	cfg.AllowedOrigins = []string{testOrigin}

	clone := cfg.Clone()
	clone.OAuthClients[0].ID = "other"
	clone.OAuthClients[0].RedirectURIs[0] = "https://other.example.test/cb"
	clone.AllowedOrigins[0] = "https://other.example.test"

	if cfg.OAuthClients[0].ID != testClientID {
		t.Error("the clone shares the client registry with its source")
	}
	if cfg.OAuthClients[0].RedirectURIs[0] != testRedirectURI {
		t.Error("the clone shares a redirect URI slice with its source")
	}
	if cfg.AllowedOrigins[0] != testOrigin {
		t.Error("the clone shares the origin allowlist with its source")
	}
}
