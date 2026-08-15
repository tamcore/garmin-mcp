// The manifest status test. The contract test pins names and schemas; this one pins
// the manifest's own `status` field against what this package actually registers, so a
// manifest that claims a tool is implemented when it is not, or that still calls a
// registered tool unimplemented, fails the build rather than a reader.
package tools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// statusManifestPath is the same pinned manifest the contract test reads.
const statusManifestPath = "../../compat/tools.json"

// statusImplemented is the enum value that claims this server registers the tool.
const statusImplemented = "implemented"

// manifestStatusEntry is the subset of a manifest record this test enforces: the wire
// name, the claimed status, and the Go block that has to describe a real registration.
type manifestStatusEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Go     struct {
		Package        string `json:"package"`
		Handler        string `json:"handler"`
		File           string `json:"file"`
		RegisteredName string `json:"registeredName"`
	} `json:"go"`
}

type manifestStatusDocument struct {
	Counts struct {
		ByStatus map[string]int `json:"byStatus"`
	} `json:"counts"`
	Tools []manifestStatusEntry `json:"tools"`
}

func loadManifestStatuses(t *testing.T) manifestStatusDocument {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(statusManifestPath))
	if err != nil {
		t.Fatalf("reading %s: %v", statusManifestPath, err)
	}
	var decoded manifestStatusDocument
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding %s: %v", statusManifestPath, err)
	}
	if len(decoded.Tools) == 0 {
		t.Fatalf("%s describes no tools", statusManifestPath)
	}
	return decoded
}

// TestManifestStatusMatchesTheRegisteredSurface pins the manifest's status in both
// directions.
//
// A record marked implemented that this package does not register is a manifest that
// overstates the surface; a registered manifest tool still marked not-implemented is a
// manifest that has fallen behind the code. Both failures name the drifted tools,
// because a bare count would leave the next reader to diff 138 records by hand.
func TestManifestStatusMatchesTheRegisteredSurface(t *testing.T) {
	t.Parallel()

	document := loadManifestStatuses(t)
	contracts := tools.Contracts()

	var overstated, understated []string
	for _, entry := range document.Tools {
		_, registered := contracts[entry.Name]
		switch {
		case entry.Status == statusImplemented && !registered:
			overstated = append(overstated, entry.Name)
		case entry.Status != statusImplemented && registered:
			understated = append(understated, entry.Name)
		}
	}

	if len(overstated) > 0 {
		t.Errorf("%s marks these tools %q, but this package registers none of them: %v",
			statusManifestPath, statusImplemented, overstated)
	}
	if len(understated) > 0 {
		t.Errorf("%s does not mark these registered tools %q: %v",
			statusManifestPath, statusImplemented, understated)
	}
}

// TestEveryImplementedManifestRecordNamesItsGoRegistration keeps the status honest at
// the record level: a status of implemented with an empty Go block says nothing about
// where the tool lives, which is the exact shape the drift took before.
func TestEveryImplementedManifestRecordNamesItsGoRegistration(t *testing.T) {
	t.Parallel()

	for _, entry := range loadManifestStatuses(t).Tools {
		if entry.Status != statusImplemented {
			if entry.Go != (manifestStatusEntry{}).Go {
				t.Errorf("%s: status is %q but the go block names a registration: %+v",
					entry.Name, entry.Status, entry.Go)
			}
			continue
		}
		if entry.Go.Package == "" || entry.Go.Handler == "" || entry.Go.File == "" {
			t.Errorf("%s: status is %q but the go block is incomplete: %+v",
				entry.Name, statusImplemented, entry.Go)
		}
		if entry.Go.RegisteredName != entry.Name {
			t.Errorf("%s: go.registeredName = %q, want the wire name %q",
				entry.Name, entry.Go.RegisteredName, entry.Name)
		}
	}
}

// TestTheManifestStatusCountsMatchTheRecords keeps the summary block from drifting away
// from the array it summarises.
func TestTheManifestStatusCountsMatchTheRecords(t *testing.T) {
	t.Parallel()

	document := loadManifestStatuses(t)
	counted := make(map[string]int, len(document.Counts.ByStatus))
	for _, entry := range document.Tools {
		counted[entry.Status]++
	}

	for status, want := range document.Counts.ByStatus {
		if got := counted[status]; got != want {
			t.Errorf("counts.byStatus[%q] = %d, the tool array holds %d", status, want, got)
		}
	}
	for status, got := range counted {
		if _, stated := document.Counts.ByStatus[status]; !stated {
			t.Errorf("counts.byStatus omits %q, which %d tools carry", status, got)
		}
	}
}

// TestEveryRegisteredToolIsEitherAManifestRecordOrADeclaredAddition is the third
// direction: a registered tool absent from the manifest must be one of the additions
// the contract test already declares, so a new tool cannot silently escape both files.
func TestEveryRegisteredToolIsEitherAManifestRecordOrADeclaredAddition(t *testing.T) {
	t.Parallel()

	document := loadManifestStatuses(t)
	named := make([]string, 0, len(document.Tools))
	for _, entry := range document.Tools {
		named = append(named, entry.Name)
	}
	additions := additionsBeyondTheManifest()

	for name := range tools.Contracts() {
		if slices.Contains(named, name) {
			continue
		}
		if _, declared := additions[name]; !declared {
			t.Errorf("%q is registered but is neither a record in %s nor a declared addition",
				name, statusManifestPath)
		}
	}
}
