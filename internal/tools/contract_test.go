// The contract test. It pins the registered tool names and their normalized input
// schemas against compat/tools.json, so a drift between the pinned upstream
// manifest and this code fails the build rather than a client.
package tools_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// manifestPath is the pinned contract this package must not drift from.
const manifestPath = "../../compat/tools.json"

// manifestTool is the subset of a manifest entry this test enforces.
type manifestTool struct {
	Name        string         `json:"name"`
	InputSchema map[string]any `json:"inputSchema"`
	Effect      string         `json:"effect"`
	Sensitivity string         `json:"sensitivity"`
	Scope       string         `json:"scope"`
	Idempotency string         `json:"idempotency"`
}

type manifest struct {
	Tools []manifestTool `json:"tools"`
}

func loadManifest(t *testing.T) map[string]manifestTool {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(manifestPath))
	if err != nil {
		t.Fatalf("reading %s: %v", manifestPath, err)
	}
	var decoded manifest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding %s: %v", manifestPath, err)
	}

	byName := make(map[string]manifestTool, len(decoded.Tools))
	for _, tool := range decoded.Tools {
		byName[tool.Name] = tool
	}
	return byName
}

// additionsBeyondTheManifest are the tools this slice registers that the pinned
// manifest does not describe, each with the reason it exists.
//
// The manifest is a snapshot of one upstream commit. Three of these come from the two
// unmerged upstream proposals this project treats as in scope, one is the workout
// update they depend on, and delete_activity exists because the pinned surface exposes
// no activity delete at all while python-garminconnect does. Naming them here is what
// keeps the drift test honest: any other unlisted name is still a failure.
func additionsBeyondTheManifest() map[string]string {
	return map[string]string{
		"update_workout":                         "unmerged upstream proposal: in-place workout update",
		"get_exercise_types":                     "unmerged upstream proposal: strength catalog read",
		"set_activity_strength_exercise_sets":    "unmerged upstream proposal: verified set replace",
		tools.ToolCreateStrengthTrainingActivity: "unmerged upstream proposal: verified strength create",
		"delete_activity":                        "python-garminconnect delete_activity; absent from the pinned surface",
	}
}

// schemaDeviations are the declared departures from the manifest's input schema, each
// with the reason. A deviation that is not listed here fails the drift test.
func schemaDeviations() map[string]map[string]string {
	return map[string]map[string]string{
		"download_activity_file": {
			"output_dir": "a tool argument must not choose a server filesystem path; " +
				"the bytes are returned as an embedded MCP resource instead",
		},
		"upload_course": {
			"gpx_path": "a tool argument must not choose a server filesystem path; " +
				"the caller supplies the GPX document's own bytes as gpx_content instead of a path",
			"gpx_content": "the renamed counterpart of the manifest's gpx_path, declared instead " +
				"because this server accepts no caller-supplied filesystem path anywhere",
		},
	}
}

func TestEveryRegisteredToolNameExistsInTheManifestOrIsADeclaredAddition(t *testing.T) {
	t.Parallel()

	entries := loadManifest(t)
	additions := additionsBeyondTheManifest()
	for name := range tools.Contracts() {
		if _, ok := entries[name]; ok {
			continue
		}
		if _, declared := additions[name]; !declared {
			t.Errorf("tool %q is registered but absent from %s and is not a declared "+
				"addition: the wire name must be the upstream compatibility name", name, manifestPath)
		}
	}
}

func TestEveryDeclaredAdditionIsRegistered(t *testing.T) {
	t.Parallel()

	contracts := tools.Contracts()
	for name, reason := range additionsBeyondTheManifest() {
		if _, ok := contracts[name]; !ok {
			t.Errorf("%q is declared as an addition (%s) but is not registered", name, reason)
		}
	}
}

func TestDeclaredInputSchemasMatchTheManifest(t *testing.T) {
	t.Parallel()

	entries := loadManifest(t)
	for name, contract := range tools.Contracts() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			entry, ok := entries[name]
			if !ok {
				return
			}
			assertSchemaAgrees(t, name, contract.Schema.JSON(), entry.InputSchema)
		})
	}
}

