//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// This file proves a load-bearing property of internal/mcpserver/confirm.go
// that nothing else in the suite exercises: at a negotiated protocol version
// before 2026-07-28, a destructive tool's confirmation is sent as an
// elicitation/create request while the tools/call is in flight, and the SDK
// writes that server-initiated request to the stream of the exact request it
// was made from, resolved from the request id carried on the handler's own
// context. If that context were ever detached, the request would instead land
// on the standalone GET stream, where no client can tell which call, or which
// user, is being asked about — and a correct client must refuse rather than
// guess.
//
// The test drives the real built server over real HTTPS with a bare
// http.Client. No second MCP SDK is added to this repository (ADR 0002 pins
// the one that's here); a raw client is also strictly better for this
// property, because it observes the wire directly instead of a library's
// interpretation of it.
//
// delete_weigh_ins is the destructive tool exercised. Its handler
// (internal/tools/weighindelete.go) resolves the principal's stored Garmin
// session before it ever makes an HTTP request, and this test's principal is
// seeded directly into the store (see e2e/seed_test.go) rather than through a
// real login, so no session exists for it. The call therefore fails fast with
// garmin/auth.ErrNoTokens, never reaching the network — which is exactly what
// is wanted here: proof the handler ran past confirmation, without this
// package ever letting the deployment's own outbound traffic leave the
// process. (The deployment also runs behind the suite's usual blackhole
// proxy; belt and braces, since AGENTS.md forbids reaching the public Garmin
// service from any e2e test regardless.)

// confirmProtocolVersion is deliberately before protocolVersionMultiRoundTrip
// (internal/mcpserver/confirm.go), so the server takes the in-flight
// elicitation branch this test targets rather than the input-required retry
// branch. It is the same version e2e/oauthflow_setup.go already established as
// the newest one a stateful deployment completes.
const confirmProtocolVersion = "2025-11-25"

// confirmToolName is the destructive tool this test drives.
const confirmToolName = "delete_weigh_ins"

// startDeploymentWithDestructiveAccess brings up a deployment configured to
// reach the destructive tier at all: both operator gates enabled, and a
// principal holding a bearer token whose granted scopes cover both the write
// and the destructive tier. Everything about the principal, the client and the
// consent is seeded directly (see e2e/seed_test.go's own note on why), so no
// browser and no Garmin login are ever attempted.
func startDeploymentWithDestructiveAccess(t *testing.T) (remoteServer, string) {
	t.Helper()

	verifier, challenge := pkcePair(t)
	var code string
	server := startDestructiveRemoteServer(t, func(dir, origin string) {
		sqlite := openSeedStore(t, dir)
		defer func() { _ = sqlite.Close() }()

		seedClient(t, sqlite)
		principalID := seedPrincipal(t, sqlite, "e2e-confirm@example.test")
		params := seedAuthCodeParams{
			principalID: principalID,
			clientID:    remoteClientID,
			redirectURI: remoteRedirectURI,
			resource:    mcpURLFor(origin),
			scopes:      []string{string(policy.ScopeWrite), string(policy.ScopeDestructive)},
			challenge:   challenge,
		}
		seedConsent(t, sqlite, params)
		code = seedAuthCode(t, sqlite, params)
	})

	success := redeemForToken(t, server, tokenForm(code, remoteRedirectURI, verifier))
	return server, success.AccessToken
}

// startDestructiveRemoteServer is startRemoteServerConfigured with one
// difference: the configuration it writes enables both tool tiers, which the
// shared helper's own configuration deliberately never does (a remote
// deployment defaults to read-only). Everything else — the certificate, the
// master key, the blackhole proxy, the readiness wait — is identical.
func startDestructiveRemoteServer(t *testing.T, seed func(dir, origin string)) remoteServer {
	t.Helper()

	dir := stateDir(t)
	port := freePort(t)
	certPEM := writeTLSMaterial(t, dir)
	origin := fmt.Sprintf("https://127.0.0.1:%d", port)

	writeMasterKey(t, dir)
	configPath := writeDestructiveRemoteConfig(t, dir, port, origin)
	if seed != nil {
		seed(dir, origin)
	}

	server := remoteServer{
		origin:   origin,
		mcpURL:   mcpURLFor(origin),
		client:   trustingClient(t, certPEM),
		stateDir: dir,
	}
	server.stop = launchRemote(t, dir, configPath, "")
	waitForRemote(t, server)
	return server
}

