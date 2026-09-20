//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// latestProtocolVersion is the MCP protocol this SDK speaks, and the one a
// current client sends in Mcp-Protocol-Version.
const latestProtocolVersion = "2026-07-28"

// TestStatelessDeploymentAdvertisesTheCurrentProtocol pins the regression that
// made this server unreachable from a current client while every layer looked
// healthy in isolation.
//
// The SDK offers protocol 2026-07-28 (SEP-2575) only from a stateless
// transport — mcp/streamable.go's SupportsProtocolVersion gates it on
// Stateless — so a deployment that leaves the transport stateful answers
// server/discover with 2025-11-25 at best. A client that speaks only the newer
// protocol then receives a perfectly valid 200, finds no mutually supported
// version, and reports a connection failure with nothing wrong in any log.
//
// The unit tests cover the setting and its wiring. Only this layer proves what
// actually reaches the wire, which is where the defect lived.
func TestStatelessDeploymentAdvertisesTheCurrentProtocol(t *testing.T) {
	fixture := setUpOAuthFlow(t, 1)
	token := redeemForToken(t, fixture.server,
		tokenForm(fixture.nextCode(t), remoteRedirectURI, fixture.verifier))

	// SEP-2575 carries the client's protocol version in per-request _meta
	// rather than in a handshake, which is the whole point of the stateless
	// discovery this test is about.
	body := strings.NewReader(`{"jsonrpc":"2.0","id":"probe","method":"server/discover",` +
		`"params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"` + latestProtocolVersion + `",` +
		`"io.modelcontextprotocol/clientCapabilities":{},` +
		`"io.modelcontextprotocol/clientInfo":{"name":"e2e-probe","version":"1"}` +
		`}}}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.server.mcpURL, body)
	if err != nil {
		t.Fatalf("building the discover request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", latestProtocolVersion)
	// The SDK's streamable handler routes on this header, the way a current
	// client sends it.
	req.Header.Set("Mcp-Method", "server/discover")

	resp, err := fixture.server.client.Do(req)
	if err != nil {
		t.Fatalf("server/discover: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		t.Fatalf("server/discover: status = %d, want 200; body: %s", resp.StatusCode, detail)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		t.Fatalf("reading the discover response: %v", err)
	}

	advertised := discoverVersions(t, raw)
	if !slices.Contains(advertised, latestProtocolVersion) {
		t.Fatalf("server/discover advertises %v, without %s, so a current client "+
			"cannot negotiate a version at all", advertised, latestProtocolVersion)
	}
}

// discoverVersions reads the version list out of a discover response, which
// arrives as one SSE event when the client accepts text/event-stream.
func discoverVersions(t *testing.T, raw []byte) []string {
	t.Helper()

	payload := string(raw)
	if _, after, found := strings.Cut(payload, "data: "); found {
		payload = after
	}
	var envelope struct {
		Result struct {
			SupportedVersions []string `json:"supportedVersions"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &envelope); err != nil {
		t.Fatalf("the discover response is not JSON: %v", err)
	}
	return envelope.Result.SupportedVersions
}