// assertSchemaAgrees compares the declared schema with the manifest schema. The
// declared schema may be stricter — a range or a format the manifest does not state
// is an improvement — but it may not rename, add, drop or retype a property, change
// what is required, or contradict a stated default.
func assertSchemaAgrees(t *testing.T, tool string, declared, wanted map[string]any) {
	t.Helper()

	declaredProps := properties(declared)
	wantedProps := properties(wanted)
	// A deviation name is stripped from BOTH sides: a pure removal (like
	// download_activity_file's output_dir) is absent from declaredProps already, so
	// stripping it from declaredProps is a no-op; a rename (like upload_course's
	// gpx_path/gpx_content pair) lists both the manifest name and its declared
	// replacement, so each is stripped from the side that actually carries it.
	for name := range schemaDeviations()[tool] {
		delete(wantedProps, name)
		delete(declaredProps, name)
	}

	declaredNames := slices.Sorted(maps.Keys(declaredProps))
	wantedNames := slices.Sorted(maps.Keys(wantedProps))
	if !slices.Equal(declaredNames, wantedNames) {
		t.Fatalf("%s: input properties drifted: declared %v, manifest %v",
			tool, declaredNames, wantedNames)
	}

	declaredRequired := requiredOf(declared)
	wantedRequired := requiredOf(wanted)
	for name := range schemaDeviations()[tool] {
		declaredRequired = removeName(declaredRequired, name)
		wantedRequired = removeName(wantedRequired, name)
	}
	if !slices.Equal(declaredRequired, wantedRequired) {
		t.Errorf("%s: required arguments drifted: declared %v, manifest %v",
			tool, declaredRequired, wantedRequired)
	}
	if additional, ok := declared["additionalProperties"].(bool); !ok || additional {
		t.Errorf("%s: declared additionalProperties = %v, want false",
			tool, declared["additionalProperties"])
	}

	for _, name := range wantedNames {
		assertPropertyAgrees(t, tool, name, declaredProps[name], wantedProps[name])
	}
}

func assertPropertyAgrees(t *testing.T, tool, property string, declared, wanted map[string]any) {
	t.Helper()

	if got, want := typesOf(declared), typesOf(wanted); !slices.Equal(got, want) {
		t.Errorf("%s.%s: type drifted: declared %v, manifest %v", tool, property, got, want)
	}
	// A manifest default of null states that the argument has no value when it is
	// absent, which is exactly what declaring no default means. Only a real default
	// has to be reproduced.
	wantedDefault, stated := wanted["default"]
	if !stated || wantedDefault == nil {
		return
	}
	declaredDefault, present := declared["default"]
	if !present {
		t.Errorf("%s.%s: the manifest states the default %v and the declaration states none",
			tool, property, wantedDefault)
		return
	}
	if !equalJSON(declaredDefault, wantedDefault) {
		t.Errorf("%s.%s: default drifted: declared %v, manifest %v",
			tool, property, declaredDefault, wantedDefault)
	}
}

func TestDeclaredSchemasMatchTheSchemasOnTheWire(t *testing.T) {
	h := newFullVisibilityHarness(t, readScript())

	contracts := tools.Contracts()
	for _, tool := range listedTools(t, h) {
		if tool.Name == mcpserver.ServerInfoToolName {
			continue
		}
		contract, ok := contracts[tool.Name]
		if !ok {
			t.Errorf("%s is on the wire but Contracts() does not describe it", tool.Name)
			continue
		}

		wire := schemaOf(t, tool)
		declared := contract.Schema.JSON()
		wireNames := slices.Sorted(maps.Keys(properties(wire)))
		declaredNames := slices.Sorted(maps.Keys(properties(declared)))
		if !slices.Equal(wireNames, declaredNames) {
			t.Errorf("%s: wire properties %v, declared %v", tool.Name, wireNames, declaredNames)
		}
		if got, want := requiredOf(wire), requiredOf(declared); !slices.Equal(got, want) {
			t.Errorf("%s: wire required %v, declared %v", tool.Name, got, want)
		}
	}
}

// tierEffects maps a policy tier onto the manifest effect that tier must carry.
//
// external-side-effect is accepted for the write tier: the manifest gives
// download_activity_file that effect because upstream writes a file to disk, and this
// server does not, so the strongest honest classification here is a write.
func tierEffects() map[policy.Tier][]string {
	return map[policy.Tier][]string{
		policy.TierReadOnly:    {"read-only"},
		policy.TierWrite:       {"write", "external-side-effect"},
		policy.TierDestructive: {"destructive"},
	}
}

