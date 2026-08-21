package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

const (
	testPrincipal    = "principal-tools-0001"
	testDisplayName  = "fake-tester"
	testCalendarDate = "2026-01-31"
	testActivityID   = "987654321"
	testFullName     = "Fake Tester"
	windowStart      = "2026-01-01"
	typeRunning      = "running"
)

// Argument names, named once so a rename shows up in one place.
const (
	argStart        = "start"
	argLimit        = "limit"
	argPage         = "page"
	argPageSize     = "page_size"
	argStartDate    = "start_date"
	argEndDate      = "end_date"
	argActivityType = "activity_type"
	argActivityID   = "activity_id"
	argDate         = "date"
	argUserID       = "user_id"
)

// caller is the principal-scoped caller for the fake service. In production this is
// *auth.Refresher, which attaches the DI bearer token and enforces the Garmin host
// allowlist; here testkit's Doer enforces the origin, so no credential is in play and
// no test can reach the real service.
type caller struct {
	doer testkit.Doer
}

func (c caller) Do(ctx context.Context, principal string, req *http.Request) (*http.Response, error) {
	if principal == "" {
		return nil, errors.New("caller: no principal")
	}
	return c.doer.Do(req.WithContext(ctx))
}

// harness drives the registered tools the way a real client does: over an MCP
// session, through the middleware chain, against a scripted fake Garmin service.
type harness struct {
	fake    *testkit.Server
	session *mcp.ClientSession
}

func newHarness(t *testing.T, script testkit.Script) harness {
	t.Helper()
	return newHarnessWith(t, script, tools.Bounds{}, client.Limits{})
}

func newHarnessWith(
	t *testing.T, script testkit.Script, bounds tools.Bounds, limits client.Limits,
) harness {
	t.Helper()

	fake := testkit.NewServer(t, script)
	return startHarness(t, fake, newRegistrar(t, fake, bounds, limits, nil))
}

// newCatalogHarness is newHarness with an explicit strength catalog, which is how
// a test drives the tools against a fetched catalog instead of the compiled-in one.
func newCatalogHarness(t *testing.T, script testkit.Script, catalog *api.ExerciseCatalog) harness {
	t.Helper()

	fake := testkit.NewServer(t, script)
	return startHarness(t, fake,
		newRegistrar(t, fake, tools.Bounds{}, client.Limits{}, catalog))
}

// startHarness runs one registrar over an in-memory MCP session, using the
// restrictive default policy.
func startHarness(t *testing.T, fake *testkit.Server, registrar *tools.Registrar) harness {
	t.Helper()
	return startHarnessWith(t, fake, newServer(t, registrar), nil)
}

// startHarnessWith is startHarness for a caller that already built the server
// and wants an explicit client option set — which is how newFullVisibilityHarness
// both substitutes its own policy and declares the elicitation capability a
// destructive tool needs before tools/list will show it.
func startHarnessWith(
	t *testing.T, fake *testkit.Server, server *mcpserver.Server, opts *mcp.ClientOptions,
) harness {
	t.Helper()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.MCPServer().Run(ctx, serverTransport)
	}()

	session, err := mcp.NewClient(&mcp.Implementation{
		Name: "garmin-mcp-tools-test", Version: "test",
	}, opts).Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connecting the test client: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		<-done
	})

	return harness{fake: fake, session: session}
}

// elicitationCapableClientOptions declares the capability that lets a session
// confirm a destructive tool, so a client using it sees the destructive tier in
// tools/list rather than having it silently withheld as unconfirmable.
func elicitationCapableClientOptions() *mcp.ClientOptions {
	return &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "accept"}, nil
		},
	}
}

func newRegistrar(
	t *testing.T, fake *testkit.Server, bounds tools.Bounds, limits client.Limits,
	catalog *api.ExerciseCatalog,
) *tools.Registrar {
	t.Helper()

	rc, err := client.New(client.Config{
		Hosts:   fake.Hosts(protocol.DomainGlobal),
		Limits:  limits,
		Sleeper: client.SleeperFunc(func(context.Context, time.Duration) error { return nil }),
		Jitter:  func() float64 { return 0 },
	})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}

	registrar, err := tools.New(tools.Deps{
		Client:          rc,
		Caller:          caller{doer: fake.Doer()},
		Bounds:          bounds,
		ExerciseCatalog: catalog,
	})
	if err != nil {
		t.Fatalf("tools.New() = %v", err)
	}
	return registrar
}

