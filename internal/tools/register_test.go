package tools_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// wantToolNames is the read-only Garmin surface this slice registers, with the
// manifest-matching names, in the order register.go wires them.
var wantToolNames = []string{
	"get_user_profile",
	"get_full_name",
	"get_unit_system",
	"get_activities",
	"get_activities_by_date",
	"get_sleep_data",
	"get_user_summary",
	tools.ToolGetDevices,
	"get_activity_typed_splits",
	"get_activity_exercise_sets",
}

// forbiddenArgumentNames are the argument names no tool may ever accept: an account
// selector would let a caller act as somebody else, and a path would let a remote
// caller name a server file.
var forbiddenArgumentNames = []string{
	"user_id", "userid", "user", "email", "account", "account_id", "principal",
	"principal_id", "display_name", "token", "path", "file", "filename", "dir",
	"directory", "output_path", "url",
}

func TestRegisterAllRegistersExactlyTheReadOnlySurface(t *testing.T) {
	h := newHarness(t, readScript())

	got := listedToolNames(t, h)
	want := append([]string{mcpserver.ServerInfoToolName}, wantToolNames...)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("registered tools = %v, want %v", got, want)
	}
}

func TestReadOnlyToolListMatchesTheRegisteredSurface(t *testing.T) {
	t.Parallel()

	want := append([]string{mcpserver.ServerInfoToolName}, wantToolNames...)
	slices.Sort(want)

	got := slices.Clone(tools.ReadOnlyTools())
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Errorf("ReadOnlyTools() = %v, want %v", got, want)
	}
}

func TestWriteAndDestructiveTierListsExistAndAreEmptyToday(t *testing.T) {
	t.Parallel()

	if got := tools.WriteTools(); len(got) != 0 {
		t.Errorf("WriteTools() = %v, want an empty list: no write tool exists yet", got)
	}
	if got := tools.DestructiveTools(); len(got) != 0 {
		t.Errorf("DestructiveTools() = %v, want an empty list: no destructive tool exists yet", got)
	}
}

func TestEveryToolDeclaresAllFourAnnotationHints(t *testing.T) {
	h := newHarness(t, readScript())

	for _, tool := range listedTools(t, h) {
		t.Run(tool.Name, func(t *testing.T) {
			annotations := tool.Annotations
			if annotations == nil {
				t.Fatalf("%s declares no annotations", tool.Name)
			}
			if !annotations.ReadOnlyHint {
				t.Error("readOnlyHint is false, but every tool in this slice only reads")
			}
			if !annotations.IdempotentHint {
				t.Error("idempotentHint is false, but a read repeats without effect")
			}
			assertBoolHint(t, "destructiveHint", annotations.DestructiveHint, false)
			assertBoolHint(t, "openWorldHint", annotations.OpenWorldHint, true)
		})
	}
}

func TestNoToolAcceptsAnAccountSelectorOrAFilesystemPath(t *testing.T) {
	h := newHarness(t, readScript())

	for _, tool := range listedTools(t, h) {
		properties, _ := schemaOf(t, tool)["properties"].(map[string]any)
		for name := range properties {
			if slices.Contains(forbiddenArgumentNames, strings.ToLower(name)) {
				t.Errorf("%s accepts the argument %q: the principal comes from the request context",
					tool.Name, name)
			}
		}
	}
}

func TestEveryToolCarriesADescriptionAndAStrictObjectSchema(t *testing.T) {
	h := newHarness(t, readScript())

	for _, tool := range listedTools(t, h) {
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		schema := schemaOf(t, tool)
		if schema["type"] != "object" {
			t.Errorf("%s: schema type = %v, want object", tool.Name, schema["type"])
		}
		if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
			t.Errorf("%s: additionalProperties = %v, want false",
				tool.Name, schema["additionalProperties"])
		}
	}
}

func assertBoolHint(t *testing.T, name string, got *bool, want bool) {
	t.Helper()

	if got == nil {
		t.Errorf("%s is absent, but all four hints must be declared explicitly", name)
		return
	}
	if *got != want {
		t.Errorf("%s = %t, want %t", name, *got, want)
	}
}

func schemaOf(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()

	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("%s: input schema is %T, want a JSON object", tool.Name, tool.InputSchema)
	}
	return schema
}

func listedTools(t *testing.T, h harness) []*mcp.Tool {
	t.Helper()

	result, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools() = %v", err)
	}
	return result.Tools
}

func listedToolNames(t *testing.T, h harness) []string {
	t.Helper()

	listed := listedTools(t, h)
	names := make([]string, 0, len(listed))
	for _, tool := range listed {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}
