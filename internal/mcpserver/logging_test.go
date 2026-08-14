package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

// Every call is logged once, centrally, with the coarse fields only.
func TestEveryToolCallIsLoggedOnceWithCoarseFields(t *testing.T) {
	t.Parallel()

	server, _, sink := tieredServer(t, nil)
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      readTool,
		Arguments: map[string]any{textArg: testText},
	}); err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	var toolCalls []map[string]any
	for _, record := range sink.Records(t) {
		if record["msg"] == msgToolCall {
			toolCalls = append(toolCalls, record)
		}
	}
	if len(toolCalls) != 1 {
		t.Fatalf("logged %d tool calls, want exactly 1", len(toolCalls))
	}

	record := toolCalls[0]
	if record["principalId"] != testPrincipalID {
		t.Errorf("principalId = %v, want principal-a", record["principalId"])
	}
	if record["category"] != testCategory {
		t.Errorf("category = %v, want diagnostics", record["category"])
	}
	if record["tier"] != "read-only" {
		t.Errorf("tier = %v, want read-only", record["tier"])
	}
	if record["outcome"] != "ok" {
		t.Errorf("outcome = %v, want ok", record["outcome"])
	}
	if id, _ := record["requestId"].(string); id == "" {
		t.Error("requestId must be set so a log line can be correlated")
	}
	if _, ok := record["tool"]; ok {
		t.Error("the exact tool name must not be logged without the debug policy")
	}
	if strings.Contains(sink.String(), testText) {
		t.Error("the log must not carry tool arguments")
	}
}

func TestExactToolNamesAreLoggedOnlyUnderTheDebugPolicy(t *testing.T) {
	t.Parallel()

	sink := &syncBuffer{}
	server, _, _ := tieredServer(t, func(d *mcpserver.Deps) {
		d.Logger = mustLogger(t, sink, mcplog.Config{DebugToolNames: true})
	})

	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      readTool,
		Arguments: map[string]any{textArg: testText},
	}); err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	if !strings.Contains(sink.String(), readTool) {
		t.Fatalf("the debug policy must emit the exact tool name, got %q", sink.String())
	}
}

// A policy refusal is logged as a denial, at warn, with the policy's own reason.
func TestAPolicyRefusalIsLoggedAsADenial(t *testing.T) {
	t.Parallel()

	server, _, sink := tieredServer(t, nil)
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      writeTool,
		Arguments: map[string]any{textArg: testText},
	}); err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	var found bool
	for _, record := range sink.Records(t) {
		if record["msg"] != msgToolCall {
			continue
		}
		found = true
		if record["outcome"] != "denied" {
			t.Errorf("outcome = %v, want denied", record["outcome"])
		}
		if record["level"] != "WARN" {
			t.Errorf("level = %v, want WARN", record["level"])
		}
		if reason, _ := record["reason"].(string); !strings.Contains(reason, "write") {
			t.Errorf("reason = %q, want it to name the write tier", reason)
		}
	}
	if !found {
		t.Fatal("the refusal was not logged")
	}
}

// A rate-limited call is logged as such, and the limiter runs before the policy
// gate so an abusive caller is throttled even on tools policy would deny.
func TestARateLimitedCallIsLoggedAndPrecedesThePolicyGate(t *testing.T) {
	t.Parallel()

	limiter, err := ratelimit.New(ratelimit.Config{
		ReadPerMinute: 60, ReadBurst: 1, WritePerMinute: 60, WriteBurst: 1, MaxPrincipals: 4,
	}, nil)
	if err != nil {
		t.Fatalf("ratelimit.New returned error: %v", err)
	}

	server, _, sink := tieredServer(t, func(d *mcpserver.Deps) { d.Limiter = limiter })
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	args := map[string]any{textArg: testText}
	// write_thing is refused by policy. It must still consume the write budget,
	// so the second attempt is rate-limited rather than merely denied again.
	for range 2 {
		if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: writeTool, Arguments: args}); err != nil {
			t.Fatalf("CallTool returned error: %v", err)
		}
	}

	outcomes := make([]string, 0, 2)
	for _, record := range sink.Records(t) {
		if record["msg"] == msgToolCall {
			outcome, _ := record["outcome"].(string)
			outcomes = append(outcomes, outcome)
		}
	}
	if len(outcomes) != 2 {
		t.Fatalf("logged outcomes %v, want two records", outcomes)
	}
	if outcomes[0] != "denied" {
		t.Errorf("the first outcome = %q, want denied", outcomes[0])
	}
	if outcomes[1] != "rate-limited" {
		t.Errorf("the second outcome = %q, want rate-limited: the limiter must run before the policy gate",
			outcomes[1])
	}
}

// A method that is not a tool call must not be gated or logged as one.
func TestNonToolMethodsAreNotGatedAsToolCalls(t *testing.T) {
	t.Parallel()

	server, _, sink := tieredServer(t, nil)
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	if _, err := session.ListTools(ctx, &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}
	if err := session.Ping(ctx, nil); err != nil {
		t.Fatalf("Ping returned error: %v", err)
	}

	for _, record := range sink.Records(t) {
		if record["msg"] == msgToolCall {
			t.Fatalf("a non-tool method was logged as a tool call: %v", record)
		}
	}
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()

	var sb strings.Builder
	for _, content := range result.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok {
			t.Fatalf("content type = %T, want *mcp.TextContent", content)
		}
		sb.WriteString(text.Text)
	}
	return sb.String()
}
