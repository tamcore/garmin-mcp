package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// panicTool is the tool whose handler panics.
const panicTool = "panicking_tool"

// TestAPanickingToolIsContainedAndTheServerKeepsServing is the availability
// property the whole receiving chain now rests on.
//
// The SDK dispatches every tool handler from a goroutine spawned off the
// connection reader rather than the one net/http serves on, so net/http's
// per-connection recover never sees a panic there, and the SDK has no recover() of
// its own. Without a barrier one panicking call ends the process and every other
// principal's session with it — on a server that is multi-tenant by design.
//
// The second call is the real assertion. A test that only checked the first call's
// result would pass against a build that crashed a moment later.
func TestAPanickingToolIsContainedAndTheServerKeepsServing(t *testing.T) {
	t.Parallel()

	var readProbe probe
	sink := &syncBuffer{}
	deps := mcpserver.Deps{
		Info:   mcpserver.Info{Name: testServerName, Version: testVersion},
		Logger: mustLogger(t, sink, mcplog.Config{}),
		Policy: mustPolicy(t, policy.Config{
			Mode:          policy.ModeLocal,
			ReadOnlyTools: []string{mcpserver.ServerInfoToolName, panicTool, readTool},
		}),
		Principals: mustResolver(t),
		Registrars: []mcpserver.ToolRegistrar{registrarFunc(func(r *mcpserver.Registry) error {
			panicking := func(context.Context, *mcp.CallToolRequest, echoInput) (
				*mcp.CallToolResult, echoOutput, error,
			) {
				panic("a dependency wrote into a map it does not own")
			}
			if err := mcpserver.AddTool(r, spec(panicTool, policy.TierReadOnly), panicking); err != nil {
				return err
			}
			return mcpserver.AddTool(r, spec(readTool, policy.TierReadOnly), readProbe.handler)
		})},
	}

	server := newTestServer(t, deps)
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      panicTool,
		Arguments: map[string]any{textArg: testText},
	})
	if err != nil {
		t.Fatalf("a contained panic must reach the caller as a tool error, not a "+
			"transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("a panicking tool reported success")
	}

	// The refusal must not carry the panic value: it is arbitrary data that may
	// have come from a Garmin response or the caller's own arguments.
	text := resultText(t, result)
	if strings.Contains(text, "map it does not own") {
		t.Errorf("the refusal echoes the panic value: %q", text)
	}

	// And the session is still usable, which is the property that matters.
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      readTool,
		Arguments: map[string]any{textArg: testText},
	}); err != nil {
		t.Fatalf("the session died with the panicking call: %v", err)
	}
	if calls, _, _ := readProbe.snapshot(); calls != 1 {
		t.Fatalf("the surviving tool ran %d times, want 1", calls)
	}

	// The log records the failure without the panic value in it.
	logged := sink.String()
	if !strings.Contains(logged, "panicked") {
		t.Errorf("the contained panic was not reported: %q", logged)
	}
	if strings.Contains(logged, "map it does not own") {
		t.Errorf("the log carries the panic value: %q", logged)
	}
}