// writeDestructiveRemoteConfig is writeRemoteConfig plus the two operator
// gates a destructive call needs. See internal/config/validate.go: destructive
// enablement requires write enablement, so both are set together.
func writeDestructiveRemoteConfig(t *testing.T, dir string, port int, origin string) string {
	t.Helper()

	resource := origin + "/mcp"
	document := strings.Join([]string{
		"transport: streamable-http",
		fmt.Sprintf("bind-address: 127.0.0.1:%d", port),
		"public-url: " + resource,
		"tls-cert-file: " + filepath.Join(dir, "tls.crt"),
		"tls-key-file: " + filepath.Join(dir, "tls.key"),
		"state-dir: " + dir,
		"master-key-file: " + filepath.Join(dir, "key-v1.json"),
		"database-path: " + filepath.Join(dir, "garmin.db"),
		"enable-write-tools: true",
		"enable-destructive-tools: true",
		"oauth-clients:",
		"  - id: " + remoteClientID,
		"    name: " + remoteClientName,
		"    redirect-uris:",
		"      - " + remoteRedirectURI,
		"    scopes:",
		"      - " + remoteScope,
		"    resources:",
		"      - " + resource,
		"    public: true",
		"",
	}, "\n")

	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte(document))
	return path
}

// rpcEnvelope decodes just enough of a JSON-RPC message to route this test's
// own logic: which method a request names, what a response's id and payload
// are. json.RawMessage on id and result/error defers decoding their shape,
// since a client answering a server request only needs to echo the id back
// verbatim, never to know how the SDK spells it.
type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

// sseReader reads Streamable HTTP's SSE framing off a live response body and
// decodes each event's data payload as one rpcEnvelope. It is deliberately not
// a full SSE client: this deployment configures no event store, so it never
// emits the priming or resumption events real clients must also handle, and a
// destructive-confirmation round trip does not need them either.
type sseReader struct {
	body *bufio.Reader
}

func newSSEReader(body io.Reader) *sseReader {
	return &sseReader{body: bufio.NewReader(body)}
}

// next blocks until one full SSE event's data lines are read and decoded, the
// stream ends, or the request's context is cancelled — cancellation surfaces
// here as a read error from the underlying connection, because the read is
// tied to the same *http.Request whose context bounds it.
func (r *sseReader) next() (rpcEnvelope, error) {
	var data []string
	for {
		line, err := r.body.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed != "" {
			if payload, ok := strings.CutPrefix(trimmed, "data:"); ok {
				data = append(data, strings.TrimPrefix(payload, " "))
			}
			// Any other non-empty line (event:, id:, a bare comment) carries
			// nothing this test reads, and is skipped.
		} else if len(data) > 0 {
			var envelope rpcEnvelope
			joined := strings.Join(data, "\n")
			if decodeErr := json.Unmarshal([]byte(joined), &envelope); decodeErr != nil {
				return rpcEnvelope{}, fmt.Errorf("decode SSE event %q: %w", joined, decodeErr)
			}
			return envelope, nil
		}
		if err != nil {
			return rpcEnvelope{}, err
		}
	}
}

// mcpStreamingClient is a client for requests this test reads incrementally
// while they are still open (both live SSE streams). server.client carries a
// fixed overall Timeout, which would kill a long-lived GET stream long before
// this test is done with it, so this client bounds each request by its own
// context deadline instead, sharing the same trusted Transport.
func mcpStreamingClient(server remoteServer) *http.Client {
	return &http.Client{Transport: server.client.Transport}
}

// openMCPStream issues one Streamable HTTP request and returns it unread,
// alongside an sseReader over its body. method, an empty body, and no
// Mcp-Session-Id together describe the initialize call; every other call this
// test makes supplies all three.
func openMCPStream(
	t *testing.T, ctx context.Context, client *http.Client, server remoteServer,
	method, token, sessionID, body string,
) (*http.Response, *sseReader) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, server.mcpURL, reader)
	if err != nil {
		t.Fatalf("build the %s request: %v", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(protocolVersionHeaderName, confirmProtocolVersion)
	if method == http.MethodGet {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Content-Type", "application/json")
	}
	if sessionID != "" {
		req.Header.Set(sessionIDHeaderName, sessionID)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("send the %s request: %v", method, err)
	}
	return resp, newSSEReader(resp.Body)
}

// protocolVersionHeaderName and sessionIDHeaderName mirror the constants the
// production transport uses (internal/mcpserver/http.go,
// internal/mcpserver/httpsession.go), spelled out here because this file
// deliberately builds requests with the standard library rather than
// importing transport-internal names.
const (
	protocolVersionHeaderName = "MCP-Protocol-Version"
	sessionIDHeaderName       = "Mcp-Session-Id"
)

// postAndDiscard issues a request this test does not need to read
// incrementally (the initialized notification, the elicitation answer) and
// returns the full response; the caller closes its body.
func postAndDiscard(
	t *testing.T, client *http.Client, server remoteServer, token, sessionID, body string,
) *http.Response {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), remoteRequestTimeout)
	defer cancel()
	resp, _ := openMCPStream(t, ctx, client, server, http.MethodPost, token, sessionID, body)
	return resp
}

