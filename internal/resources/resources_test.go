package resources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// structureReferenceURI is the one document that describes the others rather than
// being a workout, so the agreement test skips it as a subject.
const structureReferenceURI = "workout://reference/structure"

// The vocabulary field names, named once so a rename shows up in one place.
const (
	keyStepType   = "stepTypeKey"
	keyCondition  = "conditionTypeKey"
	keyTarget     = "workoutTargetTypeKey"
	keySport      = "sportTypeKey"
	zoneTargetKey = "heart.rate.zone"
)

// manifestEntry is the part of a resource record these tests compare against.
type manifestEntry struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MIMEType    string `json:"mimeType"`
}

func loadManifest(t *testing.T) map[string]manifestEntry {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "compat", "resources.json"))
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}
	var manifest struct {
		Resources []manifestEntry `json:"resources"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decoding the manifest: %v", err)
	}

	entries := make(map[string]manifestEntry, len(manifest.Resources))
	for _, entry := range manifest.Resources {
		entries[entry.URI] = entry
	}
	return entries
}

// TestEveryManifestResourceIsServed pins the published set to the pinned upstream
// surface in both directions: a resource the manifest names and this package does
// not serve is a gap, and one this package serves that the manifest does not name is
// an addition that has to be recorded rather than slipped in.
func TestEveryManifestResourceIsServed(t *testing.T) {
	t.Parallel()

	manifest := loadManifest(t)
	served := map[string]bool{}
	for _, doc := range documents() {
		served[doc.spec.URI] = true
	}

	for uri := range manifest {
		if !served[uri] {
			t.Errorf("the manifest names %q, which this package does not serve", uri)
		}
	}
	for uri := range served {
		if _, ok := manifest[uri]; !ok {
			t.Errorf("this package serves %q, which the manifest does not name", uri)
		}
	}
}

// TestEveryResourceMatchesItsManifestContract checks the fields a client sees. The
// name and the media type are what a caller keys off, and the description is what a
// model reads to decide whether the document is the one it wants.
func TestEveryResourceMatchesItsManifestContract(t *testing.T) {
	t.Parallel()

	manifest := loadManifest(t)
	for _, doc := range documents() {
		entry, ok := manifest[doc.spec.URI]
		if !ok {
			continue // reported by TestEveryManifestResourceIsServed
		}
		t.Run(doc.spec.URI, func(t *testing.T) {
			t.Parallel()

			if doc.spec.Name != entry.Name {
				t.Errorf("name = %q, manifest = %q", doc.spec.Name, entry.Name)
			}
			if doc.spec.MIMEType != entry.MIMEType {
				t.Errorf("mimeType = %q, manifest = %q", doc.spec.MIMEType, entry.MIMEType)
			}
			if doc.spec.Description != entry.Description {
				t.Errorf("description drifted from the manifest:\n got %q\nwant %q",
					doc.spec.Description, entry.Description)
			}
		})
	}
}

// TestEveryDocumentRenders proves no document reaches render's error path, which
// returns an empty body. A resource that served "" would still be a valid MCP
// response, so nothing else would notice.
func TestEveryDocumentRenders(t *testing.T) {
	t.Parallel()

	for _, doc := range documents() {
		t.Run(doc.spec.URI, func(t *testing.T) {
			t.Parallel()

			body := doc.body()
			if body == "" {
				t.Fatal("the document rendered empty, so render() took its error path")
			}
			var decoded any
			if err := json.Unmarshal([]byte(body), &decoded); err != nil {
				t.Fatalf("the served document is not valid JSON: %v", err)
			}
			if body[0] != '{' {
				t.Errorf("the document is not a JSON object, it starts with %q", body[0])
			}
		})
	}
}

// TestTheReferenceDescribesTheVocabularyTheTemplatesUse is the agreement check
// between the two halves of this package.
//
// The reference is what a caller reads before writing a workout of their own. If a
// template used a step type, condition or sport the reference does not list, the
// caller would be told a valid value is invalid — or would copy a template the
// reference contradicts.
func TestTheReferenceDescribesTheVocabularyTheTemplatesUse(t *testing.T) {
	t.Parallel()

	declared := map[string]map[string]bool{
		keyStepType:  keySet(stepTypeKeys()),
		keyCondition: keySet(endConditionKeys()),
		keyTarget:    keySet(targetTypeKeys()),
		keySport:     keySet(sportTypeKeys()),
	}

	for _, doc := range documents() {
		if doc.spec.URI == structureReferenceURI {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(doc.body()), &decoded); err != nil {
			t.Fatalf("%s: %v", doc.spec.URI, err)
		}
		for field, values := range declared {
			for _, used := range valuesOfKey(decoded, field) {
				if !values[used] {
					t.Errorf("%s uses %s %q, which the structure reference does not list",
						doc.spec.URI, field, used)
				}
			}
		}
	}
}

// canonicalPairs is the vocabulary as the pinned upstream's own structure reference
// states it, written out here rather than read from the production tables.
//
// Deriving the expectation from the code under test is what makes a test agree with
// any change to it: renumbering distance from 3 to 4 would update the template and
// the reference together, both tests would pass, and Garmin would read the wrong
// condition. These literals are the independent copy.
func canonicalPairs() map[string]map[int]string {
	return map[string]map[int]string{
		keyStepType: {
			1: "warmup", 2: "cooldown", 3: "interval", 4: "recovery", 5: "rest",
		},
		keyCondition: {
			1: "lap.button", 2: "time", 3: "distance", 7: "iterations", 10: "reps",
		},
		keyTarget: {1: "no.target", 4: "heart.rate.zone"},
		keySport:  {1: "running", 5: "strength_training"},
	}
}

// TestTheVocabularyMatchesTheUpstreamReference compares this package's tables against
// that independent copy, in both directions.
func TestTheVocabularyMatchesTheUpstreamReference(t *testing.T) {
	t.Parallel()

	tables := map[string]map[int]string{
		keyStepType:  stepTypeKeys(),
		keyCondition: endConditionKeys(),
		keyTarget:    targetTypeKeys(),
		keySport:     sportTypeKeys(),
	}
	for field, want := range canonicalPairs() {
		got := tables[field]
		for id, key := range want {
			if got[id] != key {
				t.Errorf("%s id %d = %q, upstream states %q", field, id, got[id], key)
			}
		}
		for id, key := range got {
			if _, ok := want[id]; !ok {
				t.Errorf("%s carries id %d (%q), which upstream does not state", field, id, key)
			}
		}
	}
}

// TestTheServedReferenceCarriesEveryTable decodes the document a client actually
// reads, rather than the tables behind it.
//
// The agreement test above compares templates against the Go tables, so dropping a
// whole section from the rendered reference would not have failed anything: the
// caller would simply never be told those values exist.
func TestTheServedReferenceCarriesEveryTable(t *testing.T) {
	t.Parallel()

	var served map[string]any
	for _, doc := range documents() {
		if doc.spec.URI != structureReferenceURI {
			continue
		}
		if err := json.Unmarshal([]byte(doc.body()), &served); err != nil {
			t.Fatalf("decoding the served reference: %v", err)
		}
	}
	if served == nil {
		t.Fatal("the structure reference is not among the served documents")
	}

	sections := map[string]string{
		"stepType_values":     "stepTypeKey",
		"endCondition_values": "conditionTypeKey",
		"targetType_values":   "workoutTargetTypeKey",
		"sportType_values":    "sportTypeKey",
	}
	for section, field := range sections {
		table, ok := served[section].(map[string]any)
		if !ok {
			t.Errorf("the served reference carries no %s section", section)
			continue
		}
		for id, key := range canonicalPairs()[field] {
			entry, ok := table[strconv.Itoa(id)].(map[string]any)
			if !ok {
				t.Errorf("%s is missing id %d", section, id)
				continue
			}
			if got, _ := entry[field].(string); got != key {
				t.Errorf("%s id %d = %q, want %q", section, id, got, key)
			}
		}
	}
	for _, required := range []string{"description", "step_types", "strength_training_fields"} {
		if _, ok := served[required]; !ok {
			t.Errorf("the served reference carries no %q field", required)
		}
	}
}

// TestTheReferenceListsEveryIdItsOwnTableCarries guards the rendering, which turns
// numeric ids into JSON object keys. A collision or a dropped entry there would
// silently shorten the reference.
func TestTheReferenceListsEveryIdItsOwnTableCarries(t *testing.T) {
	t.Parallel()

	for name, table := range map[string]map[int]string{
		keyStepType:  stepTypeKeys(),
		keyCondition: endConditionKeys(),
		keyTarget:    targetTypeKeys(),
		keySport:     sportTypeKeys(),
	} {
		if got := len(keyedValues(name, table)); got != len(table) {
			t.Errorf("%s rendered %d entries from a table of %d", name, got, len(table))
		}
	}
}

// keySet is the set of key names a vocabulary table declares.
func keySet(table map[int]string) map[string]bool {
	out := make(map[string]bool, len(table))
	for _, key := range table {
		out[key] = true
	}
	return out
}

// valuesOfKey walks a decoded document and collects every string value stored under
// field, at any depth.
func valuesOfKey(node any, field string) []string {
	var found []string
	switch value := node.(type) {
	case map[string]any:
		for key, nested := range value {
			if key == field {
				if text, ok := nested.(string); ok {
					found = append(found, text)
					continue
				}
			}
			found = append(found, valuesOfKey(nested, field)...)
		}
	case []any:
		for _, nested := range value {
			found = append(found, valuesOfKey(nested, field)...)
		}
	}
	slices.Sort(found)
	return slices.Compact(found)
}

// TestTheRegistrarRegistersEveryDocument drives the registrar the composition root
// wires in, against a real server, so a document that this package lists but cannot
// register fails here rather than at start-up.
func TestTheRegistrarRegistersEveryDocument(t *testing.T) {
	t.Parallel()

	server, err := mcpserver.New(mcpserver.Deps{
		Info:               mcpserver.Info{Name: "garmin-mcp-resources-test", Version: "test"},
		Policy:             mustReadOnlyPolicy(t),
		Principals:         mustStdioResolver(t),
		ResourceRegistrars: []mcpserver.ResourceRegistrar{NewRegistrar()},
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}

	registered := server.Registry().ResourceURIs()
	for _, doc := range documents() {
		if !slices.Contains(registered, doc.spec.URI) {
			t.Errorf("%s was not registered; the server holds %v", doc.spec.URI, registered)
		}
	}
	if len(registered) != len(documents()) {
		t.Errorf("registered %d resources, want %d", len(registered), len(documents()))
	}
}

func mustReadOnlyPolicy(t *testing.T) *policy.Policy {
	t.Helper()

	pol, err := policy.New(policy.Config{
		Mode:          policy.ModeLocal,
		ReadOnlyTools: []string{mcpserver.ServerInfoToolName},
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}
	return pol
}

func mustStdioResolver(t *testing.T) identity.Resolver {
	t.Helper()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{"local"},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}
	return resolver
}
