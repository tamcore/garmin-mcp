package notices

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// repoRoot is the module root relative to this package's directory, which is
// where `go test` runs.
const repoRoot = "../.."

// pflagModule is a module the registry really carries, used where a test needs
// a path that resolves.
const pflagModule = "github.com/spf13/pflag"

// noticesPath is the checked-in file this package regenerates.
var noticesPath = filepath.Join(repoRoot, "THIRD_PARTY_NOTICES.md")

// TestCheckedInNoticesMatchTheGeneratedFile is the freshness gate.
//
// THIRD_PARTY_NOTICES.md is not documentation. The release archives ship it
// through .goreleaser.yaml, the container image carries it under
// /licenses/garmin-mcp/, and CI asserts it is present and non-empty inside the
// image. It is therefore a distribution condition of the terms of every module
// in it.
//
// Before this test the file was produced once by a script that no longer exists,
// so a dependency bump could add a module, change a version, or replace a
// licence text and leave the shipped notices quietly wrong — attributing code to
// terms that no longer apply, and omitting terms that now do. Nothing anywhere
// caught that.
//
// This test is what catches it: it regenerates the file from the module graph
// and the module cache and requires the checked-in bytes to match exactly. The
// fix is never to edit the expectation; it is to run `go run ./cmd/notices` and
// commit the result in the same commit as the dependency change.
func TestCheckedInNoticesMatchTheGeneratedFile(t *testing.T) {
	requireResolvableModuleGraph(t)

	generated, err := Generate(context.Background(), Options{Dir: repoRoot})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	checkedIn, err := os.ReadFile(noticesPath)
	if err != nil {
		t.Fatalf("reading %s: %v", noticesPath, err)
	}

	if bytes.Equal(checkedIn, generated) {
		return
	}

	t.Fatalf("THIRD_PARTY_NOTICES.md is stale: regenerate it with `go run ./cmd/notices` "+
		"and commit it together with the dependency change\n%s",
		firstDifference(checkedIn, generated))
}

