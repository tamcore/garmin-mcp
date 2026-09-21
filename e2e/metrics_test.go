//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// This file proves the metrics endpoint over the real built binary. Every layer
// of that feature has its own unit test, and the assembled server can still be
// wired so that none of them ever runs: the tool-call observer is a nil-tolerant
// field on mcpserver.Deps, so a composition root that forgets to assign it
// leaves every per-package test green and every tool call unrecorded. Only a
// scrape of a real process after a real tool call can see that.

// metricsScrapePath mirrors internal/cmd/metricsserve.go's metricsPath. It is
// spelled out because that constant is unexported and this package deliberately
// drives the binary from the outside.
const metricsScrapePath = "/metrics"

// TestMetricsAreServedOnTheirOwnPortOnly is the claim "a separate port" actually
// makes. The positive half alone passes against a build that mounted the
// endpoint on the shared mux, so the negative half is the load-bearing one.
func TestMetricsAreServedOnTheirOwnPortOnly(t *testing.T) {
	address := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	server, token := startDeploymentWithMetrics(t, address)

	callServerInfo(t, server, token)

	body := scrapeMetrics(t, "http://"+address+metricsScrapePath)
	if !strings.Contains(body, "garmin_mcp_tool_calls_total{") {
		t.Fatalf("the scrape carries no tool call:\n%s", body)
	}
	if !strings.Contains(body, "garmin_mcp_build_info{") {
		t.Fatalf("the scrape carries no build identity:\n%s", body)
	}

	// The main listener must not serve it.
	response, err := server.client.Get(server.origin + metricsScrapePath)
	if err != nil {
		t.Fatalf("requesting %s on the MCP listener: %v", metricsScrapePath, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("the MCP listener answered %d for %s, want 404", response.StatusCode, metricsScrapePath)
	}
}

// startDeploymentWithMetrics brings up a deployment whose metrics listener binds
// address, plus a bearer token for one seeded principal. The principal, the
// consent and the code are seeded the way every other authenticated test in this
// package seeds them (see e2e/seed_test.go), so no Garmin login is attempted.
func startDeploymentWithMetrics(t *testing.T, address string) (remoteServer, string) {
	t.Helper()

	verifier, challenge := pkcePair(t)
	var code string
	server := startRemoteServerCustomized(t, "", remoteConfigOptions{
		extraLines: []string{"metrics-address: " + address},
	}, func(dir, origin string) {
		sqlite := openSeedStore(t, dir)
		defer func() { _ = sqlite.Close() }()

		seedClient(t, sqlite)
		params := seedAuthCodeParams{
			principalID: seedPrincipal(t, sqlite, "e2e-metrics@example.test"),
			clientID:    remoteClientID,
			redirectURI: remoteRedirectURI,
			resource:    mcpURLFor(origin),
			scopes:      []string{remoteScope},
			challenge:   challenge,
		}
		seedConsent(t, sqlite, params)
		code = seedAuthCode(t, sqlite, params)
	})

	return server, redeemForToken(t, server, tokenForm(code, remoteRedirectURI, verifier)).AccessToken
}

// callServerInfo drives one real read-only tool call to completion. server_info
// reaches no Garmin endpoint, so it succeeds for a seeded principal that holds
// no Garmin session — which is what makes it the tool this test can assert a
// recorded call on rather than a recorded failure.
func callServerInfo(t *testing.T, server remoteServer, token string) {
	t.Helper()

	body := `{"jsonrpc":"2.0","id":"metrics-1","method":"tools/call","params":` +
		`{"name":"server_info","arguments":{}}}`
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.mcpURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build the tools/call request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set(protocolVersionHeaderName, confirmProtocolVersion)

	response, err := server.client.Do(request)
	if err != nil {
		t.Fatalf("send the tools/call request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("tools/call status = %d, want 200", response.StatusCode)
	}

	envelope, err := newSSEReader(response.Body).next()
	if err != nil {
		t.Fatalf("read the tools/call response: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("tools/call returned a JSON-RPC error: %s", envelope.Error)
	}
	var result struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		t.Fatalf("decode the tools/call result: %v", err)
	}
	if result.IsError {
		t.Fatal("server_info reported a tool error; the call did not complete")
	}
}

// scrapeMetrics reads the exposition over plain HTTP. It cannot reuse the
// deployment's client, which carries that deployment's TLS material.
func scrapeMetrics(t *testing.T, url string) string {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build the scrape request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("scraping %s: %v", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the scrape answered %d, want 200", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the scrape: %v", err)
	}
	return string(body)
}
