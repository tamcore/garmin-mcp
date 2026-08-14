package mcpserver_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// echoInput and echoOutput are the typed shapes a registered test tool uses, so
// the SDK's generic AddTool has real schemas to infer.
type echoInput struct {
	Text string `json:"text" jsonschema:"the text to echo"`
}

type echoOutput struct {
	Text string `json:"text"`
}

func echoHandler(_ context.Context, _ *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, echoOutput, error) {
	return nil, echoOutput(in), nil
}

// registrarFunc adapts a function to mcpserver.ToolRegistrar, which is the
// interface the later Garmin tool slice targets.
type registrarFunc func(*mcpserver.Registry) error

func (f registrarFunc) RegisterTools(r *mcpserver.Registry) error { return f(r) }

func readOnlySpec(name string) mcpserver.ToolSpec {
	return mcpserver.ToolSpec{
		Name:        name,
		Title:       "Echo",
		Description: "echo the given text",
		Tier:        policy.TierReadOnly,
		Category:    testCategory,
		Annotations: mcpserver.Annotations{ReadOnly: true, Idempotent: true, OpenWorld: true},
	}
}

// depsWithEcho registers one extra read-only tool and widens the policy to match,
// so start-up validation is satisfied.
func depsWithEcho(t *testing.T) mcpserver.Deps {
	t.Helper()

	deps := testDeps(t, registrarFunc(func(r *mcpserver.Registry) error {
		return mcpserver.AddTool(r, readOnlySpec(echoTool), echoHandler)
	}))
	deps.Policy = mustPolicy(t, policy.Config{
		Mode:          policy.ModeLocal,
		ReadOnlyTools: []string{mcpserver.ServerInfoToolName, echoTool},
	})
	return deps
}

func TestAddToolRecordsTheSpec(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, depsWithEcho(t))

	names := server.ToolNames()
	if !slices.Contains(names, echoTool) {
		t.Fatalf("ToolNames() = %v, want it to contain echo", names)
	}

	spec, ok := server.Registry().Spec(echoTool)
	if !ok {
		t.Fatal("Spec(echo) reported false")
	}
	if spec.Category != testCategory {
		t.Fatalf("Category = %q, want diagnostics", spec.Category)
	}
	if spec.Tier != policy.TierReadOnly {
		t.Fatalf("Tier = %v, want read-only", spec.Tier)
	}
}

func TestAddToolRejectsADuplicateName(t *testing.T) {
	t.Parallel()

	deps := testDeps(t, registrarFunc(func(r *mcpserver.Registry) error {
		if err := mcpserver.AddTool(r, readOnlySpec(echoTool), echoHandler); err != nil {
			return err
		}
		return mcpserver.AddTool(r, readOnlySpec(echoTool), echoHandler)
	}))
	deps.Policy = mustPolicy(t, policy.Config{
		Mode:          policy.ModeLocal,
		ReadOnlyTools: []string{mcpserver.ServerInfoToolName, echoTool},
	})

	_, err := mcpserver.New(deps)
	if !errors.Is(err, mcpserver.ErrDuplicateTool) {
		t.Fatalf("New error = %v, want ErrDuplicateTool", err)
	}
}

func TestAddToolRejectsAnInvalidSpec(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		apply func(*mcpserver.ToolSpec)
	}{
		{"empty name", func(s *mcpserver.ToolSpec) { s.Name = "" }},
		{"padded name", func(s *mcpserver.ToolSpec) { s.Name = " echo " }},
		{"no description", func(s *mcpserver.ToolSpec) { s.Description = "" }},
		{"no category", func(s *mcpserver.ToolSpec) { s.Category = "" }},
		{"no tier", func(s *mcpserver.ToolSpec) { s.Tier = policy.Tier(0) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := readOnlySpec(echoTool)
			tc.apply(&spec)

			deps := testDeps(t, registrarFunc(func(r *mcpserver.Registry) error {
				return mcpserver.AddTool(r, spec, echoHandler)
			}))
			if _, err := mcpserver.New(deps); !errors.Is(err, mcpserver.ErrInvalidToolSpec) {
				t.Fatalf("New error = %v, want ErrInvalidToolSpec", err)
			}
		})
	}
}

