package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// Synthetic values shared by the HTTP transport tests.
const (
	testPublicURL   = "https://mcp.example.test/mcp"
	testBindAddress = "127.0.0.1:8443"
	testOrigin      = "https://client.example.test"
	testResource    = "https://mcp.example.test/mcp"
	whoamiTool      = "whoami"

	tokenAlice = "token-alice"
	tokenBob   = "token-bob"
	tokenOther = "token-alice-other-client"

	principalAlice = "principal-alice"
	principalBob   = "principal-bob"

	clientA = "client-a"
	clientB = "client-b"

	familyAlice = "family-alice"
	scopeRead   = "garmin.read"

	evilOrigin      = "https://evil.example.test"
	cleartextURL    = "http://mcp.example.test/mcp"
	mcpPath         = "/mcp"
	proxyCIDR       = "10.0.0.0/8"
	proxyPeer       = "10.1.2.3:5000"
	proxyPeerIP     = "10.1.2.3"
	forwardedClient = "198.51.100.7"
)

// fakeAuthorizer is an HTTPAuthorizer with a fixed token table.
//
// It reads the Authorization header and nothing else, exactly like the real
// adapter, so a binding test can mint two principals without standing up an
// OAuth server. The RFC 6750 wording is not its business; that is asserted
// against the real adapter instead.
type fakeAuthorizer struct {
	mu     sync.Mutex
	grants map[string]mcpserver.Grant
}

type fakeGrantKey struct{}

func newFakeAuthorizer(t *testing.T) *fakeAuthorizer {
	t.Helper()

	return &fakeAuthorizer{
		grants: map[string]mcpserver.Grant{
			tokenAlice: {
				Principal: mustPrincipalID(t, principalAlice),
				ClientID:  clientA,
				Resource:  testResource,
				Scopes:    []string{scopeRead},
				Family:    familyAlice,
			},
			tokenBob: {
				Principal: mustPrincipalID(t, principalBob),
				ClientID:  clientB,
				Resource:  testResource,
				Scopes:    []string{scopeRead},
				Family:    "family-bob",
			},
			tokenOther: {
				Principal: mustPrincipalID(t, principalAlice),
				ClientID:  clientB,
				Resource:  testResource,
				Scopes:    []string{scopeRead},
				Family:    "family-alice-b",
			},
		},
	}
}

func (a *fakeAuthorizer) lookup(token string) (mcpserver.Grant, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	grant, ok := a.grants[token]
	return grant, ok
}

func (a *fakeAuthorizer) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fields := strings.Fields(r.Header.Get("Authorization"))
			if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
				w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			grant, ok := a.lookup(fields[1])
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="test", error="invalid_token"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), fakeGrantKey{}, grant)))
		})
	}
}

func (a *fakeAuthorizer) Grant(ctx context.Context) (mcpserver.Grant, error) {
	grant, ok := ctx.Value(fakeGrantKey{}).(mcpserver.Grant)
	if !ok {
		return mcpserver.Grant{}, fmt.Errorf("no grant on the context")
	}
	return grant, nil
}

func (a *fakeAuthorizer) ProtectedResourceMetadataHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              testResource,
			"authorization_servers": []string{"https://mcp.example.test"},
		})
	})
}

func mustPrincipalID(t *testing.T, id string) identity.Principal {
	t.Helper()

	principal, err := identity.NewPrincipal(id)
	if err != nil {
		t.Fatalf("NewPrincipal(%q) returned error: %v", id, err)
	}
	return principal
}

// whoamiRegistrar registers a tool that reports the principal its handler saw.
// It is how a test proves which principal reached the handler, rather than which
// one the transport claims to have resolved.
type whoamiRegistrar struct{}

// whoamiInput is empty: nothing a caller sends may influence the answer.
type whoamiInput struct{}

// whoamiOutput carries the principal the handler was given.
type whoamiOutput struct {
	Principal string `json:"principal" jsonschema:"the resolved principal identifier"`
}

func (whoamiRegistrar) RegisterTools(registry *mcpserver.Registry) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ whoamiInput) (
		*mcp.CallToolResult, whoamiOutput, error,
	) {
		principal, err := identity.FromContext(ctx)
		if err != nil {
			return nil, whoamiOutput{}, err
		}
		return nil, whoamiOutput{Principal: principal.ID()}, nil
	}
	return mcpserver.AddTool(registry, mcpserver.ToolSpec{
		Name:        whoamiTool,
		Title:       "Who am I",
		Description: "Reports the principal the server resolved for this request.",
		Category:    testCategory,
		Tier:        policy.TierReadOnly,
		Annotations: mcpserver.Annotations{ReadOnly: true, Idempotent: true, OpenWorld: true},
	}, handler)
}

// httpDeps builds a dependency set whose principal comes from the authorizer.
func httpDeps(t *testing.T, authorizer mcpserver.HTTPAuthorizer) mcpserver.Deps {
	t.Helper()

	resolver, err := identity.NewBearerResolver(mcpserver.PrincipalSource(authorizer))
	if err != nil {
		t.Fatalf("NewBearerResolver returned error: %v", err)
	}
	return mcpserver.Deps{
		Info:   mcpserver.Info{Name: testServerName, Version: testVersion},
		Logger: mustLogger(t, &syncBuffer{}, mcplog.Config{}),
		Policy: mustPolicy(t, policy.Config{
			Mode:          policy.ModeRemote,
			ReadOnlyTools: []string{mcpserver.ServerInfoToolName, whoamiTool},
		}),
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{whoamiRegistrar{}},
	}
}

// testHTTPOptions is a valid option set a test then narrows.
func testHTTPOptions(authorizer mcpserver.HTTPAuthorizer) mcpserver.HTTPOptions {
	return mcpserver.HTTPOptions{
		PublicURL:      testPublicURL,
		BindAddress:    testBindAddress,
		Authorizer:     authorizer,
		AllowedOrigins: []string{testOrigin},
	}
}

func newTestTransport(t *testing.T, opts mcpserver.HTTPOptions) *mcpserver.HTTPTransport {
	t.Helper()

	server := newTestServer(t, httpDeps(t, opts.Authorizer))
	transport, err := mcpserver.NewHTTPTransport(server, opts)
	if err != nil {
		t.Fatalf("NewHTTPTransport returned error: %v", err)
	}
	return transport
}

// newBoundTransport is the common case: a fake authorizer and a valid option set.
func newBoundTransport(t *testing.T) *mcpserver.HTTPTransport {
	t.Helper()

	return newTestTransport(t, testHTTPOptions(newFakeAuthorizer(t)))
}

// initializeBody is a protocol-correct initialize request.
func initializeBody() string {
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},` +
		`"clientInfo":{"name":"test-client","version":"1"}}}`
}

func callToolBody(id int) string {
	return fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":{}}}`,
		id, whoamiTool)
}

// mcpPOST builds a Streamable HTTP POST with the headers the SDK requires.
func mcpPOST(t *testing.T, body, token, sessionID string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, testPublicURL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	return req
}

// initSession performs an initialize POST and returns the assigned session id.
func initSession(t *testing.T, transport *mcpserver.HTTPTransport, token string) string {
	t.Helper()

	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder, mcpPOST(t, initializeBody(), token, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}
	sessionID := recorder.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatalf("initialize returned no Mcp-Session-Id header")
	}
	return sessionID
}