// initializeConfirmSession performs the initialize call declaring both the
// pre-multi-round-trip protocol version and the elicitation capability —
// without the latter, confirmDestructive refuses the call before ever trying
// to ask — and returns the assigned session id.
func initializeConfirmSession(t *testing.T, client *http.Client, server remoteServer, token string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), remoteRequestTimeout)
	defer cancel()
	body := `{"jsonrpc":"2.0","id":"init","method":"initialize","params":` +
		`{"protocolVersion":"` + confirmProtocolVersion + `","capabilities":{"elicitation":{}},` +
		`"clientInfo":{"name":"garmin-mcp-e2e-confirm","version":"0.0.0"}}}`
	resp, reader := openMCPStream(t, ctx, client, server, http.MethodPost, token, "", body)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("initialize status = %d, want 200 (body %s)", resp.StatusCode, raw)
	}
	sessionID := resp.Header.Get(sessionIDHeaderName)
	if sessionID == "" {
		t.Fatal("initialize returned no Mcp-Session-Id header")
	}
	envelope, err := reader.next()
	if err != nil {
		t.Fatalf("read the initialize response: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("initialize returned a JSON-RPC error: %s", envelope.Error)
	}
	return sessionID
}

// TestDestructiveConfirmationArrivesOnlyOnTheCallersOwnStream is the mutant
// this test catches: confirm (internal/mcpserver/middleware.go) building the
// elicitation's deadline from a context that has lost the handler's own
// request id — context.Background(), for instance, in place of the ctx the
// handler was actually given — would still ask the client, but on the wrong
// stream: the standalone GET stream every session opens, rather than the
// tools/call request that is waiting on the answer. A client cannot attribute
// a request on that stream to any call, or any user, and AGENTS.md requires
// this server to fail closed rather than let a client guess.
//
// The two assertions this test exists for: the confirmation arrives on the
// tools/call POST's own SSE stream (the positive half), and the standalone GET
// stream receives nothing at all while that round trip runs (the negative
// half, and the one a test built against an SDK's own client cannot show,
// since the SDK never exposes the standalone stream as something a caller can
// inspect independently of the call it correlates events to).
func TestDestructiveConfirmationArrivesOnlyOnTheCallersOwnStream(t *testing.T) {
	server, token := startDeploymentWithDestructiveAccess(t)
	client := mcpStreamingClient(server)

	sessionID := initializeConfirmSession(t, client, server, token)
	initializedResp := postAndDiscard(t, client, server, token, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	defer func() { _ = initializedResp.Body.Close() }()
	if initializedResp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(initializedResp.Body)
		t.Fatalf("notifications/initialized status = %d, want 202 (body %s)",
			initializedResp.StatusCode, raw)
	}

	// Open the standalone stream every session carries, and read it
	// continuously in the background: the negative assertion is that nothing
	// this test does ever produces a message here.
	getCtx, cancelGet := context.WithCancel(context.Background())
	defer cancelGet()
	getResp, getReader := openMCPStream(t, getCtx, client, server, http.MethodGet, token, sessionID, "")
	defer func() { _ = getResp.Body.Close() }()
	if getResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(getResp.Body)
		t.Fatalf("standalone GET stream status = %d, want 200 (body %s)", getResp.StatusCode, raw)
	}

	getEvents := make(chan rpcEnvelope, 4)
	getErrs := make(chan error, 1)
	go func() {
		for {
			envelope, err := getReader.next()
			if err != nil {
				getErrs <- err
				return
			}
			getEvents <- envelope
		}
	}()

	// The destructive call itself. Its own SSE stream is read incrementally
	// below, so the elicitation request can be observed before the call's
	// final result exists.
	postCtx, cancelPost := context.WithTimeout(context.Background(),
		mcpserver.DefaultConfirmationTimeout+remoteRequestTimeout)
	defer cancelPost()
	callBody := `{"jsonrpc":"2.0","id":"delete-weigh-ins-1","method":"tools/call","params":` +
		`{"name":"` + confirmToolName + `","arguments":{"date":"2024-01-15"}}}`
	postResp, postReader := openMCPStream(t, postCtx, client, server, http.MethodPost, token, sessionID, callBody)
	defer func() { _ = postResp.Body.Close() }()
	if postResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(postResp.Body)
		t.Fatalf("tools/call status = %d, want 200 (body %s)", postResp.StatusCode, raw)
	}

	// Read the tools/call stream's first event in the background too, and race
	// it against the standalone stream: the correct outcome is that the
	// tools/call channel wins almost immediately. If the confirmation lost its
	// request-id correlation, it goes out on the standalone stream instead,
	// and the tools/call stream then produces nothing at all until the
	// server's own confirmation timeout eventually denies the call — tens of
	// seconds later, and with a message that never even claims to be
	// elicitation/create. Racing the two, rather than waiting on the
	// tools/call stream alone, is what lets this test fail in seconds instead
	// of minutes, and lets the failure name exactly where the request went.
	type read struct {
		envelope rpcEnvelope
		err      error
	}
	postFirst := make(chan read, 1)
	go func() {
		envelope, err := postReader.next()
		postFirst <- read{envelope, err}
	}()

	var elicit rpcEnvelope
	select {
	case r := <-postFirst:
		if r.err != nil {
			t.Fatalf("reading the tools/call POST stream failed before any event arrived: %v", r.err)
		}
		elicit = r.envelope
	case envelope := <-getEvents:
		t.Fatalf("elicitation/create arrived on the standalone GET stream instead of the "+
			"tools/call POST stream (method %q). A client cannot attribute that request to "+
			"this call or its user.", envelope.Method)
	case getErr := <-getErrs:
		t.Fatalf("the standalone GET stream ended unexpectedly while waiting for the "+
			"confirmation: %v", getErr)
	case <-time.After(5 * time.Second):
		t.Fatal("elicitation/create arrived on neither the tools/call POST stream nor the " +
			"standalone GET stream within 5s")
	}
	if elicit.Method != "elicitation/create" {
		t.Fatalf("first event on the tools/call stream: method = %q, want elicitation/create", elicit.Method)
	}

	// The negative half, checked before answering: the standalone stream must
	// still be silent.
	select {
	case envelope := <-getEvents:
		t.Fatalf("the standalone GET stream received %q while the tools/call confirmation "+
			"was outstanding; it must receive nothing at all", envelope.Method)
	case getErr := <-getErrs:
		t.Fatalf("the standalone GET stream ended unexpectedly: %v", getErr)
	case <-time.After(500 * time.Millisecond):
	}

	answerBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"action":"accept","content":{"confirm":true}}}`,
		string(elicit.ID))
	answerResp := postAndDiscard(t, client, server, token, sessionID, answerBody)
	defer func() { _ = answerResp.Body.Close() }()
	if answerResp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(answerResp.Body)
		t.Fatalf("the confirmation answer status = %d, want 202 (body %s)", answerResp.StatusCode, raw)
	}

	result, err := postReader.next()
	if err != nil {
		t.Fatalf("read the tool result from the tools/call stream: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("tools/call returned a top-level JSON-RPC error rather than a tool result: %s", result.Error)
	}
	var callResult struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(result.Result, &callResult); err != nil {
		t.Fatalf("decode the tool result: %v (%s)", err, result.Result)
	}
	// The tool ran past confirmation: that is the point of this test, not
	// whether it can reach Garmin. This principal was seeded with no stored
	// Garmin session at all (see startDeploymentWithDestructiveAccess), so the
	// handler fails fast with garmin/auth.ErrNoTokens before any network
	// request — an error result here is expected, and is itself evidence the
	// handler executed rather than being refused by policy before ever asking.
	if !callResult.IsError {
		t.Error("delete_weigh_ins reported success with no Garmin session ever seeded for this " +
			"principal; want an error result naming the missing session")
	}

	// Final check: the standalone stream received nothing over the whole round
	// trip, from before the elicitation to after the result.
	select {
	case envelope := <-getEvents:
		t.Fatalf("the standalone GET stream received %q by the time the round trip completed",
			envelope.Method)
	default:
	}
}
