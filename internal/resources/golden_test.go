package resources

import (
	"cmp"
	"encoding/json"
	"slices"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestResourceSurfaceMatchesTheGoldenSnapshot pins what resources/list publishes.
func TestResourceSurfaceMatchesTheGoldenSnapshot(t *testing.T) {
	type entry struct {
		URI         string `json:"uri"`
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
		MIMEType    string `json:"mimeType"`
	}
	docs := documents()
	entries := make([]entry, 0, len(docs))
	for _, doc := range docs {
		entries = append(entries, entry(doc.spec))
	}
	slices.SortFunc(entries, func(a, b entry) int { return cmp.Compare(a.URI, b.URI) })

	got, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("encoding the surface: %v", err)
	}
	testkit.Golden(t, "testdata/resources.golden.json", append(got, '\n'))
}
