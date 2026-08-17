//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// This file exercises the remote Streamable HTTP transport the way a client
// meets it before it holds a token: it discovers the protected resource
// metadata, receives the RFC 6750 challenge, and is refused.
//
// See e2e/remote_setup.go for the synthetic deployment every test here drives.
//
// Completing the authorization code flow needs a Garmin login, which is out of
// scope here. Everything up to the point where a token would be presented is
// not, and that is what these tests cover.

// TestRemoteTransportServesProtectedResourceMetadata covers the first step a
// client takes: the RFC 9728 document is readable without a token, names the
// canonical resource, and advertises the header as the only place a bearer
// token is accepted.
func TestRemoteTransportServesProtectedResourceMetadata(t *testing.T) {
	server := startRemoteServer(t)

	response, err := server.client.Get(server.metadataURL())
	if err != nil {
		t.Fatalf("read the protected resource metadata: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: the document must be readable unauthenticated", response.StatusCode)
	}

	var metadata struct {
		Resource               string   `json:"resource"`
		AuthorizationServers   []string `json:"authorization_servers"`
		ScopesSupported        []string `json:"scopes_supported"`
		BearerMethodsSupported []string `json:"bearer_methods_supported"`
	}
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		t.Fatalf("decode the protected resource metadata: %v", err)
	}

	if metadata.Resource != server.mcpURL {
		t.Errorf("resource = %q, want %q", metadata.Resource, server.mcpURL)
	}
	if len(metadata.AuthorizationServers) == 0 {
		t.Error("authorization_servers is empty; a client cannot start the flow")
	}
	if !contains(metadata.ScopesSupported, remoteScope) {
		t.Errorf("scopes_supported = %v, want it to contain %q", metadata.ScopesSupported, remoteScope)
	}
	if len(metadata.BearerMethodsSupported) != 1 || metadata.BearerMethodsSupported[0] != "header" {
		t.Errorf("bearer_methods_supported = %v, want [header] only", metadata.BearerMethodsSupported)
	}
}

// TestRemoteTransportRefusesAnMCPRequestWithoutABearerToken covers the second
// step: the request is refused, and the challenge points back at the document
// the previous test read. A request that presented no credential must not be
// reported as one that presented an invalid one.
func TestRemoteTransportRefusesAnMCPRequestWithoutABearerToken(t *testing.T) {
	server := startRemoteServer(t)

	response := postInitialize(t, server, nil)
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a request with no bearer token", response.StatusCode)
	}

	challenge := response.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Bearer ") {
		t.Fatalf("WWW-Authenticate = %q, want an RFC 6750 Bearer challenge", challenge)
	}
	if !strings.Contains(challenge, `resource_metadata="`+server.metadataURL()+`"`) {
		t.Errorf("challenge %q does not point at %s", challenge, server.metadataURL())
	}
	if strings.Contains(challenge, "error=") {
		t.Errorf("challenge %q reports an error; a missing token is not an invalid one", challenge)
	}
	if store := response.Header.Get("Cache-Control"); store != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", store)
	}
}

// TestRemoteTransportRefusesABearerTokenOutsideTheHeader covers the rule that a
// token is read from the Authorization header and from nowhere else: a query
// parameter carrying one leaves the request unauthenticated, and the challenge
// is the bare one, because nothing was presented where it counts.
func TestRemoteTransportRefusesABearerTokenOutsideTheHeader(t *testing.T) {
	server := startRemoteServer(t)

	request := newInitializeRequest(t, server.mcpURL+"?access_token=synthetic-e2e-token")
	response, err := server.client.Do(request)
	if err != nil {
		t.Fatalf("send the MCP request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: a query parameter never authenticates", response.StatusCode)
	}
	if challenge := response.Header.Get("WWW-Authenticate"); strings.Contains(challenge, "error=") {
		t.Errorf("challenge %q treats a query parameter as a presented credential", challenge)
	}
}

// TestRemoteTransportReportsAnUnusableBearerTokenAsInvalid is the other half of
// the distinction: a credential was presented in the right place and rejected,
// so the challenge says so and a client knows not to retry the same token.
func TestRemoteTransportReportsAnUnusableBearerTokenAsInvalid(t *testing.T) {
	server := startRemoteServer(t)

	response := postInitialize(t, server, map[string]string{
		"Authorization": "Bearer synthetic-e2e-token",
	})
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an unusable token", response.StatusCode)
	}
	challenge := response.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, `error="invalid_token"`) {
		t.Errorf("challenge %q does not report invalid_token", challenge)
	}
}

// postInitialize sends an MCP initialize call with the given extra headers.
func postInitialize(t *testing.T, server remoteServer, headers map[string]string) *http.Response {
	t.Helper()

	request := newInitializeRequest(t, server.mcpURL)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := server.client.Do(request)
	if err != nil {
		t.Fatalf("send the MCP request: %v", err)
	}
	return response
}

// newInitializeRequest builds a well-formed Streamable HTTP initialize request,
// so a refusal can only be about authorization.
func newInitializeRequest(t *testing.T, url string) *http.Request {
	t.Helper()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2026-07-28","capabilities":{},` +
		`"clientInfo":{"name":"garmin-mcp-e2e","version":"0.0.0"}}}`

	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build the MCP request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2026-07-28")
	return request
}

// contains reports whether values holds want.
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
