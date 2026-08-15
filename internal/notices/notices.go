// Package notices regenerates THIRD_PARTY_NOTICES.md from the module cache.
//
// The notices file is a distribution condition, not documentation: the release
// archives carry it through .goreleaser.yaml and the container image carries it
// under /licenses/garmin-mcp/. It was originally produced by a throwaway script,
// which meant a dependency bump could leave it silently wrong. This package is
// the generator that replaces that script, and the freshness test beside it is
// what turns a stale file into a failing build.
//
// Two rules shape the design.
//
// First, nothing is inferred. A licence identifier is a human reading of a
// licence text, so the SPDX identifiers, the per-module prose, and the list of
// licence files each module ships live in a curated registry in this package.
// The generator only copies bytes and checks the curation against reality: a
// module with no registry entry, a registry entry for a module that is no longer
// linked, or a licence file present in the cache but absent from the entry is a
// hard error naming the module. It never emits a placeholder and never guesses.
//
// Second, the dependency set is the linked set, not the go.mod requirement set.
// It is the union of `go list -deps ./cmd/garmin-mcp` over the six released
// GOOS/GOARCH targets under CGO_ENABLED=0, which is what the existing file's own
// header claims and what reproduces its 32 modules. Test-only and tool-only
// requirements reach no artifact and are excluded; a module only one platform
// links — mousetrap, which Cobra imports on Windows alone — is still covered.
package notices

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Options configures generation. The zero value is usable from the repository
// root.
type Options struct {
	// Dir is the module root the dependency set is resolved from. Empty means
	// the current working directory.
	Dir string
}

// Generate returns the complete contents of THIRD_PARTY_NOTICES.md.
//
// The output depends only on the module graph and the module cache, so two runs
// against one tree are byte-identical: there is no timestamp, no absolute path,
// and no host-specific value anywhere in it.
func Generate(ctx context.Context, opts Options) ([]byte, error) {
	mods, err := linkedModules(ctx, opts.Dir)
	if err != nil {
		return nil, err
	}

	entries, err := describe(mods)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer

	out.WriteString(preamble)
	out.WriteString("\n")

	writeModuleTable(&out, entries)

	if err := writeLicenceTexts(&out, entries); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

// entry is one rendered module: the resolved coordinates from the module graph
// joined to its curated notice.
type entry struct {
	module module
	notice moduleNotice
}

// describe joins the linked module set to the curated registry, failing on any
// disagreement between the two. Both directions matter: an unregistered module
// would otherwise ship with no licence text, and a registered module that is no
// longer linked means the registry is carrying a stale claim.
func describe(mods []module) ([]entry, error) {
	entries := make([]entry, 0, len(mods))
	seen := make(map[string]struct{}, len(mods))

	for _, mod := range mods {
		notice, ok := moduleNotices[mod.Path]
		if !ok {
			return nil, fmt.Errorf(
				"notices: module %s reaches the released binary but has no registry entry: "+
					"read its licence yourself and add it to moduleNotices in internal/notices/registry.go",
				mod.Path)
		}

		if err := verifyLicenceFiles(mod, notice); err != nil {
			return nil, err
		}

		seen[mod.Path] = struct{}{}
		entries = append(entries, entry{module: mod, notice: notice})
	}

	stale := make([]string, 0)

	for path := range moduleNotices {
		if _, ok := seen[path]; !ok {
			stale = append(stale, path)
		}
	}

	if len(stale) > 0 {
		slices.Sort(stale)

		return nil, fmt.Errorf(
			"notices: registry entries for modules no longer linked into the binary: %s: "+
				"remove them from moduleNotices in internal/notices/registry.go",
			strings.Join(stale, ", "))
	}

	return entries, nil
}

// verifyLicenceFiles checks that the curated file list names exactly the
// licence-bearing files the module actually ships. A file appearing upstream
// that the registry does not name is the dangerous case — it is a term the
// notices would omit — so it fails rather than being copied unreviewed.
func verifyLicenceFiles(mod module, notice moduleNotice) error {
	found, err := licenceFiles(mod.Dir)
	if err != nil {
		return fmt.Errorf("notices: scanning %s: %w", mod.Path, err)
	}

	want := slices.Clone(notice.Files)
	slices.Sort(want)

	if !slices.Equal(found, want) {
		return fmt.Errorf(
			"notices: module %s ships licence files [%s] but the registry names [%s]: "+
				"read the difference and update moduleNotices in internal/notices/registry.go",
			mod.Path, strings.Join(found, " "), strings.Join(want, " "))
	}

	return nil
}

// licenceFiles reports the sorted names of the licence-bearing files at the root
// of a module directory. Subdirectories are not scanned: a licence deeper in a
// tree belongs to material that module's own notices cover.
func licenceFiles(dir string) ([]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("module is absent from the module cache: run go mod download")
	}

	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, 4)

	for _, de := range des {
		if de.IsDir() || !isLicenceFileName(de.Name()) {
			continue
		}

		names = append(names, de.Name())
	}

	slices.Sort(names)

	return names, nil
}

