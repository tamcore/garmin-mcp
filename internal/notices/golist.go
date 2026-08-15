package notices

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// buildTarget is one released GOOS/GOARCH pair. The list mirrors the builds
// matrix in .goreleaser.yaml. It is spelled out rather than derived from that
// file because the notices must describe what was released, and a YAML parse
// would make the notices depend on a second parser for no gain.
type buildTarget struct {
	GOOS   string
	GOARCH string
}

// The released operating systems and architectures, named so the matrix below
// and the test that pins it cannot drift apart on a typo.
const (
	osLinux   = "linux"
	osDarwin  = "darwin"
	osWindows = "windows"

	archAMD64 = "amd64"
	archARM64 = "arm64"
)

// releasedTargets is the six-target matrix the release archives cover.
var releasedTargets = []buildTarget{
	{GOOS: osLinux, GOARCH: archAMD64},
	{GOOS: osLinux, GOARCH: archARM64},
	{GOOS: osDarwin, GOARCH: archAMD64},
	{GOOS: osDarwin, GOARCH: archARM64},
	{GOOS: osWindows, GOARCH: archAMD64},
	{GOOS: osWindows, GOARCH: archARM64},
}

// mainPackage is the only package whose linked dependencies the notices cover.
// The generator itself, the tests, and the tools are outside it by construction,
// which is exactly the selection rule the notices file states.
const mainPackage = "./cmd/garmin-mcp"

// module is a dependency of the released binary, as the module graph reports it.
type module struct {
	Path    string
	Version string
	// Dir is the module cache directory the licence texts are read from. It is
	// host-specific and never appears in the generated output.
	Dir string
}

// linkedModules reports every module the released binary links, sorted by module
// path, as the union over the six released targets.
//
// The union is what makes the set honest. A single `go list` run answers for one
// platform only, so a Windows-only import — Cobra's use of mousetrap — would be
// missing from a set derived on Linux, and its licence would silently drop out
// of an artifact that actually contains its code.
func linkedModules(ctx context.Context, dir string) ([]module, error) {
	union := make(map[string]module)

	for _, target := range releasedTargets {
		mods, err := listModules(ctx, dir, target)
		if err != nil {
			return nil, err
		}

		for _, mod := range mods {
			existing, ok := union[mod.Path]
			if ok && existing.Version != mod.Version {
				return nil, fmt.Errorf(
					"notices: module %s resolves to %s and %s across the released targets: "+
						"the notices cannot state one version for it",
					mod.Path, existing.Version, mod.Version)
			}

			union[mod.Path] = mod
		}
	}

	mods := make([]module, 0, len(union))
	for _, mod := range union {
		mods = append(mods, mod)
	}

	slices.SortFunc(mods, func(a, b module) int { return strings.Compare(a.Path, b.Path) })

	if len(mods) == 0 {
		return nil, fmt.Errorf("notices: go list reported no dependency modules for %s", mainPackage)
	}

	return mods, nil
}

// listFormat emits one tab-separated record per non-stdlib, non-main package.
// Standard library packages carry no module and produce an empty line, which the
// caller drops.
const listFormat = `{{if .Module}}{{if not .Module.Main}}` +
	`{{.Module.Path}}{{"\t"}}{{.Module.Version}}{{"\t"}}{{.Module.Dir}}` +
	`{{end}}{{end}}`

// listModules runs `go list -deps` for one target. CGO_ENABLED=0 matches the
// release build: cgo would change which files, and therefore which imports, are
// selected.
func listModules(ctx context.Context, dir string, target buildTarget) ([]module, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-f", listFormat, mainPackage)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GOOS="+target.GOOS,
		"GOARCH="+target.GOARCH,
		"CGO_ENABLED=0",
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("notices: go list for %s/%s: %w: %s",
			target.GOOS, target.GOARCH, err, strings.TrimSpace(stderr.String()))
	}

	return parseModuleLines(string(out))
}

// parseModuleLines turns the tab-separated `go list` output into modules,
// rejecting any record the format did not fill in rather than carrying a blank
// through into a licence lookup.
func parseModuleLines(out string) ([]module, error) {
	mods := make([]module, 0, 32)

	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] == "" || fields[1] == "" {
			return nil, fmt.Errorf("notices: unusable go list record %q", line)
		}

		mods = append(mods, module{Path: fields[0], Version: fields[1], Dir: fields[2]})
	}

	return mods, nil
}
