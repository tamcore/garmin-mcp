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

func TestEveryRegisteredToolNameExistsInTheManifest(t *testing.T) {
	t.Parallel()

	entries := loadManifest(t)
	for name := range tools.Contracts() {
		if _, ok := entries[name]; !ok {
			t.Errorf("tool %q is registered but absent from %s: the wire name must be the "+
				"upstream compatibility name", name, manifestPath)
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
				t.Fatalf("no manifest entry for %q", name)
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

	declaredNames := slices.Sorted(maps.Keys(declaredProps))
	wantedNames := slices.Sorted(maps.Keys(wantedProps))
	if !slices.Equal(declaredNames, wantedNames) {
		t.Fatalf("%s: input properties drifted: declared %v, manifest %v",
			tool, declaredNames, wantedNames)
	}

	if got, want := requiredOf(declared), requiredOf(wanted); !slices.Equal(got, want) {
		t.Errorf("%s: required arguments drifted: declared %v, manifest %v", tool, got, want)
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
	wantedDefault, stated := wanted["default"]
	if !stated {
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
	h := newHarness(t, readScript())

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

func TestEveryRegisteredToolIsReadOnlyInTheManifest(t *testing.T) {
	t.Parallel()

	entries := loadManifest(t)
	for name := range tools.Contracts() {
		if got := entries[name].Effect; got != "read-only" {
			t.Errorf("%s: manifest effect = %q, but this slice registers it as read-only", name, got)
		}
	}
}

func TestEveryRegisteredToolLogsTheManifestSensitivityDomain(t *testing.T) {
	t.Parallel()

	entries := loadManifest(t)
	for name, contract := range tools.Contracts() {
		if got, want := contract.Spec.Category, entries[name].Sensitivity; got != want {
			t.Errorf("%s: log category = %q, manifest sensitivity = %q", name, got, want)
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