func TestEveryRegisteredToolsTierMatchesTheManifestEffect(t *testing.T) {
	t.Parallel()

	entries := loadManifest(t)
	effects := tierEffects()
	for name, contract := range tools.Contracts() {
		entry, ok := entries[name]
		if !ok {
			continue
		}
		if want := effects[contract.Spec.Tier]; !slices.Contains(want, entry.Effect) {
			t.Errorf("%s: manifest effect = %q, but this slice registers it in the %v tier, "+
				"which requires one of %v", name, entry.Effect, contract.Spec.Tier, want)
		}
	}
}

// TestEveryRegisteredToolsIdempotentHintMatchesTheManifest pins the hint against the
// manifest's classification rather than against upstream's description.
//
// The two disagree on purpose for the scheduling tools: upstream's docstring says
// "Idempotent", and the manifest records non-idempotent because the pre-check that
// claim rests on fails open. The classification is the one that is true.
func TestEveryRegisteredToolsIdempotentHintMatchesTheManifest(t *testing.T) {
	t.Parallel()

	entries := loadManifest(t)
	for name, contract := range tools.Contracts() {
		entry, ok := entries[name]
		if !ok || entry.Idempotency == "unknown" {
			continue
		}
		want := entry.Idempotency == "idempotent"
		if got := contract.Spec.Annotations.Idempotent; got != want {
			t.Errorf("%s: idempotent hint = %t, manifest idempotency = %q",
				name, got, entry.Idempotency)
		}
	}
}

func TestEveryRegisteredToolLogsTheManifestSensitivityDomain(t *testing.T) {
	t.Parallel()

	entries := loadManifest(t)
	for name, contract := range tools.Contracts() {
		entry, ok := entries[name]
		if !ok {
			continue
		}
		if got, want := contract.Spec.Category, entry.Sensitivity; got != want {
			t.Errorf("%s: log category = %q, manifest sensitivity = %q", name, got, want)
		}
	}
}

// TestNoManifestToolIsRegisteredWithoutTheEndpointItNeeds pins the tools this slice
// deliberately leaves unregistered, so the parity manifest keeps telling the truth.
func TestNoManifestToolIsRegisteredWithoutTheEndpointItNeeds(t *testing.T) {
	t.Parallel()

	unregistered := map[string]string{
		"set_fit_download_dir": "would persist a caller-supplied server filesystem path",
	}

	contracts := tools.Contracts()
	for name, reason := range unregistered {
		if _, ok := contracts[name]; ok {
			t.Errorf("%s is registered, but it is recorded as unimplemented because it %s",
				name, reason)
		}
	}
}

func properties(schema map[string]any) map[string]map[string]any {
	raw, _ := schema["properties"].(map[string]any)
	out := make(map[string]map[string]any, len(raw))
	for name, value := range raw {
		property, _ := value.(map[string]any)
		out[name] = property
	}
	return out
}

// removeName returns names with every occurrence of value removed.
func removeName(names []string, value string) []string {
	out := names[:0:0]
	for _, name := range names {
		if name != value {
			out = append(out, name)
		}
	}
	return out
}

func requiredOf(schema map[string]any) []string {
	var out []string
	switch required := schema["required"].(type) {
	case []any:
		for _, value := range required {
			if name, ok := value.(string); ok {
				out = append(out, name)
			}
		}
	case []string:
		out = append(out, required...)
	}
	slices.Sort(out)
	return out
}

// typesOf renders a property's accepted JSON types, flattening the anyOf form the
// manifest uses for an identifier that arrives as a number or as a string.
func typesOf(property map[string]any) []string {
	if property == nil {
		return nil
	}
	if single, ok := property["type"].(string); ok {
		return []string{single}
	}
	variants, _ := property["anyOf"].([]any)
	out := make([]string, 0, len(variants))
	for _, variant := range variants {
		entry, _ := variant.(map[string]any)
		if name, ok := entry["type"].(string); ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func equalJSON(a, b any) bool {
	encodedA, errA := json.Marshal(a)
	encodedB, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return reflect.DeepEqual(a, b)
	}
	return string(encodedA) == string(encodedB)
}