func newServer(t *testing.T, registrar *tools.Registrar) *mcpserver.Server {
	t.Helper()
	return newServerWithPolicy(t, registrar, restrictivePolicy(t))
}

// restrictivePolicy is the default test policy: both higher tiers are disabled.
// Tests that assert a write or destructive call is refused rely on this default.
func restrictivePolicy(t *testing.T) *policy.Policy {
	t.Helper()

	pol, err := policy.New(policy.Config{
		Mode:             policy.ModeLocal,
		ReadOnlyTools:    tools.ReadOnlyTools(),
		WriteTools:       tools.WriteTools(),
		DestructiveTools: tools.DestructiveTools(),
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}
	return pol
}

// fullVisibilityPolicy enables both higher tiers and grants both their scopes,
// so policy.Decide allows every registered tool and tools/list returns the
// whole declared surface. It exists only for tests asserting something about
// that whole surface (a schema shape, an annotation, a name); tests about what
// one deployment shape's caller may see belong in internal/mcpserver instead.
func fullVisibilityPolicy(t *testing.T) *policy.Policy {
	t.Helper()

	pol, err := policy.New(policy.Config{
		Mode:              policy.ModeLocal,
		ReadOnlyTools:     tools.ReadOnlyTools(),
		WriteTools:        tools.WriteTools(),
		DestructiveTools:  tools.DestructiveTools(),
		EnableWrite:       true,
		EnableDestructive: true,
	}, grantedScopes{scopes: []policy.Scope{policy.ScopeWrite, policy.ScopeDestructive}})
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}
	return pol
}

func newServerWithPolicy(
	t *testing.T, registrar *tools.Registrar, pol *policy.Policy,
) *mcpserver.Server {
	t.Helper()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{testPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}

	server, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-test", Version: "0.0.0-test"},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{registrar},
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}
	return server
}

// newFullVisibilityHarness is newHarness but with every tier enabled and
// granted, and a client that declares the elicitation capability, so
// tools/list returns the whole declared surface including the destructive
// tier: a destructive tool tools/list would only ever show as a capability the
// caller could actually confirm (internal/mcpserver's toolsListMiddleware), so
// a client that cannot confirm must not be used here or the "whole surface"
// this harness promises would quietly lose 9 tools. Use it only for tests
// about the surface itself; behavioral tests keep the restrictive default
// newHarness gives them.
func newFullVisibilityHarness(t *testing.T, script testkit.Script) harness {
	t.Helper()

	fake := testkit.NewServer(t, script)
	registrar := newRegistrar(t, fake, tools.Bounds{}, client.Limits{}, nil)
	server := newServerWithPolicy(t, registrar, fullVisibilityPolicy(t))
	return startHarnessWith(t, fake, server, elicitationCapableClientOptions())
}

// call invokes a tool and requires it to succeed.
func (h harness) call(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()

	result := h.rawCall(t, name, args)
	if result.IsError {
		t.Fatalf("%s returned an error result: %s", name, resultText(result))
	}
	return structured(t, name, result)
}

// rawCall invokes a tool and returns whatever came back, error result included.
func (h harness) rawCall(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	// A nil map marshals to JSON null, which is not an object and which the
	// server refuses. A conformant client that passes no arguments sends an empty
	// object, so send that.
	if args == nil {
		args = map[string]any{}
	}

	result, err := h.session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) transport error = %v", name, err)
	}
	return result
}

// callError invokes a tool and requires an error result, returning its text.
func (h harness) callError(t *testing.T, name string, args map[string]any) string {
	t.Helper()

	result := h.rawCall(t, name, args)
	if !result.IsError {
		t.Fatalf("%s succeeded, want an error result", name)
	}
	return resultText(result)
}

func structured(t *testing.T, name string, result *mcp.CallToolResult) map[string]any {
	t.Helper()

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

func resultText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	return text.String()
}

// requests returns the paths the fake service was asked for, in order.
func (h harness) requests() []string {
	recorded := h.fake.Requests()
	paths := make([]string, 0, len(recorded))
	for _, request := range recorded {
		paths = append(paths, request.Path)
	}
	return paths
}

func sleepPath() string { return client.PathDailySleepPrefix + "/" + testDisplayName }

func summaryPath() string { return client.PathUserSummaryPrefix + "/" + testDisplayName }

func activityDetailPath(segment string) string {
	return client.PathActivityPrefix + "/" + testActivityID + "/" + segment
}