// TestGenerateIsDeterministic pins the property the freshness gate depends on.
//
// A generator that embedded a timestamp, a host path, or a map iteration order
// would fail the gate above on runs where nothing changed, and that would train
// maintainers to regenerate reflexively — which is how a real drift gets
// committed unread. Two runs over one tree must produce one byte sequence.
func TestGenerateIsDeterministic(t *testing.T) {
	requireResolvableModuleGraph(t)

	ctx := context.Background()

	first, err := Generate(ctx, Options{Dir: repoRoot})
	if err != nil {
		t.Fatalf("Generate (first): %v", err)
	}

	second, err := Generate(ctx, Options{Dir: repoRoot})
	if err != nil {
		t.Fatalf("Generate (second): %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Fatalf("two runs differ:\n%s", firstDifference(first, second))
	}
}

// TestGeneratedNoticesCarryNoAbsolutePath guards the one host-specific value the
// generator handles. The module cache directory is needed to read the licence
// texts and must never reach the output: a notices file that names a
// maintainer's home directory is both noise and a diff that changes per machine.
func TestGeneratedNoticesCarryNoAbsolutePath(t *testing.T) {
	requireResolvableModuleGraph(t)

	generated, err := Generate(context.Background(), Options{Dir: repoRoot})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	cache := goEnv("GOMODCACHE")
	if cache == "" {
		t.Skip("go env GOMODCACHE is empty, so there is no path to look for")
	}

	if bytes.Contains(generated, []byte(cache)) {
		t.Errorf("generated notices contain the module cache path %q", cache)
	}
}

// TestDescribeRejectsAnUnregisteredModule pins the fail-loud rule. A module that
// reaches the binary with no curated entry has no reviewed licence, and emitting
// a placeholder for it would be a false attribution statement in a file that
// ships. It must stop the run and name the module.
func TestDescribeRejectsAnUnregisteredModule(t *testing.T) {
	dir := moduleDirWithLicence(t, fileLicense)

	_, err := describe([]module{{Path: "example.com/unregistered", Version: "v1.0.0", Dir: dir}})
	if err == nil {
		t.Fatal("describe accepted a module with no registry entry, want an error")
	}

	if !strings.Contains(err.Error(), "example.com/unregistered") {
		t.Errorf("error does not name the offending module: %v", err)
	}
}

// TestDescribeRejectsAStaleRegistryEntry covers the other direction. A registry
// entry for a module the binary no longer links means the notices would claim to
// distribute something they do not, so the registry is wrong and must be pruned
// rather than silently ignored.
func TestDescribeRejectsAStaleRegistryEntry(t *testing.T) {
	dir := moduleDirWithLicence(t, fileLicense)

	// One real module, so every other registry entry is left unmatched.
	_, err := describe([]module{{Path: pflagModule, Version: "v1.0.10", Dir: dir}})
	if err == nil {
		t.Fatal("describe accepted a registry with unmatched entries, want an error")
	}

	if !strings.Contains(err.Error(), "no longer linked") {
		t.Errorf("error does not explain the stale entry: %v", err)
	}
}

// TestVerifyLicenceFilesRejectsAnUnreviewedLicenceFile is the regression that
// matters most on a version bump: upstream adds a second licence file, the
// registry still names one, and the notices would ship missing a term nobody
// read. The scan must refuse rather than reproduce only what it was told about.
func TestVerifyLicenceFilesRejectsAnUnreviewedLicenceFile(t *testing.T) {
	dir := moduleDirWithLicence(t, fileLicense, "LICENSE-VENDORED")

	err := verifyLicenceFiles(
		module{Path: "example.com/two", Version: "v1.0.0", Dir: dir},
		moduleNotice{SPDX: spdxMIT, Files: []string{fileLicense}},
	)
	if err == nil {
		t.Fatal("verifyLicenceFiles accepted an unreviewed licence file, want an error")
	}

	if !strings.Contains(err.Error(), "LICENSE-VENDORED") {
		t.Errorf("error does not name the unreviewed file: %v", err)
	}
}

// TestLicenceFileNameMatching documents which root-level names count as licence
// terms. The qualified-name cases are not hypothetical: modernc.org/sqlite ships
// SQLITE-LICENSE and modernc.org/memory ships LICENSE-MMAP-GO, and a prefix-only
// match dropped the SQLite public-domain dedication from the output while this
// generator was being written.
func TestLicenceFileNameMatching(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{fileLicense, true},
		{fileLicenseTxt, true},
		{"LICENCE", true},
		{"license", true},
		{"LICENSE-GO", true},
		{"LICENSE-MMAP-GO", true},
		{"LICENSE-3RD-PARTY.md", true},
		{"SQLITE-LICENSE", true},
		{fileNotice, true},
		{filePatents, true},
		{"COPYING", true},
		{"AUTHORS", false},
		{"CONTRIBUTORS", false},
		{"CONTRIBUTING.md", false},
		{"README.md", false},
		{"license_test.go", false},
	} {
		if got := isLicenceFileName(tc.name); got != tc.want {
			t.Errorf("isLicenceFileName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestReadLicenceRefusesAnEmptyFile keeps the generator from emitting an empty
// fenced block, which would read as "these are the terms" while stating nothing.
func TestReadLicenceRefusesAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileLicense), []byte("\n\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := readLicence(module{Path: "example.com/empty", Dir: dir}, fileLicense); err == nil {
		t.Fatal("readLicence accepted an empty licence, want an error")
	}
}

// TestReadLicenceCollapsesOnlyTheTrailingNewlines pins the single normalisation
// the generator performs, so a later change cannot start trimming interior blank
// lines or trailing spaces out of a licence text that the document promises is
// reproduced byte-for-byte.
func TestReadLicenceCollapsesOnlyTheTrailingNewlines(t *testing.T) {
	dir := t.TempDir()
	raw := "Terms.\n\n  indented   \n\n\n"

	if err := os.WriteFile(filepath.Join(dir, fileLicense), []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := readLicence(module{Path: "example.com/spacing", Dir: dir}, fileLicense)
	if err != nil {
		t.Fatalf("readLicence: %v", err)
	}

	if want := "Terms.\n\n  indented   \n"; got != want {
		t.Errorf("readLicence = %q, want %q", got, want)
	}
}

// requireResolvableModuleGraph skips instead of failing when the environment
// genuinely cannot answer the question. The module graph needs the toolchain and
// a populated module cache; without them the test proves nothing either way, and
// a spurious failure would teach maintainers to ignore this gate. Once the probe
// passes, any later error is a real defect and fails.
func requireResolvableModuleGraph(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain in PATH: cannot resolve the module graph")
	}

	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", mainPackage)
	cmd.Dir = repoRoot

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("go list cannot resolve %s, so notices freshness cannot be checked here "+
			"(run `go mod download` with network access): %v: %s",
			mainPackage, err, strings.TrimSpace(string(out)))
	}
}

// goEnv reports one `go env` value, or "" when it cannot be read.
func goEnv(key string) string {
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

// moduleDirWithLicence builds a throwaway module directory holding the named
// licence files, so the registry checks can be exercised without the cache.
func moduleDirWithLicence(t *testing.T, names ...string) string {
	t.Helper()

	dir := t.TempDir()

	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("Terms.\n"), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	return dir
}

// firstDifference reports the first differing line with its number, so a
// stale-notices failure names the module that moved instead of dumping a
// hundred-kilobyte diff into the test log.
func firstDifference(want, got []byte) string {
	wantLines := strings.Split(string(want), "\n")
	gotLines := strings.Split(string(got), "\n")

	for i := range max(len(wantLines), len(gotLines)) {
		w, g := lineAt(wantLines, i), lineAt(gotLines, i)
		if w == g {
			continue
		}

		return "first difference at line " + strconv.Itoa(i+1) +
			":\n  checked in: " + w + "\n  generated:  " + g
	}

	return "files differ in length only"
}

// lineAt reports one line, or a marker when that file ended first.
func lineAt(lines []string, i int) string {
	if i >= len(lines) {
		return "<end of file>"
	}

	return lines[i]
}
