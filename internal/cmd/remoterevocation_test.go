package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// The two synthetic accounts this file authorizes. Neither is a real Garmin
// account, and no request leaves the process.
const (
	aliceEmail  = "alice@example.test"
	bobEmail    = "bob@example.test"
	aliceAccess = "access-token-alice-0001"
	bobAccess   = "access-token-bob-0001"
)

// seededSession is one authorized account with a live MCP session.
type seededSession struct {
	principal string
	token     string
	sessionID string
}

// authorize mints a consent and a token family for one account, exactly as a
// completed authorization would, and returns what a client would hold.
//
// The tokens are written through the store rather than through the browser flow,
// because what is under test is revocation and not authorization: the flow that
// issues them is exercised elsewhere.
func authorize(t *testing.T, remote *remoteDeployment, cfg config.Config,
	email, accessToken string,
) seededSession {
	t.Helper()
	ctx := t.Context()
	clientID := cfg.OAuthClients[0].ID

	principal, err := remote.sqlite.CreatePrincipal(ctx, email)
	if err != nil {
		t.Fatalf("CreatePrincipal(%s): %v", email, err)
	}
	if err := remote.sqlite.GrantConsent(ctx, principal.ID, clientID,
		[]string{remoteScope}); err != nil {
		t.Fatalf("GrantConsent(%s): %v", email, err)
	}
	if _, err := remote.sqlite.IssueTokenFamily(ctx, store.TokenGrant{
		PrincipalID:     principal.ID,
		ClientID:        clientID,
		Scopes:          []string{remoteScope},
		Audience:        cfg.PublicURL,
		AccessToken:     tokenMaterial(accessToken),
		RefreshToken:    tokenMaterial(accessToken + "-refresh"),
		AccessLifetime:  time.Hour,
		RefreshLifetime: 24 * time.Hour,
	}); err != nil {
		t.Fatalf("IssueTokenFamily(%s): %v", email, err)
	}
	return seededSession{principal: principal.ID, token: accessToken}
}

// tokenMaterial is what the store holds for an opaque token.
//
// The store never sees a token: it addresses a row by the digest the authorization
// server derives, so a test that seeds a row has to derive the same digest. Writing
// the token itself would store a row nothing could ever look up.
func tokenMaterial(token string) store.Secret {
	return store.NewSecret(oauthserver.SecretFromString(token).Lookup().Hex())
}

// mcpRequest builds a Streamable HTTP POST with the headers the transport requires.
func mcpRequest(body, token, sessionID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, remotePublicURL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	return req
}

const initializeRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
	`{"protocolVersion":"2025-06-18","capabilities":{},` +
	`"clientInfo":{"name":"test-client","version":"1"}}}`

const listToolsRequest = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`

// openSession performs the initialize handshake and records the assigned session.
func openSession(t *testing.T, remote *remoteDeployment, session seededSession) seededSession {
	t.Helper()

	recorder := httptest.NewRecorder()
	remote.handler.ServeHTTP(recorder, mcpRequest(initializeRequest, session.token, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}
	session.sessionID = recorder.Header().Get("Mcp-Session-Id")
	if session.sessionID == "" {
		t.Fatal("initialize returned no Mcp-Session-Id header")
	}
	return session
}

// status performs one request on an open session and reports the status code.
func status(t *testing.T, remote *remoteDeployment, session seededSession) int {
	t.Helper()

	recorder := httptest.NewRecorder()
	remote.handler.ServeHTTP(recorder, mcpRequest(listToolsRequest, session.token, session.sessionID))
	return recorder.Code
}

// openStream holds a standalone SSE stream open for a session and reports a channel
// that closes when the server ends it.
//
// The stream is the observation that matters. Refusing the next request would be
// satisfied by a token check alone, which is what the deployment already did before
// there was a revocation stream; only a stream that ends proves the session itself
// was terminated.
func openStream(t *testing.T, remote *remoteDeployment, base string, session seededSession) <-chan struct{} {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+remote.endpoints.mcpPath, nil)
	if err != nil {
		t.Fatalf("build the stream request: %v", err)
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+session.token)
	request.Header.Set("Mcp-Session-Id", session.sessionID)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		t.Fatalf("stream status = %d, want 200", response.StatusCode)
	}

	ended := make(chan struct{})
	go func() {
		defer close(ended)
		defer func() { _ = response.Body.Close() }()
		_, _ = io.Copy(io.Discard, response.Body)
	}()
	t.Cleanup(func() { _ = response.Body.Close() })
	return ended
}

// TestRevokingAConsentClosesThatPrincipalsSession is the property the whole
// revocation stream exists for: a withdrawn authorization ends the session that
// holds it, rather than being noticed at the next request — a stream held open
// would otherwise keep delivering — and it ends nobody else's.
func TestRevokingAConsentClosesThatPrincipalsSession(t *testing.T) {
	cfg := remoteConfig(t)
	remote := buildRemote(t, cfg)

	watch, stopWatch := context.WithCancel(t.Context())
	defer stopWatch()
	watchDone := make(chan error, 1)
	go func() { watchDone <- remote.transport.Run(watch) }()

	// The server is closed through Cleanup rather than a defer, because cleanups
	// run last-registered-first: every stream this test opens registers its own
	// close afterwards, so each is shut before the server waits for its
	// connections. A defer would run first and block on the streams it has to
	// outlive.
	server := httptest.NewServer(remote.handler)
	t.Cleanup(server.Close)

	alice := openSession(t, remote, authorize(t, remote, cfg, aliceEmail, aliceAccess))
	bob := openSession(t, remote, authorize(t, remote, cfg, bobEmail, bobAccess))
	if code := status(t, remote, alice); code != http.StatusOK {
		t.Fatalf("alice's session answered %d before any revocation, want 200", code)
	}
	if code := status(t, remote, bob); code != http.StatusOK {
		t.Fatalf("bob's session answered %d before any revocation, want 200", code)
	}
	aliceStream := openStream(t, remote, server.URL, alice)
	bobStream := openStream(t, remote, server.URL, bob)

	if _, err := remote.sqlite.RevokeConsent(t.Context(), alice.principal,
		cfg.OAuthClients[0].ID); err != nil {
		t.Fatalf("RevokeConsent: %v", err)
	}

	select {
	case <-aliceStream:
	case <-time.After(5 * time.Second):
		t.Fatal("alice's stream survived the revocation of her consent")
	}
	select {
	case <-bobStream:
		t.Error("bob's stream was closed by a revocation that named alice")
	case <-time.After(200 * time.Millisecond):
	}

	// And the session is gone rather than merely unusable: the id addresses
	// nothing, which is what an open stream would otherwise have kept alive.
	if code := status(t, remote, alice); code == http.StatusOK {
		t.Error("alice's session still served a request after her consent was revoked")
	}
	if code := status(t, remote, bob); code != http.StatusOK {
		t.Errorf("bob's session answered %d after alice's revocation, want 200", code)
	}

	stopWatch()
	if err := <-watchDone; err != nil {
		t.Fatalf("the revocation watch returned error: %v", err)
	}
}
