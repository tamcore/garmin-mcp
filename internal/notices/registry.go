package notices

import _ "embed"

// preamble is the hand-maintained head of the document: the licensing statement
// and the two upstream compatibility references, whose licence texts come from
// pinned git commits rather than from the module cache and therefore cannot be
// regenerated. It is embedded verbatim so that regenerating the notices never
// rewrites prose a human wrote, and so that editing that prose stays an ordinary
// file edit rather than a Go string edit.
//
//go:embed preamble.md
var preamble string

// moduleSetIntro states the selection rule for the module table. It is prose
// about how the set is derived, so it is fixed text; only the table and the
// count below it are computed.
const moduleSetIntro = "The set below is every module that reaches a released binary. It was\n" +
	"derived with `go list -deps ./cmd/garmin-mcp` under `CGO_ENABLED=0` for\n" +
	"each released target — `linux`, `darwin` and `windows` crossed with\n" +
	"`amd64` and `arm64`, matching the `builds` matrix in `.goreleaser.yaml` —\n" +
	"and unioned, so a module that only a single platform links is still\n" +
	"covered. It is the linked set, not the `go.mod` requirement set:\n" +
	"build-only and test-only requirements are excluded because they reach no\n" +
	"artifact.\n"

// SPDX identifiers used by the registry. They are named so the table below
// states a licence once and cannot drift into a typo that no reader would catch.
const (
	spdxMIT       = "MIT"
	spdxBSD3      = "BSD-3-Clause"
	spdxApache2   = "Apache-2.0"
	spdxSDKMixed  = "Apache-2.0 AND MIT"
	spdxYAMLMixed = "MIT AND Apache-2.0"
)

// Licence file names the registry refers to more than once.
const (
	fileLicense    = "LICENSE"
	fileLicenseTxt = "LICENSE.txt"
	fileNotice     = "NOTICE"
	filePatents    = "PATENTS"
)

// moduleNotice is the curated record for one module: what a human concluded
// after reading its licence files, and which files those are.
//
// None of this can be derived. Go publishes no licence metadata, and guessing an
// SPDX identifier from a text is exactly the inference the notices file promises
// it never made. The generator therefore treats this table as the authority for
// what a module's terms are, and treats the module cache as the authority for
// whether the table is still complete.
type moduleNotice struct {
	// SPDX identifies the terms. It is not always a single identifier: three
	// modules carry more than one licence, and the Note explains each.
	SPDX string
	// Note is optional prose placed above the licence texts, for a module whose
	// SPDX field alone would mislead.
	Note string
	// Files names the licence-bearing files to reproduce, in the order they are
	// reproduced. The generator fails when the module ships a licence file this
	// list does not name, so a new upstream licence file cannot slip through
	// unreviewed.
	Files []string
}

// licenseOnly is the shape of the overwhelming majority: one LICENSE file and
// nothing else to say about it.
func licenseOnly(spdx string) moduleNotice {
	return moduleNotice{SPDX: spdx, Files: []string{fileLicense}}
}

// moduleNotices is the curated registry, keyed by module path. Versions are
// deliberately absent: a patch bump must not require a second edit here, and the
// generator reads the version from the module graph. The consequence is that a
// bump which *changes* a module's terms needs a human to notice, which is why
// docs/dependencies.md makes re-reading the licence part of a dependency bump.
var moduleNotices = map[string]moduleNotice{
	"github.com/dustin/go-humanize":        licenseOnly(spdxMIT),
	"github.com/fsnotify/fsnotify":         licenseOnly(spdxBSD3),
	"github.com/go-viper/mapstructure/v2":  licenseOnly(spdxMIT),
	"github.com/google/jsonschema-go":      licenseOnly(spdxMIT),
	"github.com/google/uuid":               licenseOnly(spdxBSD3),
	"github.com/inconshreveable/mousetrap": licenseOnly(spdxApache2),
	"github.com/mattn/go-isatty":           licenseOnly(spdxMIT),
	"github.com/ncruces/go-strftime":       licenseOnly(spdxMIT),
	"github.com/pelletier/go-toml/v2":      licenseOnly(spdxMIT),
	"github.com/remyoudompheng/bigfft":     licenseOnly(spdxBSD3),
	"github.com/sagikazarmark/locafero":    licenseOnly(spdxMIT),
	"github.com/segmentio/asm":             licenseOnly(spdxMIT),
	"github.com/segmentio/encoding":        licenseOnly(spdxMIT),
	"github.com/sourcegraph/conc":          licenseOnly(spdxMIT),
	"github.com/spf13/cast":                licenseOnly(spdxMIT),
	"github.com/spf13/pflag":               licenseOnly(spdxBSD3),
	"github.com/spf13/viper":               licenseOnly(spdxMIT),
	"github.com/subosito/gotenv":           licenseOnly(spdxMIT),
	"github.com/yosida95/uritemplate/v3":   licenseOnly(spdxBSD3),
	"golang.org/x/oauth2":                  licenseOnly(spdxBSD3),
	"modernc.org/mathutil":                 licenseOnly(spdxBSD3),

	"github.com/spf13/afero": {SPDX: spdxApache2, Files: []string{fileLicenseTxt}},
	"github.com/spf13/cobra": {SPDX: spdxApache2, Files: []string{fileLicenseTxt}},

	"github.com/modelcontextprotocol/go-sdk": {
		SPDX: spdxSDKMixed,
		Note: "The SDK is mid-relicensing. New and relicensed contributions are Apache-2.0; " +
			"contributions whose authors have not granted relicensing consent remain MIT. " +
			"Documentation excluding specifications is CC-BY-4.0. The single `LICENSE` file " +
			"below states all three and is reproduced whole.",
		Files: []string{fileLicense},
	},

	"go.yaml.in/yaml/v3": {
		SPDX: spdxYAMLMixed,
		Note: "The files ported from libyaml stay MIT; every other file is Apache-2.0. " +
			"Both the `LICENSE` and the `NOTICE` file are reproduced.",
		Files: []string{fileLicense, fileNotice},
	},

	// The x/ repositories ship a PATENTS grant beside the licence. It is a term
	// of use, not authorship metadata, so it is reproduced too.
	"golang.org/x/sync": {SPDX: spdxBSD3, Files: []string{fileLicense, filePatents}},
	"golang.org/x/sys":  {SPDX: spdxBSD3, Files: []string{fileLicense, filePatents}},
	"golang.org/x/text": {SPDX: spdxBSD3, Files: []string{fileLicense, filePatents}},
	"golang.org/x/time": {SPDX: spdxBSD3, Files: []string{fileLicense, filePatents}},

	"modernc.org/libc": {
		SPDX: spdxBSD3,
		Note: "`LICENSE-3RD-PARTY.md` records the terms of the C sources and the Go code " +
			"that this module carries. It is reproduced whole.",
		Files: []string{fileLicense, "LICENSE-3RD-PARTY.md"},
	},

	"modernc.org/memory": {
		SPDX:  spdxBSD3,
		Note:  "The module ships four licence files. All four are reproduced.",
		Files: []string{fileLicense, "LICENSE-GO", "LICENSE-MMAP-GO", "LICENSE-LOGO"},
	},

	"modernc.org/sqlite": {
		SPDX: spdxBSD3,
		Note: "The driver is BSD-3-Clause. It bundles SQLite itself, which its authors " +
			"dedicated to the public domain; `SQLITE-LICENSE` states that dedication and is " +
			"reproduced below.",
		Files: []string{fileLicense, "SQLITE-LICENSE"},
	},
}
