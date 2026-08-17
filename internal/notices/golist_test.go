package notices

import (
	"context"
	"strings"
	"testing"
)

// TestReleasedTargetsMatchTheGoReleaserMatrix pins the selection rule the
// notices file states in its own header: two operating systems crossed with two
// architectures. The union over exactly this matrix is what makes the set
// complete, and it must stay in step with .goreleaser.yaml — a target that ships
// an archive but is missing here would take that archive's notices with it.
//
// Windows was dropped deliberately, and this is where the consequence shows:
// Cobra imports inconshreveable/mousetrap only for GOOS=windows, so with the
// target gone that module leaves the linked set and its Apache-2.0 notice leaves
// THIRD_PARTY_NOTICES.md. Nothing ships the code any more, so nothing owes the
// notice.
func TestReleasedTargetsMatchTheGoReleaserMatrix(t *testing.T) {
	want := map[string][]string{
		osLinux:  {archAMD64, archARM64},
		osDarwin: {archAMD64, archARM64},
	}

	got := make(map[string][]string)
	for _, target := range releasedTargets {
		got[target.GOOS] = append(got[target.GOOS], target.GOARCH)
	}

	if len(got) != len(want) {
		t.Fatalf("releasedTargets covers %d operating systems, want %d", len(got), len(want))
	}

	for goos, arches := range want {
		if strings.Join(got[goos], ",") != strings.Join(arches, ",") {
			t.Errorf("releasedTargets[%s] = %v, want %v", goos, got[goos], arches)
		}
	}
}

// TestParseModuleLinesDropsStandardLibraryRecords covers the shape `go list`
// actually produces: standard library packages carry no module, so the template
// emits a blank line for each of them. Those lines are noise, not an error.
func TestParseModuleLinesDropsStandardLibraryRecords(t *testing.T) {
	out := "\n\n" + pflagModule + "\tv1.0.10\t/cache/pflag\n\ngolang.org/x/sys\tv0.47.0\t/cache/sys\n"

	mods, err := parseModuleLines(out)
	if err != nil {
		t.Fatalf("parseModuleLines: %v", err)
	}

	if len(mods) != 2 {
		t.Fatalf("parseModuleLines returned %d modules, want 2", len(mods))
	}

	if mods[0].Path != pflagModule || mods[0].Version != "v1.0.10" {
		t.Errorf("first module = %+v, want pflag v1.0.10", mods[0])
	}
}

// TestParseModuleLinesRejectsAnIncompleteRecord keeps a half-filled record from
// travelling on. A module with a blank version would render a licence section
// attributed to no version at all, which is worse than a hard failure because it
// still looks like a complete notice.
func TestParseModuleLinesRejectsAnIncompleteRecord(t *testing.T) {
	for _, line := range []string{
		pflagModule + "\t\t/cache/pflag",
		"\tv1.0.10\t/cache/pflag",
		"github.com/spf13/pflag\tv1.0.10",
	} {
		if _, err := parseModuleLines(line + "\n"); err == nil {
			t.Errorf("parseModuleLines(%q) accepted an incomplete record, want an error", line)
		}
	}
}

// TestLinkedModulesReportsTheSortedUnion is the end-to-end shape check on the
// dependency set: sorted by module path, free of the main module, and carrying a
// cache directory for every entry so no licence lookup can start from a blank.
func TestLinkedModulesReportsTheSortedUnion(t *testing.T) {
	requireResolvableModuleGraph(t)

	mods, err := linkedModules(context.Background(), repoRoot)
	if err != nil {
		t.Fatalf("linkedModules: %v", err)
	}

	for i, mod := range mods {
		if mod.Path == "github.com/tamcore/garmin-mcp" {
			t.Error("linkedModules included the main module")
		}

		if mod.Dir == "" {
			t.Errorf("module %s has no cache directory", mod.Path)
		}

		if i > 0 && mods[i-1].Path >= mod.Path {
			t.Errorf("modules out of order: %s before %s", mods[i-1].Path, mod.Path)
		}
	}

	// go-isatty is linked on darwin and not on linux, so its presence is what
	// proves the union is taken rather than a single platform's answer. This used
	// to be mousetrap, which Cobra imports on Windows alone; that target is gone,
	// so the canary had to move to a platform that still ships.
	if !containsPath(mods, "github.com/mattn/go-isatty") {
		t.Error("union is missing the darwin-only module github.com/mattn/go-isatty, " +
			"so it is one platform's answer rather than the union")
	}

	// And the inverse, which is the new invariant: nothing Windows-only may appear.
	// A mousetrap notice in the file would mean an archive owes an Apache-2.0
	// notice for code no target ships.
	if containsPath(mods, "github.com/inconshreveable/mousetrap") {
		t.Error("union contains the Windows-only module github.com/inconshreveable/mousetrap, " +
			"but Windows is not a released target")
	}
}

// containsPath reports whether the set holds a module path.
func containsPath(mods []module, path string) bool {
	for _, mod := range mods {
		if mod.Path == path {
			return true
		}
	}

	return false
}
