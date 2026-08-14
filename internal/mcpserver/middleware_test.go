package mcpserver_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// grantAll is the M2 OAuth seam standing in for a token that carries both tier
// scopes, so the intersection gate can be opened in a test.
type grantAll struct{}

func (grantAll) GrantedScopes(context.Context) ([]policy.Scope, error) {
	return []policy.Scope{policy.ScopeWrite, policy.ScopeDestructive}, nil
}

// probe records what a tool handler saw, so a test can assert the handler either
// ran or did not, and that it received a resolved principal.
type probe struct {
	mu        sync.Mutex
	calls     int
	principal identity.Principal
	resolvErr error
}

func (p *probe) handler(ctx context.Context, _ *mcp.CallToolRequest, _ echoInput) (
	*mcp.CallToolResult, echoOutput, error,
) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.principal, p.resolvErr = identity.FromContext(ctx)
	return nil, echoOutput{Text: "done"}, nil
}

func (p *probe) snapshot() (int, identity.Principal, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.principal, p.resolvErr
}

func spec(name string, tier policy.Tier) mcpserver.ToolSpec {
	annotations := mcpserver.Annotations{OpenWorld: true}
	switch tier {
	case policy.TierReadOnly:
		annotations.ReadOnly = true
		annotations.Idempotent = true
	case policy.TierDestructive:
		annotations.Destructive = true
	case policy.TierWrite:
	}
	return mcpserver.ToolSpec{
		Name:        name,
		Description: "a test tool",
		Category:    testCategory,
		Tier:        tier,
		Annotations: annotations,
	}
}

// tieredServer builds a server with one tool per tier plus the built-in, and
// returns the probes so a test can see which handler ran.
func tieredServer(t *testing.T, mutate func(*mcpserver.Deps)) (
	*mcpserver.Server, map[string]*probe, *syncBuffer,
) {
	t.Helper()

	probes := map[string]*probe{
		readTool:        {},
		writeTool:       {},
		destructiveTool: {},
	}
	tiers := map[string]policy.Tier{
		readTool:        policy.TierReadOnly,
		writeTool:       policy.TierWrite,
		destructiveTool: policy.TierDestructive,
	}

	sink := &syncBuffer{}
	deps := mcpserver.Deps{
		Info:   mcpserver.Info{Name: testServerName, Version: testVersion},
		Logger: mustLogger(t, sink, mcplog.Config{}),
		Policy: mustPolicy(t, policy.Config{
			Mode:             policy.ModeLocal,
			ReadOnlyTools:    []string{mcpserver.ServerInfoToolName, readTool},
			WriteTools:       []string{writeTool},
			DestructiveTools: []string{destructiveTool},
		}),
		Principals: mustResolver(t),
		Registrars: []mcpserver.ToolRegistrar{registrarFunc(func(r *mcpserver.Registry) error {
			for name, tier := range tiers {
				if err := mcpserver.AddTool(r, spec(name, tier), probes[name].handler); err != nil {
					return err
				}
			}
			return nil
		})},
	}
	if mutate != nil {
		mutate(&deps)
	}

	return newTestServer(t, deps), probes, sink
}

// destructiveEnabled opens the intersection gate for both higher tiers.
func destructiveEnabled(t *testing.T) func(*mcpserver.Deps) {
	t.Helper()

	return func(d *mcpserver.Deps) {
		p, err := policy.New(policy.Config{
			Mode:              policy.ModeLocal,
			ReadOnlyTools:     []string{mcpserver.ServerInfoToolName, readTool},
			WriteTools:        []string{writeTool},
			DestructiveTools:  []string{destructiveTool},
			EnableWrite:       true,
			EnableDestructive: true,
		}, grantAll{})
		if err != nil {
			t.Fatalf("policy.New returned error: %v", err)
		}
		d.Policy = p
	}
}

// A handler must see the principal the server resolved, and must see it through
// the context rather than through any argument.
func TestHandlerReceivesTheResolvedPrincipal(t *testing.T) {
	t.Parallel()

	server, probes, _ := tieredServer(t, nil)
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      readTool,
		Arguments: map[string]any{textArg: testText},
	}); err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	calls, principal, resolveErr := probes[readTool].snapshot()
	if calls != 1 {
		t.Fatalf("handler ran %d times, want 1", calls)
	}
	if resolveErr != nil {
		t.Fatalf("the handler could not read a principal: %v", resolveErr)
	}
	if principal.ID() != testPrincipalID {
		t.Fatalf("principal = %q, want principal-a", principal.ID())
	}
}

// The tool-argument rule, proved through the whole server: arguments that name a
// user, an email, or a token path must not change the resolved principal.
func TestToolArgumentsCannotOverrideThePrincipalThroughTheServer(t *testing.T) {
	t.Parallel()

	server, probes, _ := tieredServer(t, nil)
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: readTool,
		Arguments: map[string]any{
			"text":       testText,
			"user_id":    "attacker-principal",
			"email":      "attacker@example.com",
			"token_path": "/tmp/attacker/garmin_tokens.json",
		},
	}); err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	// Two outcomes are acceptable and both prove the rule. The SDK's inferred
	// schema may reject the unknown properties outright, in which case the handler
	// never runs; or the call proceeds, in which case the principal must still be
	// the one bound at start-up. What must never happen is the attacker's value
	// reaching the handler.
	calls, principal, resolveErr := probes[readTool].snapshot()
	if calls == 0 {
		if principal.IsValid() {
			t.Fatalf("the handler did not run yet a principal %q was recorded", principal.ID())
		}
		return
	}
	if resolveErr != nil {
		t.Fatalf("the handler could not read a principal: %v", resolveErr)
	}
	if principal.ID() != testPrincipalID {
		t.Fatalf("principal = %q, want the start-up bound principal-a", principal.ID())
	}
}

// The write tier is refused today because no scope is granted anywhere, and the
// refusal must reach the caller as a readable tool error.
func TestWriteToolIsRefusedWithNoScopesGranted(t *testing.T) {
	t.Parallel()

	server, probes, _ := tieredServer(t, nil)
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      writeTool,
		Arguments: map[string]any{textArg: testText},
	})
	if err != nil {
		t.Fatalf("a policy refusal must not be a transport error, got %v", err)
	}
	if !result.IsError {
		t.Fatal("the write tool must be refused")
	}
	if calls, _, _ := probes[writeTool].snapshot(); calls != 0 {
		t.Fatalf("the handler ran %d times despite the refusal", calls)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "write") {
		t.Errorf("refusal %q does not name the tier", text)
	}
	if strings.Contains(text, writeTool) {
		t.Errorf("refusal %q echoes the tool name", text)
	}
}
