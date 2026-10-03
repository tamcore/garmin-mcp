package tools_test

import (
	"cmp"
	"encoding/json"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// surfaceEntry is one tool as tools/list publishes it, plus the tier that gates it.
type surfaceEntry struct {
	Name         string               `json:"name"`
	Tier         string               `json:"tier"`
	Annotations  *mcp.ToolAnnotations `json:"annotations"`
	InputSchema  any                  `json:"inputSchema"`
	OutputSchema any                  `json:"outputSchema,omitempty"`
}

// TestToolSurfaceMatchesTheGoldenSnapshot pins the whole published tool surface.
func TestToolSurfaceMatchesTheGoldenSnapshot(t *testing.T) {
	h := newFullVisibilityHarness(t, readScript())
	contracts := tools.Contracts()

	listed := listedTools(t, h)
	entries := make([]surfaceEntry, 0, len(listed))
	for _, tool := range listed {
		tier := policy.TierReadOnly
		if contract, ok := contracts[tool.Name]; ok {
			tier = contract.Spec.Tier
		}
		entries = append(entries, surfaceEntry{
			Name:         tool.Name,
			Tier:         tier.String(),
			Annotations:  tool.Annotations,
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
		})
	}
	slices.SortFunc(entries, func(a, b surfaceEntry) int { return cmp.Compare(a.Name, b.Name) })

	// encoding/json v1 matches what the SDK puts on the wire.
	got, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("encoding the surface: %v", err)
	}
	testkit.Golden(t, "testdata/tools.golden.json", append(got, '\n'))
}
