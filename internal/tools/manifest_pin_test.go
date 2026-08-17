package tools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The three places an upstream pin is written down, which must never disagree.
const (
	upstreamPinsDoc      = "../../docs/upstream-pins.md"
	resourceManifestPath = "../../compat/resources.json"
)

// manifestUpstream is the provenance block both compat manifests carry.
type manifestUpstream struct {
	Upstream struct {
		Commit                       string `json:"commit"`
		GarminconnectReferenceCommit string `json:"garminconnectReferenceCommit"`
	} `json:"upstream"`
}

// TestUpstreamPinsAgreeBetweenTheDocAndBothManifests couples the recorded pin to
// the artifacts derived from it.
//
// This is the part of "manifest drift against a new upstream pin" that can be
// checked without fetching anything: a pin bumped in docs/upstream-pins.md while
// compat/tools.json and compat/resources.json still describe the OLD commit means
// every contract test in this package is now validating the server against a
// snapshot of something else, while the documentation claims otherwise. That is
// the failure mode a pin bump actually has, and it is silent today.
//
// What this deliberately does NOT do is re-derive the manifests from upstream
// source. That needs a committed extractor which provably reproduces the reviewed
// artifacts first, and the manifests have been corrected by hand since they were
// generated — so a generator that disagreed would invite "fixing" it by
// overwriting reviewed content that 137 contract tests depend on. See
// docs/parity.md for why that regeneration stays a documented manual procedure.
func TestUpstreamPinsAgreeBetweenTheDocAndBothManifests(t *testing.T) {
	tools := loadManifestUpstream(t, manifestPath)
	resources := loadManifestUpstream(t, resourceManifestPath)

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

	raw, err := os.ReadFile(filepath.Clean(upstreamPinsDoc))
	if err != nil {
		t.Fatalf("reading %s: %v", upstreamPinsDoc, err)
	}
	doc := string(raw)

	for _, pin := range []struct {
		what   string
		commit string
	}{
		{what: "Taxuspt/garmin_mcp", commit: tools.Upstream.Commit},
		{what: "cyberjunky/python-garminconnect", commit: tools.Upstream.GarminconnectReferenceCommit},
	} {
		if pin.commit == "" {
			t.Fatalf("the manifests record no commit for %s", pin.what)
		}
		if !strings.Contains(doc, pin.commit) {
			t.Fatalf("%s pins %s at a commit the manifests do not carry: the manifests say %s, "+
				"which that file never mentions. Either the pin was bumped without regenerating "+
				"the manifests — in which case every contract test here now validates against a "+
				"snapshot of a different commit — or the manifests were regenerated without "+
				"recording the new pin. Follow the procedure in docs/parity.md.",
				upstreamPinsDoc, pin.what, pin.commit)
		}
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
