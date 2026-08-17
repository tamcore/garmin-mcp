package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The intersection gate opened, but the client advertises no elicitation
// capability. The destructive tool must refuse rather than proceed.
func TestDestructiveToolFailsClosedWhenTheClientCannotBeAsked(t *testing.T) {
	t.Parallel()

	server, probes, _ := tieredServer(t, destructiveEnabled(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      destructiveTool,
		Arguments: map[string]any{textArg: testText},
	})
	if err != nil {
		t.Fatalf("the refusal must not be a transport error, got %v", err)
	}
	if !result.IsError {
		t.Fatal("a destructive tool must be refused when confirmation cannot be obtained")
	}
	if calls, _, _ := probes[destructiveTool].snapshot(); calls != 0 {
		t.Fatalf("the destructive handler ran %d times without confirmation", calls)
	}
	if text := resultText(t, result); !strings.Contains(text, "unsupported") {
		t.Fatalf("refusal %q does not name the reason", text)
	}
}

func TestDestructiveToolIsRefusedWhenTheUserDeclines(t *testing.T) {
	t.Parallel()

	server, probes, _ := tieredServer(t, destructiveEnabled(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "decline"}, nil
		},
	})

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      destructiveTool,
		Arguments: map[string]any{textArg: testText},
	})
	if err != nil {
		t.Fatalf("the refusal must not be a transport error, got %v", err)
	}
	if !result.IsError {
		t.Fatal("a declined destructive tool must be refused")
	}
	if calls, _, _ := probes[destructiveTool].snapshot(); calls != 0 {
		t.Fatalf("the destructive handler ran %d times after a decline", calls)
	}
	if text := resultText(t, result); !strings.Contains(text, "declined") {
		t.Fatalf("refusal %q does not name the reason", text)
	}
}

// A dismissal without an explicit choice is not consent either.
func TestDestructiveToolIsRefusedWhenTheUserDismisses(t *testing.T) {
	t.Parallel()

	server, probes, _ := tieredServer(t, destructiveEnabled(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "cancel"}, nil
		},
	})

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      destructiveTool,
		Arguments: map[string]any{textArg: testText},
	})
	if err != nil {
		t.Fatalf("the refusal must not be a transport error, got %v", err)
	}
	if !result.IsError {
		t.Fatal("a dismissed destructive tool must be refused")
	}
	if calls, _, _ := probes[destructiveTool].snapshot(); calls != 0 {
		t.Fatalf("the destructive handler ran %d times after a dismissal", calls)
	}
}

// An accepted confirmation is the only path through.
func TestDestructiveToolRunsOnlyAfterAnAcceptedConfirmation(t *testing.T) {
	t.Parallel()

	server, probes, _ := tieredServer(t, destructiveEnabled(t))
	ctx := context.Background()

	var asked int
	session := connectClient(t, ctx, server, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked++
			if req.Params.Message == "" {
				t.Error("the elicitation must tell the user what it is confirming")
			}
			return &mcp.ElicitResult{Action: actionAccept, Content: map[string]any{fieldConfirm: true}}, nil
		},
	})

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      destructiveTool,
		Arguments: map[string]any{textArg: testText},
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("a confirmed destructive tool must run: %v", result.Content)
	}
	if asked != 1 {
		t.Fatalf("the client was asked %d times, want 1", asked)
	}
	if calls, _, _ := probes[destructiveTool].snapshot(); calls != 1 {
		t.Fatalf("the destructive handler ran %d times, want 1", calls)
	}
}

// A write tool must not be gated on confirmation: only the destructive tier is.
func TestWriteToolDoesNotRequireConfirmation(t *testing.T) {
	t.Parallel()

	server, probes, _ := tieredServer(t, destructiveEnabled(t))
	ctx := context.Background()

	asked := 0
	session := connectClient(t, ctx, server, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked++
			return &mcp.ElicitResult{Action: actionAccept, Content: map[string]any{fieldConfirm: true}}, nil
		},
	})

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      writeTool,
		Arguments: map[string]any{textArg: testText},
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("the write tool was refused: %v", result.Content)
	}
	if asked != 0 {
		t.Fatalf("the client was asked to confirm a write %d times, want 0", asked)
	}
	if calls, _, _ := probes[writeTool].snapshot(); calls != 1 {
		t.Fatalf("the write handler ran %d times, want 1", calls)
	}
}

// TestAnAcceptedFormWithoutATrueConfirmIsRefused closes a fail-open in the
// destructive gate.
//
// interpretElicitResult refused only an explicit confirm:false. An accepted form
// that omitted the field, or carried something that was not a boolean, fell through
// to consent — so a client answering {"confirm":"false"} had its destructive
// operation executed, and so did one that accepted the prompt carrying nothing at
// all. AGENTS.md states destructive tools fail closed when confirmation cannot be
// obtained, and an answer this server cannot read as a yes is not a yes.
func TestAnAcceptedFormWithoutATrueConfirmIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		content map[string]any
	}{
		{"the field is absent", map[string]any{}},
		{"the content is nil", nil},
		{"the value is the string false", map[string]any{fieldConfirm: "false"}},
		{"the value is the string true", map[string]any{fieldConfirm: "true"}},
		{"the value is null", map[string]any{fieldConfirm: nil}},
		{"the value is a number", map[string]any{fieldConfirm: 0}},
		{"the value is false", map[string]any{fieldConfirm: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server, probes, _ := tieredServer(t, destructiveEnabled(t))
			ctx := context.Background()
			session := connectClient(t, ctx, server, &mcp.ClientOptions{
				ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					return &mcp.ElicitResult{Action: actionAccept, Content: tc.content}, nil
				},
			})

			result, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      destructiveTool,
				Arguments: map[string]any{textArg: testText},
			})

			// The answer is refused either by the SDK validating it against the
			// requested schema, which surfaces as a transport error, or by this
			// server reading it as a refusal, which surfaces as an error result.
			// Which one fires depends on the shape of the bad answer; what must
			// hold for every one of them is that the tool did not run.
			if err == nil && !result.IsError {
				t.Error("the destructive tool reported success without an " +
					"affirmative confirmation")
			}
			if calls, _, _ := probes[destructiveTool].snapshot(); calls != 0 {
				t.Errorf("the handler ran %d times without an affirmative confirmation", calls)
			}
		})
	}
}

// TestTheConfirmationSchemaRequiresTheField makes the contract explicit: a client
// cannot satisfy the prompt by accepting it and sending nothing.
func TestTheConfirmationSchemaRequiresTheField(t *testing.T) {
	t.Parallel()

	var asked *mcp.ElicitRequest
	server, _, _ := tieredServer(t, destructiveEnabled(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked = req
			return &mcp.ElicitResult{Action: actionAccept, Content: map[string]any{fieldConfirm: true}}, nil
		},
	})
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      destructiveTool,
		Arguments: map[string]any{textArg: testText},
	}); err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	if asked == nil || asked.Params == nil {
		t.Fatal("no elicitation was requested")
	}
	schema, ok := asked.Params.RequestedSchema.(map[string]any)
	if !ok {
		t.Fatalf("the requested schema is %T, want an object", asked.Params.RequestedSchema)
	}
	required, _ := schema["required"].([]any)
	found := false
	for _, name := range required {
		if text, _ := name.(string); text == fieldConfirm {
			found = true
		}
	}
	if !found {
		t.Errorf("the confirmation schema does not require %q: %v", fieldConfirm, schema["required"])
	}
}