// isLicenceFileName reports whether a root-level file name carries licence
// terms. AUTHORS, CONTRIBUTORS and CONTRIBUTING are deliberately excluded: they
// record who wrote the code, not under what terms it may be redistributed.
//
// The match on LICENSE is a substring rather than a prefix because a module may
// qualify the name on either side: modernc.org/memory ships LICENSE-MMAP-GO and
// modernc.org/sqlite ships SQLITE-LICENSE, and a prefix match would have missed
// the SQLite public-domain dedication entirely. A false positive here costs a
// human one line in the registry; a false negative drops a licence from a file
// that ships as a distribution condition.
func isLicenceFileName(name string) bool {
	upper := strings.ToUpper(name)

	if strings.HasSuffix(upper, ".GO") {
		return false
	}

	if upper == filePatents || strings.Contains(upper, fileLicense) || strings.Contains(upper, "LICENCE") {
		return true
	}

	return strings.HasPrefix(upper, fileNotice) || strings.HasPrefix(upper, "COPYING")
}

// writeModuleTable renders the summary section. The module count is derived, so
// it can never disagree with the rows above it.
func writeModuleTable(out *bytes.Buffer, entries []entry) {
	out.WriteString("## Go modules in the released binary\n\n")
	out.WriteString(moduleSetIntro)
	out.WriteString("\n| Module | Version | SPDX |\n")
	out.WriteString("|--------|---------|------|\n")

	for _, e := range entries {
		fmt.Fprintf(out, "| `%s` | `%s` | `%s` |\n", e.module.Path, e.module.Version, e.notice.SPDX)
	}

	fmt.Fprintf(out,
		"\n%d modules. Every licence was read from the module cache;\nnone was inferred.\n\n",
		len(entries))
}

// writeLicenceTexts renders one section per module, each licence file inside a
// fenced block. The bytes between the fences are the file's bytes: nothing is
// trimmed, re-wrapped or re-indented, because the header of this document
// promises the texts are reproduced byte-for-byte.
func writeLicenceTexts(out *bytes.Buffer, entries []entry) error {
	out.WriteString("## Full licence texts\n")

	for _, e := range entries {
		fmt.Fprintf(out, "\n### `%s` %s\n\n", e.module.Path, e.module.Version)
		fmt.Fprintf(out, "SPDX identifier: `%s`\n", e.notice.SPDX)

		if e.notice.Note != "" {
			fmt.Fprintf(out, "\n%s\n", e.notice.Note)
		}

		for _, name := range e.notice.Files {
			text, err := readLicence(e.module, name)
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "\n`%s`:\n\n```text\n%s```\n", name, text)
		}
	}

	return nil
}

// readLicence reads one licence file verbatim.
//
// The single normalisation is the run of newlines at the very end, which is
// collapsed to one so the closing fence sits on its own line. It touches no
// other byte: not the wrapping, not the indentation, not the interior blank
// lines. Only one file in the current set is affected — modernc.org/memory's
// LICENSE-MMAP-GO ends with a blank line — and dropping a trailing empty line
// removes no licence term, so the document's byte-for-byte promise holds for
// every line that carries text.
func readLicence(mod module, name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(mod.Dir, name))
	if err != nil {
		return "", fmt.Errorf("notices: reading %s %s: %w", mod.Path, name, err)
	}

	text := strings.TrimRight(string(raw), "\n")
	if text == "" {
		return "", fmt.Errorf("notices: %s %s is empty: refusing to emit an empty licence", mod.Path, name)
	}

	return text + "\n", nil
}
