package tools_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// wantReadOnlyToolNames is the read-only Garmin surface this slice registers, in the
// order register.go wires them.
var wantReadOnlyToolNames = []string{
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
	"get_userprofile_settings",
	"get_personal_record",
	"get_activity_splits",
	"get_activity_split_summaries",
	"get_activity_hr_in_timezones",
	"get_activity_power_in_timezones",
	"get_activity_weather",
	"get_exercise_types",
	"get_workouts",
	"get_workout_by_id",
	"download_workout",
}

// wantWriteToolNames is the write tier. Every one of them needs the operator's
// enablement and the caller's write scope, so a default deployment refuses all of
// them and a stdio deployment refuses them whatever the operator enabled.
var wantWriteToolNames = []string{
	"set_activity_name",
	"set_activity_type",
	"set_activity_event_type",
	"set_activity_description",
	"set_activity_feel",
	"set_perceived_effort",
	"add_gear_to_activity",
	"remove_gear_from_activity",
	"create_manual_activity",
	"set_activity_strength_exercise_sets",
	tools.ToolCreateStrengthTrainingActivity,
	"upload_workout",
	"upload_workouts",
	"update_workout",
	"schedule_workout",
	"schedule_workouts",
	"create_walk_run_workout",
	"create_run_workout",
	"create_z2_walk_workout",
	"create_strength_workout",
	"download_activity_file",
}

// wantDestructiveToolNames is the destructive tier, which the server's confirmation
// middleware covers because each tool declares itself destructive.
var wantDestructiveToolNames = []string{
	"delete_activity",
	"delete_workout",
	"delete_workouts",
	"unschedule_workout",
	"unschedule_workouts",
}

// wantToolNames is the whole Garmin surface this slice registers.
func wantToolNames() []string {
	return slices.Concat(
		wantReadOnlyToolNames, wantWriteToolNames, wantDestructiveToolNames)
}

// nonIdempotentTools are the tools whose repeat does not converge: a create, and the
// two scheduling tools the manifest classifies as non-idempotent because upstream's
// duplicate pre-check fails open.
var nonIdempotentTools = []string{
	"create_manual_activity",
	tools.ToolCreateStrengthTrainingActivity,
	"upload_workout",
	"upload_workouts",
	"schedule_workout",
	"schedule_workouts",
	"create_walk_run_workout",
	"create_run_workout",
	"create_z2_walk_workout",
	"create_strength_workout",
}

// forbiddenArgumentNames are the argument names no tool may ever accept: an account
// selector would let a caller act as somebody else, and a path would let a remote
// caller name a server file.
var forbiddenArgumentNames = []string{
	"user_id", "userid", "user", "email", "account", "account_id", "principal",
	"principal_id", "display_name", "token", "path", "file", "filename", "dir",
	"directory", "output_path", "url",
}

func TestRegisterAllRegistersExactlyTheDeclaredSurface(t *testing.T) {
	h := newHarness(t, readScript())

	got := listedToolNames(t, h)
	want := append([]string{mcpserver.ServerInfoToolName}, wantToolNames()...)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("registered tools = %v, want %v", got, want)
	}
}

func TestEachTierListMatchesTheRegisteredSurface(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		got  []string
		want []string
	}{
		"read-only": {
			tools.ReadOnlyTools(),
			append([]string{mcpserver.ServerInfoToolName}, wantReadOnlyToolNames...),
		},
		"write":       {tools.WriteTools(), wantWriteToolNames},
		"destructive": {tools.DestructiveTools(), wantDestructiveToolNames},
	}
	for tier, tc := range cases {
		t.Run(tier, func(t *testing.T) {
			t.Parallel()

			got, want := slices.Clone(tc.got), slices.Clone(tc.want)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("%s tier = %v, want %v", tier, got, want)
			}
		})
	}
}

func TestEveryToolDeclaresAllFourAnnotationHintsForItsTier(t *testing.T) {
	h := newHarness(t, readScript())

	for _, tool := range listedTools(t, h) {
		t.Run(tool.Name, func(t *testing.T) {
			annotations := tool.Annotations
			if annotations == nil {
				t.Fatalf("%s declares no annotations", tool.Name)
			}
			assertTierHints(t, tool.Name, annotations)
			assertBoolHint(t, "openWorldHint", annotations.OpenWorldHint, true)
		})
	}
}

// assertTierHints checks the three effect hints against the tier the tool is in.
func assertTierHints(t *testing.T, name string, annotations *mcp.ToolAnnotations) {
	t.Helper()

	destructive := slices.Contains(wantDestructiveToolNames, name)
	write := slices.Contains(wantWriteToolNames, name)
	readOnly := !destructive && !write

	if annotations.ReadOnlyHint != readOnly {
		t.Errorf("readOnlyHint = %t, want %t", annotations.ReadOnlyHint, readOnly)
	}
	assertBoolHint(t, "destructiveHint", annotations.DestructiveHint, destructive)

	wantIdempotent := !slices.Contains(nonIdempotentTools, name)
	if annotations.IdempotentHint != wantIdempotent {
		t.Errorf("idempotentHint = %t, want %t", annotations.IdempotentHint, wantIdempotent)
	}
}

func TestNoToolDescriptionRepeatsUpstreamsIdempotencyClaim(t *testing.T) {
	t.Parallel()

	for name, contract := range tools.Contracts() {
		if strings.Contains(contract.Spec.Description, "Idempotent:") {
			t.Errorf("%s carries upstream's \"Idempotent:\" sentence, which is wrong for "+
				"the scheduling tools and must not be repeated", name)
		}
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
