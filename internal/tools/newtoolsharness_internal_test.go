package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// This file extends garmincoach_internal_test.go's own pattern — driving one tool
// through the real server before register.go carries it — to the write and
// destructive tiers, which the six courses/data-management tools in this slice both
// need. It is shared across their test files rather than copied six times.

const newToolsPrincipal = "principal-newtools-0001"

// newToolsVersion is a distinct synthetic version string, deliberately not
// the widely-shared "0.0.0-test"/"test" literals other harness files in
// this package already use, so as not to add another site to that shared
// count.
const newToolsVersion = "0.0.0-newtools"

// newToolsCaller is the principal-scoped caller for the fake service. No credential
// is in play: the fake's Doer enforces the origin, so no test can reach real Garmin.
type newToolsCaller struct {
	doer testkit.Doer
}

func (c newToolsCaller) Do(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	if principal == "" {
		return nil, errors.New("newToolsCaller: no principal")
	}
	return c.doer.Do(req.WithContext(ctx))
}

// newToolsService builds the tool service over a scripted fake Garmin service.
func newToolsService(t *testing.T, script testkit.Script) (*service, *testkit.Server) {
	t.Helper()

	fake := testkit.NewServer(t, script)
	rc, err := client.New(client.Config{Hosts: fake.Hosts(protocol.DomainGlobal)})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}
	svc, err := newService(Deps{Client: rc, Caller: newToolsCaller{doer: fake.Doer()}})
	if err != nil {
		t.Fatalf("newService() = %v", err)
	}
	return svc, fake
}

// newToolsRegistration pairs one register function with the tool name it
// registers, so a test can name exactly one tool's tier without register.go.
type newToolsRegistration struct {
	name     string
	register func(*mcpserver.Registry, *service) error
}

// newToolsRegistrar registers exactly the tools a test names, so a tool added in
// this slice can be driven through the real server before register.go carries it.
type newToolsRegistrar struct {
	svc           *service
	registrations []newToolsRegistration
}

func (r newToolsRegistrar) RegisterTools(registry *mcpserver.Registry) error {
	for _, registration := range r.registrations {
		if err := registration.register(registry, r.svc); err != nil {
			return err
		}
	}
	return nil
}

// alwaysGrantedScopes grants every scope a test config lists, standing in for the
// remote bearer-token scope source.
type alwaysGrantedScopes struct {
	scopes []policy.Scope
}

func (g alwaysGrantedScopes) GrantedScopes(context.Context) ([]policy.Scope, error) {
	return g.scopes, nil
}

// autoConfirmer stands in for a user who confirms every destructive call.
type autoConfirmer struct{}

func (autoConfirmer) Confirm(context.Context, policy.ConfirmationRequest) error { return nil }

// newToolsServerConfig configures the single-purpose server one of this file's
// callers builds.
type newToolsServerConfig struct {
	readOnly    []newToolsRegistration
	write       []newToolsRegistration
	destructive []newToolsRegistration
}

// newToolsServer builds a server carrying only the tools cfg names, enabling
// whichever tiers it uses and granting the matching scopes, so a write or
// destructive tool in this slice can be driven exactly the way it will be once
// register.go carries it.
func newToolsServer(t *testing.T, svc *service, cfg newToolsServerConfig) *mcpserver.Server {
	t.Helper()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{newToolsPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}

	policyCfg := policy.Config{
		Mode:              policy.ModeLocal,
		ReadOnlyTools:     append([]string{mcpserver.ServerInfoToolName}, registrationNames(cfg.readOnly)...),
		WriteTools:        registrationNames(cfg.write),
		DestructiveTools:  registrationNames(cfg.destructive),
		EnableWrite:       len(cfg.write) > 0 || len(cfg.destructive) > 0,
		EnableDestructive: len(cfg.destructive) > 0,
	}
	var scopes []policy.Scope
	if len(cfg.write) > 0 {
		scopes = append(scopes, policy.ScopeWrite)
	}
	if len(cfg.destructive) > 0 {
		scopes = append(scopes, policy.ScopeDestructive)
	}
	pol, err := policy.New(policyCfg, alwaysGrantedScopes{scopes: scopes})
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	registrar := newToolsRegistrar{svc: svc, registrations: append(append(
		append([]newToolsRegistration{}, cfg.readOnly...), cfg.write...), cfg.destructive...)}

	server, err := mcpserver.New(mcpserver.Deps{
		Info:                mcpserver.Info{Name: "garmin-mcp-newtools-test", Version: newToolsVersion},
		Policy:              pol,
		Principals:          resolver,
		Registrars:          []mcpserver.ToolRegistrar{registrar},
		Confirmer:           autoConfirmer{},
		ConfirmationTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}
	return server
}

func registrationNames(registrations []newToolsRegistration) []string {
	names := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		names = append(names, registration.name)
	}
	return names
}

// newToolsSession connects an in-memory MCP client to server, registering the
// shutdown that stops both.
func newToolsSession(t *testing.T, server *mcpserver.Server) *mcp.ClientSession {
	t.Helper()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.MCPServer().Run(ctx, serverTransport)
	}()

	session, err := mcp.NewClient(&mcp.Implementation{
		Name: "garmin-mcp-newtools-test-client", Version: newToolsVersion,
	}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connecting the test client: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		<-done
	})
	return session
}

// newToolsCall invokes a tool over session and requires it to succeed.
func newToolsCall(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()

	result := newToolsRawCall(t, session, name, args)
	if result.IsError {
		t.Fatalf("%s returned an error result: %s", name, newToolsResultText(result))
	}
	if result.StructuredContent == nil {
		t.Fatalf("%s returned no structured content", name)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshalling %s structured content: %v", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decoding %s structured content: %v", name, err)
	}
	return out
}

// newToolsCallError invokes a tool over session and requires an error result.
func newToolsCallError(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()

	result := newToolsRawCall(t, session, name, args)
	if !result.IsError {
		t.Fatalf("%s succeeded, want an error result", name)
	}
	return newToolsResultText(result)
}

func newToolsRawCall(
	t *testing.T, session *mcp.ClientSession, name string, args map[string]any,
) *mcp.CallToolResult {
	t.Helper()

	if args == nil {
		args = map[string]any{}
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) transport error = %v", name, err)
	}
	return result
}

func newToolsResultText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	return text.String()
}
