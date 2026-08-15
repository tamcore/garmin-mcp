package config

import (
	"errors"
	"strings"
	"testing"
)

// The registry is the one setting that is a list of records rather than a scalar,
// so it has its own loader and its own tests: a configuration-file list, the JSON
// document a container supplies through the environment, and the refusal that
// keeps a half-read registration from starting a deployment.

// TestLoadReadsTheRegistryFromTheEnvironment covers the container spelling: a JSON
// array in one variable, so a deployment can register a client without mounting a
// configuration file.
func TestLoadReadsTheRegistryFromTheEnvironment(t *testing.T) {
	t.Setenv("GARMIN_MCP_OAUTH_CLIENTS", `[{"id":"`+testClientID+`",`+
		`"name":"`+testClientName+`",`+
		`"redirect-uris":["`+testRedirectURI+`"],`+
		`"scopes":["`+testScope+`"],`+
		`"resources":["`+testResource+`"],`+
		`"public":true}]`)

	cfg, err := Load(LoadOptions{ConfigFile: writeConfigFile(t, strings.Join([]string{
		"transport: streamable-http",
		"bind-address: 127.0.0.1:9001",
		"public-url: " + testResource,
		"allow-insecure-http: false",
		"database-path: " + databasePath,
		"master-key-file: /run/secrets/master.key",
	}, "\n")+"\n")})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertOneRegisteredClient(t, cfg)
}

// TestLoadRejectsAnUnreadableRegistry keeps a half-read registry from starting a
// deployment with a client that lost its redirect URIs.
func TestLoadRejectsAnUnreadableRegistry(t *testing.T) {
	t.Setenv("GARMIN_MCP_OAUTH_CLIENTS", "not-a-json-array")

	_, err := Load(LoadOptions{})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("error %v does not match ErrInvalidConfig", err)
	}
}
