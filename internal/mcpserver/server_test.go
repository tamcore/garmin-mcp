package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

func TestNewRequiresItsDependencies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		apply func(*mcpserver.Deps)
		want  error
	}{
		{"no name", func(d *mcpserver.Deps) { d.Info.Name = "" }, mcpserver.ErrInvalidInfo},
		{"no version", func(d *mcpserver.Deps) { d.Info.Version = "" }, mcpserver.ErrInvalidInfo},
		{"no policy", func(d *mcpserver.Deps) { d.Policy = nil }, mcpserver.ErrMissingDependency},
		{"no principal resolver", func(d *mcpserver.Deps) { d.Principals = nil }, mcpserver.ErrMissingDependency},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			deps := testDeps(t)
			tc.apply(&deps)
			if _, err := mcpserver.New(deps); !errors.Is(err, tc.want) {
				t.Fatalf("New error = %v, want %v", err, tc.want)
			}
		})
	}
}

// A nil logger and a nil limiter are both valid: the logger becomes a no-op and
// the limiter passes through. Neither may make the server reach for a global.
func TestNewAcceptsANilLoggerAndNilLimiter(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.Logger = nil
	deps.Limiter = nil

	server, err := mcpserver.New(deps)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: mcpserver.ServerInfoToolName}); err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
}

// No Garmin tool belongs to this slice. Exactly one built-in tool is registered.
func TestOnlyTheBuiltInToolIsRegistered(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, testDeps(t))

	names := server.ToolNames()
	if !slices.Equal(names, []string{mcpserver.ServerInfoToolName}) {
		t.Fatalf("ToolNames() = %v, want only %q", names, mcpserver.ServerInfoToolName)
	}
}

func TestServerInfoToolIsCallableEndToEnd(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, testDeps(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: mcpserver.ServerInfoToolName})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("server_info returned an error result: %v", result.Content)
	}

	var info mcpserver.ServerInfo
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshaling structured content returned error: %v", err)
	}
	if err := json.Unmarshal(structured, &info); err != nil {
		t.Fatalf("unmarshaling %s returned error: %v", structured, err)
	}

	if info.Name != testServerName {
		t.Errorf("Name = %q, want garmin-mcp-test", info.Name)
	}
	if info.Version != testVersion {
		t.Errorf("Version = %q, want 0.0.0-test", info.Version)
	}
	if info.ProtocolVersion != mcpserver.ProtocolVersion {
		t.Errorf("ProtocolVersion = %q, want %q", info.ProtocolVersion, mcpserver.ProtocolVersion)
	}
	if info.ToolCount != 1 {
		t.Errorf("ToolCount = %d, want 1", info.ToolCount)
	}
	if info.Mode != "local" {
		t.Errorf("Mode = %q, want local", info.Mode)
	}
}

// server_info must not disclose anything about the account: no principal, no
// email, no Garmin data.
func TestServerInfoDisclosesNoAccountData(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, testDeps(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: mcpserver.ServerInfoToolName})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	rendered, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshaling the result returned error: %v", err)
	}
	if strings.Contains(string(rendered), testPrincipalID) {
		t.Fatalf("server_info result %s discloses the principal identifier", rendered)
	}
}

// The tier lists are validated against what was actually registered, so a typo
// fails at construction rather than at the first call.
func TestNewFailsOnATierListTypo(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.Policy = mustPolicy(t, policy.Config{
		Mode:          policy.ModeLocal,
		ReadOnlyTools: []string{"server_infoo"},
	})

	_, err := mcpserver.New(deps)
	if !errors.Is(err, policy.ErrToolNotRegistered) {
		t.Fatalf("New error = %v, want policy.ErrToolNotRegistered", err)
	}
}