// A read-only tool that claims to be destructive is a contradiction, and a
// destructive tool that claims to be read-only is a dangerous one.
func TestAddToolRejectsAnnotationsThatContradictTheTier(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec mcpserver.ToolSpec
	}{
		{
			name: "read-only tier not marked read-only",
			spec: mcpserver.ToolSpec{
				Name: echoTool, Description: "d", Category: "c", Tier: policy.TierReadOnly,
				Annotations: mcpserver.Annotations{OpenWorld: true},
			},
		},
		{
			name: "destructive tier marked read-only",
			spec: mcpserver.ToolSpec{
				Name: echoTool, Description: "d", Category: "c", Tier: policy.TierDestructive,
				Annotations: mcpserver.Annotations{ReadOnly: true, Destructive: true, OpenWorld: true},
			},
		},
		{
			name: "destructive tier not marked destructive",
			spec: mcpserver.ToolSpec{
				Name: echoTool, Description: "d", Category: "c", Tier: policy.TierDestructive,
				Annotations: mcpserver.Annotations{OpenWorld: true},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			deps := testDeps(t, registrarFunc(func(r *mcpserver.Registry) error {
				return mcpserver.AddTool(r, tc.spec, echoHandler)
			}))
			if _, err := mcpserver.New(deps); !errors.Is(err, mcpserver.ErrAnnotationMismatch) {
				t.Fatalf("New error = %v, want ErrAnnotationMismatch", err)
			}
		})
	}
}

// Garmin is an open-world API, so a tool that claims a closed world is refused
// rather than silently advertised as closed.
func TestAddToolRequiresTheOpenWorldHint(t *testing.T) {
	t.Parallel()

	spec := readOnlySpec(echoTool)
	spec.Annotations.OpenWorld = false

	deps := testDeps(t, registrarFunc(func(r *mcpserver.Registry) error {
		return mcpserver.AddTool(r, spec, echoHandler)
	}))
	if _, err := mcpserver.New(deps); !errors.Is(err, mcpserver.ErrAnnotationMismatch) {
		t.Fatalf("New error = %v, want ErrAnnotationMismatch", err)
	}
}

// All four hints must reach the wire. At SDK v1.7.0 ReadOnlyHint and
// IdempotentHint are plain bools that serialize even when false, while
// DestructiveHint and OpenWorldHint are *bool, so the pointers must be set
// explicitly or the hint is omitted entirely.
func TestAllFourAnnotationHintsAreDeclaredExplicitly(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, depsWithEcho(t))

	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}

	idx := slices.IndexFunc(tools.Tools, func(tool *mcp.Tool) bool { return tool.Name == echoTool })
	if idx < 0 {
		t.Fatalf("echo is not in the advertised tool list %v", tools.Tools)
	}
	annotations := tools.Tools[idx].Annotations
	if annotations == nil {
		t.Fatal("the tool advertises no annotations")
	}
	if !annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint must be true for a read-only tool")
	}
	if !annotations.IdempotentHint {
		t.Error("IdempotentHint must be true for this tool")
	}
	if annotations.DestructiveHint == nil || *annotations.DestructiveHint {
		t.Error("DestructiveHint must be explicitly false, not omitted")
	}
	if annotations.OpenWorldHint == nil || !*annotations.OpenWorldHint {
		t.Error("OpenWorldHint must be explicitly true: Garmin is an open-world API")
	}
}

func TestRegistryNamesAreSortedForDeterminism(t *testing.T) {
	t.Parallel()

	deps := testDeps(t, registrarFunc(func(r *mcpserver.Registry) error {
		for _, name := range []string{"zebra", "alpha", "middle"} {
			if err := mcpserver.AddTool(r, readOnlySpec(name), echoHandler); err != nil {
				return err
			}
		}
		return nil
	}))
	deps.Policy = mustPolicy(t, policy.Config{
		Mode:          policy.ModeLocal,
		ReadOnlyTools: []string{mcpserver.ServerInfoToolName, "zebra", "alpha", "middle"},
	})

	server := newTestServer(t, deps)

	names := server.ToolNames()
	if !slices.IsSorted(names) {
		t.Fatalf("ToolNames() = %v, want a sorted slice", names)
	}
}

// ToolNames returns a copy, so a caller cannot reach into the registry.
func TestToolNamesReturnsACopy(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, testDeps(t))

	names := server.ToolNames()
	if len(names) == 0 {
		t.Fatal("expected at least the built-in tool")
	}
	names[0] = "mutated"

	if server.ToolNames()[0] == "mutated" {
		t.Fatal("ToolNames must return a copy")
	}
}
