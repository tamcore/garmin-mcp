package ratelimit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

// callToolRequest builds the request shape the SDK hands to receiving middleware
// for a tools/call message.
func callToolRequest(tool string) mcp.Request {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool}}
}

// countingHandler records how often the wrapped handler ran.
type countingHandler struct {
	calls int
}

func (h *countingHandler) handle(context.Context, string, mcp.Request) (mcp.Result, error) {
	h.calls++
	return &mcp.CallToolResult{}, nil
}

// recordingObserver captures the limiter results the middleware reports.
type recordingObserver struct {
	results []ratelimit.Result
}

func (o *recordingObserver) RateLimited(_ context.Context, result ratelimit.Result) {
	o.results = append(o.results, result)
}

func readOnlyClassifier(string) ratelimit.Kind { return ratelimit.KindRead }

func writeClassifier(string) ratelimit.Kind { return ratelimit.KindWrite }

func resolvedContext(t *testing.T, id string) context.Context {
	t.Helper()

	return identity.WithPrincipal(context.Background(), mustPrincipal(t, id))
}

func TestMiddlewarePassesAnAllowedCallThrough(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	limiter, err := ratelimit.New(testConfig(), clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	handler := &countingHandler{}
	wrapped := ratelimit.Middleware(limiter, readOnlyClassifier, nil)(handler.handle)

	result, err := wrapped(resolvedContext(t, "principal-a"), "tools/call", callToolRequest("get_activities"))
	if err != nil {
		t.Fatalf("wrapped handler returned error: %v", err)
	}
	if handler.calls != 1 {
		t.Fatalf("handler ran %d times, want 1", handler.calls)
	}
	if callResult, ok := result.(*mcp.CallToolResult); !ok || callResult.IsError {
		t.Fatalf("result = %#v, want a successful CallToolResult", result)
	}
}

// A limited call must come back as a tool-level error result the model can read
// and act on, never as a JSON-RPC transport error.
func TestMiddlewareReturnsAnActionableErrorResultRatherThanATransportError(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	cfg := testConfig()
	cfg.WriteBurst = 1
	limiter, err := ratelimit.New(cfg, clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	handler := &countingHandler{}
	observer := &recordingObserver{}
	wrapped := ratelimit.Middleware(limiter, writeClassifier, observer)(handler.handle)
	ctx := resolvedContext(t, "principal-a")

	if _, err := wrapped(ctx, "tools/call", callToolRequest("add_weigh_in")); err != nil {
		t.Fatalf("the first call returned error: %v", err)
	}

	result, err := wrapped(ctx, "tools/call", callToolRequest("add_weigh_in"))
	if err != nil {
		t.Fatalf("a limited call must not return a transport error, got %v", err)
	}
	if handler.calls != 1 {
		t.Fatalf("handler ran %d times, want 1: the limited call must not reach it", handler.calls)
	}

	callResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("result type = %T, want *mcp.CallToolResult", result)
	}
	if !callResult.IsError {
		t.Fatal("a limited result must set IsError")
	}

	text := strings.ToLower(resultText(t, callResult))
	for _, want := range []string{"rate limit", "write", "retry"} {
		if !strings.Contains(text, want) {
			t.Errorf("result text %q does not mention %q", text, want)
		}
	}
	if len(observer.results) != 1 {
		t.Fatalf("the observer saw %d results, want 1", len(observer.results))
	}
	if observer.results[0].Allowed {
		t.Error("the observer must be told the call was limited")
	}
}

// The middleware must not leak the principal identifier into caller-facing text.
func TestLimitedResultTextCarriesNoPrincipalIdentifier(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	cfg := testConfig()
	cfg.WriteBurst = 1
	limiter, err := ratelimit.New(cfg, clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	handler := &countingHandler{}
	wrapped := ratelimit.Middleware(limiter, writeClassifier, nil)(handler.handle)
	ctx := resolvedContext(t, "principal-secret-id")

	if _, err := wrapped(ctx, "tools/call", callToolRequest("add_weigh_in")); err != nil {
		t.Fatalf("the first call returned error: %v", err)
	}
	result, err := wrapped(ctx, "tools/call", callToolRequest("add_weigh_in"))
	if err != nil {
		t.Fatalf("wrapped handler returned error: %v", err)
	}

	callResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("result type = %T, want *mcp.CallToolResult", result)
	}
	if text := resultText(t, callResult); strings.Contains(text, "principal-secret-id") {
		t.Fatalf("result text %q leaks the principal identifier", text)
	}
}

// Only tools/call is gated. A method the limiter has no budget model for must pass
// through untouched rather than consume a budget.
func TestMiddlewareOnlyGatesToolCalls(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	cfg := testConfig()
	cfg.ReadBurst = 1
	limiter, err := ratelimit.New(cfg, clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	handler := &countingHandler{}
	wrapped := ratelimit.Middleware(limiter, readOnlyClassifier, nil)(handler.handle)
	ctx := resolvedContext(t, "principal-a")

	listRequest := &mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}}
	for range 5 {
		if _, err := wrapped(ctx, "tools/list", listRequest); err != nil {
			t.Fatalf("tools/list returned error: %v", err)
		}
	}
	if handler.calls != 5 {
		t.Fatalf("handler ran %d times, want 5", handler.calls)
	}
	if limiter.TrackedPrincipals() != 0 {
		t.Fatal("a non-tools/call method must not consume a per-principal budget")
	}
}