func TestNewFailsWhenARegisteredToolHasNoTier(t *testing.T) {
	t.Parallel()

	deps := testDeps(t, registrarFunc(func(r *mcpserver.Registry) error {
		return mcpserver.AddTool(r, readOnlySpec(echoTool), echoHandler)
	}))

	_, err := mcpserver.New(deps)
	if !errors.Is(err, policy.ErrToolWithoutTier) {
		t.Fatalf("New error = %v, want policy.ErrToolWithoutTier", err)
	}
}

// A registrar failure must abort construction: a partially registered tool set is
// not a server anyone should run.
func TestNewPropagatesARegistrarFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("registrar exploded")
	deps := testDeps(t, registrarFunc(func(*mcpserver.Registry) error { return sentinel }))

	_, err := mcpserver.New(deps)
	if !errors.Is(err, sentinel) {
		t.Fatalf("New error = %v, want it to wrap the registrar's error", err)
	}
}

func TestNilRegistrarEntryIsRejected(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.Registrars = []mcpserver.ToolRegistrar{nil}

	if _, err := mcpserver.New(deps); !errors.Is(err, mcpserver.ErrMissingDependency) {
		t.Fatalf("New error = %v, want ErrMissingDependency", err)
	}
}

func TestStartupIsLogged(t *testing.T) {
	t.Parallel()

	sink := &syncBuffer{}
	deps := testDeps(t)
	deps.Logger = mustLogger(t, sink, mcplog.Config{})

	newTestServer(t, deps)

	records := sink.Records(t)
	if len(records) == 0 {
		t.Fatal("construction emitted no log record")
	}
	startup := records[0]
	if startup["phase"] != "startup" {
		t.Fatalf("phase = %v, want startup", startup["phase"])
	}
	if startup["protocolVersion"] != mcpserver.ProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %q", startup["protocolVersion"], mcpserver.ProtocolVersion)
	}
	if startup["toolCount"] != float64(1) {
		t.Fatalf("toolCount = %v, want 1", startup["toolCount"])
	}
}

// The MCP logging capability is deprecated by SEP-2577 and this server must not
// advertise it; structured logging is mcplog's job.
func TestServerDoesNotAdvertiseTheDeprecatedLoggingCapability(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, testDeps(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	capabilities := session.InitializeResult().Capabilities
	// SA1019 is expected here: the assertion is that the deprecated
	// capability is absent, which cannot be written without naming it.
	if capabilities.Logging != nil { //nolint:staticcheck // deliberate deprecated-field check
		t.Fatal("the server must not advertise the deprecated logging capability")
	}
	if capabilities.Tools == nil {
		t.Fatal("the server must advertise the tools capability")
	}
}

// The injected limiter must actually be in the chain, keyed on the resolved
// principal, and must refuse with a tool-level error rather than a transport one.
func TestInjectedLimiterIsInTheChain(t *testing.T) {
	t.Parallel()

	limiter, err := ratelimit.New(ratelimit.Config{
		ReadPerMinute: 60, ReadBurst: 1, WritePerMinute: 60, WriteBurst: 1, MaxPrincipals: 4,
	}, nil)
	if err != nil {
		t.Fatalf("ratelimit.New returned error: %v", err)
	}

	deps := testDeps(t)
	deps.Limiter = limiter
	server := newTestServer(t, deps)

	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	first, err := session.CallTool(ctx, &mcp.CallToolParams{Name: mcpserver.ServerInfoToolName})
	if err != nil {
		t.Fatalf("the first CallTool returned error: %v", err)
	}
	if first.IsError {
		t.Fatalf("the first call was refused: %v", first.Content)
	}

	second, err := session.CallTool(ctx, &mcp.CallToolParams{Name: mcpserver.ServerInfoToolName})
	if err != nil {
		t.Fatalf("the limited CallTool must not be a transport error, got %v", err)
	}
	if !second.IsError {
		t.Fatal("the second call must be refused by the injected limiter")
	}
	if limiter.TrackedPrincipals() != 1 {
		t.Fatalf("TrackedPrincipals() = %d, want 1: the limiter must be keyed on the resolved principal",
			limiter.TrackedPrincipals())
	}
}
