package tools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const resourceManifestPath = "../../compat/resources.json"

// manifestUpstream is the provenance block both compat manifests carry.
type manifestUpstream struct {
	Upstream struct {
		Commit                       string `json:"commit"`
		GarminconnectReferenceCommit string `json:"garminconnectReferenceCommit"`
	} `json:"upstream"`
}

// TestUpstreamPinsAgreeBetweenBothManifests fails when the tool and resource
// manifests describe different upstream commits, so a partial pin bump cannot
// leave the contract tests validating against two snapshots at once.
func TestUpstreamPinsAgreeBetweenBothManifests(t *testing.T) {
	tools := loadManifestUpstream(t, manifestPath)
	resources := loadManifestUpstream(t, resourceManifestPath)

	if tools.Upstream.Commit == "" || tools.Upstream.GarminconnectReferenceCommit == "" {
		t.Fatalf("compat/tools.json records an empty upstream commit")
	}
	if tools.Upstream.Commit != resources.Upstream.Commit {
		t.Fatalf("compat/tools.json pins Taxuspt %s but compat/resources.json pins %s: "+
			"the two manifests describe different upstream commits",
			tools.Upstream.Commit, resources.Upstream.Commit)
	}
	if tools.Upstream.GarminconnectReferenceCommit != resources.Upstream.GarminconnectReferenceCommit {
		t.Fatalf("the two manifests pin different python-garminconnect commits: %s and %s",
			tools.Upstream.GarminconnectReferenceCommit,
			resources.Upstream.GarminconnectReferenceCommit)
	}
}

// loadManifestUpstream reads just the provenance block of a compat manifest.
func loadManifestUpstream(t *testing.T, path string) manifestUpstream {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var decoded manifestUpstream
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return decoded
}