// A call with no resolved principal is refused, not given an anonymous shared
// budget: a shared bucket would be a cross-principal channel.
func TestMiddlewareRefusesACallWithNoResolvedPrincipal(t *testing.T) {
	t.Parallel()

	limiter, err := ratelimit.New(testConfig(), nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	handler := &countingHandler{}
	wrapped := ratelimit.Middleware(limiter, readOnlyClassifier, nil)(handler.handle)

	result, err := wrapped(context.Background(), "tools/call", callToolRequest("get_activities"))
	if err != nil {
		t.Fatalf("an unresolved principal must not produce a transport error, got %v", err)
	}
	if handler.calls != 0 {
		t.Fatal("the handler must not run without a resolved principal")
	}
	callResult, ok := result.(*mcp.CallToolResult)
	if !ok || !callResult.IsError {
		t.Fatalf("result = %#v, want an error CallToolResult", result)
	}
}

// A nil limiter makes the middleware a transparent pass-through.
func TestMiddlewareWithANilLimiterPassesThrough(t *testing.T) {
	t.Parallel()

	handler := &countingHandler{}
	wrapped := ratelimit.Middleware(nil, writeClassifier, nil)(handler.handle)
	ctx := resolvedContext(t, "principal-a")

	for range 50 {
		if _, err := wrapped(ctx, "tools/call", callToolRequest("add_weigh_in")); err != nil {
			t.Fatalf("wrapped handler returned error: %v", err)
		}
	}
	if handler.calls != 50 {
		t.Fatalf("handler ran %d times, want 50", handler.calls)
	}
}

// A nil classifier must fall back to the cheapest budget rather than skipping the
// limiter, so a wiring mistake cannot disable rate limiting outright.
func TestMiddlewareWithANilClassifierChargesTheReadBudget(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	cfg := testConfig()
	cfg.ReadBurst = 1
	limiter, err := ratelimit.New(cfg, clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	handler := &countingHandler{}
	wrapped := ratelimit.Middleware(limiter, nil, nil)(handler.handle)
	ctx := resolvedContext(t, "principal-a")

	if _, err := wrapped(ctx, "tools/call", callToolRequest("get_activities")); err != nil {
		t.Fatalf("the first call returned error: %v", err)
	}
	result, err := wrapped(ctx, "tools/call", callToolRequest("get_activities"))
	if err != nil {
		t.Fatalf("wrapped handler returned error: %v", err)
	}
	if callResult, ok := result.(*mcp.CallToolResult); !ok || !callResult.IsError {
		t.Fatalf("result = %#v, want the read budget to have been charged", result)
	}
}

func TestErrorResultDescribesTheLimit(t *testing.T) {
	t.Parallel()

	result := ratelimit.ErrorResult(ratelimit.Result{
		Kind:       ratelimit.KindWrite,
		RetryAfter: 2500 * time.Millisecond,
		Reason:     "the write budget for this account is exhausted",
	})

	if !result.IsError {
		t.Fatal("ErrorResult must set IsError")
	}
	text := resultText(t, result)
	if !strings.Contains(text, "the write budget for this account is exhausted") {
		t.Fatalf("result text %q does not carry the reason", text)
	}
	if !strings.Contains(text, "2.5s") && !strings.Contains(text, "3s") {
		t.Fatalf("result text %q does not carry an actionable retry delay", text)
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
