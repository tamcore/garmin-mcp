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

// This is the one in-package harness. It replaces the three near-identical per-slice
// harnesses that grew alongside the activity, daily-wellness and stress slices, each
// with its own registrar, its own principal and its own copy of call, callError,
// rawCall and text.
//
// One harness is now possible because register.go carries every tool: a slice no
// longer has to declare a private registration list to prove its tools register, so
// the harness builds the real *Registrar and drives whatever it registered. That also
// makes these tests stricter than the copies were, because a tool now has to survive
// the same start-up tier validation the server performs in production.
//
// The identities and the version below are synthetic. No fixture in this package is a
// recording of a real account.
const (
	harnessPrincipal = "principal-tools-internal-0001"
	harnessVersion   = "0.0.0-internal"
)

// harnessCaller is the principal-scoped caller for the fake service. In production this
// is *auth.Refresher, which attaches the DI bearer token and enforces the Garmin host
// allowlist; here testkit's Doer enforces the origin, so no credential is in play and
// no test can reach the real Garmin service.
type harnessCaller struct {
	doer testkit.Doer
}

func (c harnessCaller) Do(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	if principal == "" {
		return nil, errors.New("harnessCaller: no principal")
	}
	return c.doer.Do(req.WithContext(ctx))
}

// toolHarness drives the registered tools the way a real client does: over an MCP
// session, through the middleware chain, against a scripted fake Garmin service.
type toolHarness struct {
	fake    *testkit.Server
	session *mcp.ClientSession
}

func newToolHarness(t *testing.T, script testkit.Script) toolHarness {
	t.Helper()
	return newToolHarnessWith(t, script, client.Limits{})
}

func newToolHarnessWith(t *testing.T, script testkit.Script, limits client.Limits) toolHarness {
	t.Helper()

	fake := testkit.NewServer(t, script)
	return toolHarness{fake: fake, session: connectHarness(t, newHarnessServer(t, fake, limits))}
}

// connectHarness runs the server and returns a connected client session, registering
// the shutdown that stops both.
func connectHarness(t *testing.T, server *mcpserver.Server) *mcp.ClientSession {
	t.Helper()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.MCPServer().Run(ctx, serverTransport)
	}()

	session, err := mcp.NewClient(&mcp.Implementation{
		Name: "garmin-mcp-internal-test", Version: harnessVersion,
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

// newHarnessServer builds the real registrar over the fake Garmin service.
func newHarnessServer(t *testing.T, fake *testkit.Server, limits client.Limits) *mcpserver.Server {
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
	registrar, err := New(Deps{Client: rc, Caller: harnessCaller{doer: fake.Doer()}})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{harnessPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}
	pol, err := policy.New(policy.Config{
		Mode:             policy.ModeLocal,
		ReadOnlyTools:    ReadOnlyTools(),
		WriteTools:       WriteTools(),
		DestructiveTools: DestructiveTools(),
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	server, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-internal-test", Version: harnessVersion},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{registrar},
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}
	return server
}

// call invokes a tool and requires it to succeed.
func (h toolHarness) call(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()

	result := h.rawCall(t, name, args)
	if result.IsError {
		t.Fatalf("%s returned an error result: %s", name, harnessText(result))
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

// callError invokes a tool and requires an error result, returning its advice.
func (h toolHarness) callError(t *testing.T, name string, args map[string]any) string {
	t.Helper()

	result := h.rawCall(t, name, args)
	if !result.IsError {
		t.Fatalf("%s succeeded, want an error result", name)
	}
	return harnessText(result)
}

// rawCall invokes a tool and returns whatever came back, error result included.
func (h toolHarness) rawCall(
	t *testing.T, name string, args map[string]any,
) *mcp.CallToolResult {
	t.Helper()

	// A nil map marshals to JSON null, which is not an object and which the server
	// refuses. A conformant client that passes no arguments sends an empty object.
	if args == nil {
		args = map[string]any{}
	}
	result, err := h.session.CallTool(t.Context(),
		&mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) transport error = %v", name, err)
	}
	return result
}

// text renders the whole result, structured content included, so a leak test can
// assert over everything that left the process.
func (h toolHarness) text(t *testing.T, name string, args map[string]any) string {
	t.Helper()

	result := h.rawCall(t, name, args)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshalling the %s result: %v", name, err)
	}
	return string(encoded)
}

func harnessText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	return text.String()
}

// number reads a numeric field out of a structured result.
func number(t *testing.T, result map[string]any, key string) float64 {
	t.Helper()

	value, ok := result[key].(float64)
	if !ok {
		t.Fatalf("result[%q] = %#v, want a number", key, result[key])
	}
	return value
}

// object reads a nested object out of a structured result.
func object(t *testing.T, result map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := result[key].(map[string]any)
	if !ok {
		t.Fatalf("result[%q] = %#v, want an object", key, result[key])
	}
	return value
}

// list reads an array out of a structured result.
func list(t *testing.T, result map[string]any, key string) []any {
	t.Helper()

	value, ok := result[key].([]any)
	if !ok {
		t.Fatalf("result[%q] = %#v, want an array", key, result[key])
	}
	return value
}

// entry reads one object element of an array.
func entry(t *testing.T, items []any, index int) map[string]any {
	t.Helper()

	value, ok := items[index].(map[string]any)
	if !ok {
		t.Fatalf("items[%d] = %#v, want an object", index, items[index])
	}
	return value
}

// assertNoRawPayload proves a refusal carries authored advice and no Garmin payload,
// credential or stack trace. A schema refusal quotes the caller's own argument back,
// which is the caller's value and not this server's state, so only material that could
// come from Garmin or from this process is forbidden.
func assertNoRawPayload(t *testing.T, advice string) {
	t.Helper()

	if advice == "" {
		t.Fatal("the refusal carries no advice")
	}
	for _, forbidden := range []string{"Bearer", "goroutine", "http://", "garmin.com"} {
		if strings.Contains(advice, forbidden) {
			t.Errorf("the refusal %q carries %q", advice, forbidden)
		}
	}
}

// newDomainToolServer builds a server carrying one domain's tools and nothing else.
//
// Every domain test file needs the same three steps — a stdio resolver, a policy
// that admits exactly this domain's read-only names plus the built-in, and a server
// wired to this domain's registrar — and writing them out per domain made two
// byte-identical copies that the duplication linter was right to object to. The
// three things that actually differ are parameters.
func newDomainToolServer(
	t *testing.T,
	name string,
	registrations []registration,
	registrar mcpserver.ToolRegistrar,
) *mcpserver.Server {
	t.Helper()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{harnessPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}
	pol, err := policy.New(policy.Config{
		Mode: policy.ModeLocal,
		ReadOnlyTools: append([]string{mcpserver.ServerInfoToolName},
			namesOf(registrations)...),
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	server, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: name, Version: harnessVersion},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{registrar},
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}
	return server
}
