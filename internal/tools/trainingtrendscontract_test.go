package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The contract half of the trend tests: what these tools declare to a client, checked
// against the pinned manifest, and that every one of them registers.

// trendManifestPath is the pinned contract these tools take their names, arguments and
// effects from.
const trendManifestPath = "../../compat/tools.json"

// manifestEntry is the subset of a manifest record these tests enforce.
type manifestEntry struct {
	Name        string         `json:"name"`
	InputSchema map[string]any `json:"inputSchema"`
	Effect      string         `json:"effect"`
	Idempotency string         `json:"idempotency"`
}

func loadTrendManifest(t *testing.T) map[string]manifestEntry {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(trendManifestPath))
	if err != nil {
		t.Fatalf("reading %s: %v", trendManifestPath, err)
	}
	var decoded struct {
		Tools []manifestEntry `json:"tools"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding %s: %v", trendManifestPath, err)
	}
	byName := make(map[string]manifestEntry, len(decoded.Tools))
	for _, tool := range decoded.Tools {
		byName[tool.Name] = tool
	}
	return byName
}

// trendContracts is every contract this slice declares, keyed by wire name.
func trendContracts() map[string]Contract {
	contracts := map[string]Contract{}
	for _, build := range []func() Contract{
		getProgressSummaryBetweenDatesContract,
		getHRVDataContract,
		getHRVTrendContract,
		getVO2MaxTrendContract,
		getRespirationTrendContract,
		getTrainingLoadTrendContract,
		getTrainingLoadBalanceContract,
		requestReloadContract,
	} {
		contract := build()
		contracts[contract.Spec.Name] = contract
	}
	return contracts
}

// TestTrendContractsMatchTheManifest is the drift test for this slice. It is the check
// register.go's contract test performs, run here because these tools are not yet in
// the registration lists.
func TestTrendContractsMatchTheManifest(t *testing.T) {
	t.Parallel()

	entries := loadTrendManifest(t)
	for name, contract := range trendContracts() {
		entry, ok := entries[name]
		if !ok {
			t.Errorf("%s is not in %s: the wire name must be the upstream one",
				name, trendManifestPath)
			continue
		}

		declared := contract.Schema.JSON()
		if got, want := schemaNames(declared), schemaNames(entry.InputSchema); !slices.Equal(got, want) {
			t.Errorf("%s: properties %v, manifest %v", name, got, want)
		}
		if got, want := requiredNames(declared), requiredNames(entry.InputSchema); !slices.Equal(got, want) {
			t.Errorf("%s: required %v, manifest %v", name, got, want)
		}
		if additional, ok := declared["additionalProperties"].(bool); !ok || additional {
			t.Errorf("%s: additionalProperties = %v, want false",
				name, declared["additionalProperties"])
		}
		if want := entry.Idempotency == "idempotent"; contract.Spec.Annotations.Idempotent != want {
			t.Errorf("%s: idempotent hint = %v, manifest says %q",
				name, contract.Spec.Annotations.Idempotent, entry.Idempotency)
		}
		if !contract.Spec.Annotations.OpenWorld {
			t.Errorf("%s: open-world hint is false; Garmin is an open-world API", name)
		}
		if contract.Spec.Annotations.Destructive {
			t.Errorf("%s: destructive hint is true; no tool in this slice is destructive", name)
		}
	}
}

// TestOnlyRequestReloadIsAWrite pins the tier split of this slice.
func TestOnlyRequestReloadIsAWrite(t *testing.T) {
	t.Parallel()

	for name, contract := range trendContracts() {
		readOnly := contract.Spec.Annotations.ReadOnly
		if name == ToolRequestReload {
			if readOnly {
				t.Errorf("%s declares itself read-only, but it POSTs to Garmin", name)
			}
			continue
		}
		if !readOnly {
			t.Errorf("%s is a read and must declare the read-only hint", name)
		}
	}
}

func schemaNames(schema map[string]any) []string {
	properties, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func requiredNames(schema map[string]any) []string {
	var names []string
	switch required := schema["required"].(type) {
	case []string:
		names = slices.Clone(required)
	case []any:
		for _, value := range required {
			if name, ok := value.(string); ok {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}

// trendRegistrar registers this slice's tools and records what landed.
//
// register.go does not carry them yet, so this stands in for the tier lists: it proves
// every contract is one the SDK accepts, and it is what the composition root will do
// once the eight registration entries are added.
type trendRegistrar struct {
	svc   *service
	names []string
}

func (r *trendRegistrar) RegisterTools(registry *mcpserver.Registry) error {
	registrations := []func(*mcpserver.Registry, *service) error{
		registerGetProgressSummaryBetweenDates,
		registerGetHRVData,
		registerGetHRVTrend,
		registerGetVO2MaxTrend,
		registerGetRespirationTrend,
		registerGetTrainingLoadTrend,
		registerGetTrainingLoadBalance,
		registerRequestReload,
	}
	for _, register := range registrations {
		if err := register(registry, r.svc); err != nil {
			return err
		}
	}
	r.names = registry.Names()
	return nil
}

// TestTrendToolsRegister proves every tool of this slice registers with its declared
// schema, in the tier its contract names.
func TestTrendToolsRegister(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, testkit.NewScript())
	registrar := &trendRegistrar{svc: h.svc}

	readOnly := []string{mcpserver.ServerInfoToolName}
	for name, contract := range trendContracts() {
		if contract.Spec.Tier == policy.TierReadOnly {
			readOnly = append(readOnly, name)
		}
	}
	pol, err := policy.New(policy.Config{
		Mode:          policy.ModeLocal,
		ReadOnlyTools: readOnly,
		WriteTools:    []string{ToolRequestReload},
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{harnessPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}
	if _, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-trend-test", Version: harnessVersion},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{registrar},
	}); err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}

	for name := range trendContracts() {
		if !slices.Contains(registrar.names, name) {
			t.Errorf("%s did not register: registered %v", name, registrar.names)
		}
	}
}
