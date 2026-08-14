# Dependencies

`AGENTS.md` requires the standard library first. Every nontrivial dependency
needs a rationale, a license, and a maintenance note in an ADR or in this file.
This file is that record for direct module requirements that no ADR covers.

Last updated: 2026-08-14. Verified against `go.mod`, `go mod graph`, the module
cache license files, and `proxy.golang.org`.

## Direct requirements [NOW]

| Module | Version | License | Released | Used by |
|--------|---------|---------|----------|---------|
| `github.com/spf13/cobra` | `v1.10.2` | Apache-2.0 (`LICENSE.txt`) | 2025-12-03 | `internal/cmd` |
| `github.com/spf13/viper` | `v1.21.0` | MIT | 2025-09-08 | `internal/config` |
| `github.com/spf13/pflag` | `v1.0.10` | BSD-3-Clause | 2025-09-02 | `internal/config` |

All three are at the latest version published by `proxy.golang.org` on the
verification date. These are the first real dependencies of the module.

### `github.com/spf13/cobra`

**Rationale.** The command tree (`serve`, `auth`, `doctor`, `tools list`,
`migrate`, `version`) needs subcommand grouping, persistent flags shared with
the configuration layer, per-command argument validation, and injectable output
and error writers. The injectable writers are load-bearing: they are how
`internal/cmd` tests assert that stdout stays byte-empty on the stdio path
without touching the process file descriptors. The standard library `flag`
package supplies none of this.

**License.** Apache-2.0. Compatible with distribution of this project.

**Maintenance.** Active. `v1.10.2` is the latest release. Cobra is the
command-layer default across the Go ecosystem, with a large downstream user
base.

### `github.com/spf13/viper`

**Rationale.** `internal/config` needs one precedence chain over four sources:
a flag the operator actually changed, then a `GARMIN_MCP_*` environment
variable, then the configuration file, then the default. Viper supplies the
pflag binding that carries the "changed" bit, the per-key environment binding,
and the file reading and decoding. The project keeps the security-relevant parts
local: each key is bound explicitly with `BindEnv`, so `AutomaticEnv` never
absorbs an unknown variable; the `_FILE` secret handling, all validation, and
all redaction live in `internal/config`. Viper aggregates sources; it is not the
policy layer.

**License.** MIT. Compatible with distribution of this project.

**Maintenance.** Active. `v1.21.0` is the latest release.

**Cost.** Viper is the reason the module has a transitive set at all. See
[Indirect set](#indirect-set-now).

### `github.com/spf13/pflag`

**Rationale.** POSIX-style flags, plus the `Changed` bit that separates
"explicitly set on the command line" from "left at the default". Configuration
precedence depends on that distinction, so `internal/config` imports pflag
directly rather than only inheriting it. It is also unavoidable: Cobra and
Viper both require it.

**License.** BSD-3-Clause. Compatible with distribution of this project.

**Maintenance.** Active. `v1.0.10` is the latest release.

### House-stack note

Cobra plus Viper is the maintainer's house stack in the other Go MCP servers.
Reusing it keeps flag naming, environment-variable conventions, and command-tree
shape consistent across those servers, so operator knowledge transfers. That
consistency is why the pair was selected, not a claim that a smaller alternative
could not work.

## Pinned but not yet required [NOW]

| Module | Version | License | Status |
|--------|---------|---------|--------|
| `github.com/modelcontextprotocol/go-sdk` | `v1.7.0` | Apache-2.0 with a residual MIT subset; see below | Pinned by ADR 0002 and `docs/upstream-pins.md`. **Absent from `go.mod`.** |

**Why it is absent.** `go mod tidy` drops a requirement that no package
imports, and CI verifies a clean `go mod tidy` diff, so an unused requirement
would fail the build. The module line lands with the MCP foundation slice, in
the same commit as the first code that imports the SDK. A reader who finds no
SDK line in `go.mod` must read that as sequencing, not as a forgotten pin.

**Rationale.** ADR 0002 selects the SDK and the MCP specification version. It
is the official SDK; `mark3labs/mcp-go` is deliberately not used.

**License.** The `LICENSE` file at the `v1.7.0` tag records a licensing
transition. New and relicensed contributions are Apache-2.0. Contributions whose
authors have not granted relicensing consent stay MIT. Documentation excluding
specifications is CC-BY-4.0. Both code licenses are compatible with distribution
of this project. Re-check the mixed state whenever the pin moves.

**Maintenance.** `v1.7.0` is the latest version published by
`proxy.golang.org` on the verification date, and it is a stable release
(`prerelease=false`, `draft=false`). `docs/upstream-pins.md` holds the tag and
commit evidence; `docs/mcp-version-matrix.md` holds the per-feature obligations.

## Indirect set [NOW]

`go.mod` carries 12 `// indirect` requirements. The set arrived with Viper:

- 10 reach the module only through Viper — `fsnotify/fsnotify`,
  `go-viper/mapstructure/v2`, `pelletier/go-toml/v2`,
  `sagikazarmark/locafero`, `sourcegraph/conc`, `spf13/afero`, `spf13/cast`,
  `subosito/gotenv`, `golang.org/x/sys`, `golang.org/x/text`.
- 1 comes from Cobra alone — `inconshreveable/mousetrap`.
- 1 is required by both — `go.yaml.in/yaml/v3`.

Licenses across the set are MIT, Apache-2.0, and BSD-3-Clause only. No copyleft
and no unlicensed module is present.

`sourcegraph/conc` is pinned by Viper at a pseudo-version
(`v0.3.1-0.20240121214520-5f936abd7ae8`), not at a release tag. That is Viper's
choice, and it cannot be changed without replacing Viper.

### Known advisories in the indirect set

`govulncheck ./...` reports **0 vulnerabilities affecting this code** and
passes. It also reports two advisories against versions Viper pins:

| Advisory | Module | Pinned | Fixed in | Reachability |
|----------|--------|--------|----------|--------------|
| `GO-2026-5970` | `golang.org/x/text` | `v0.28.0` | `v0.39.0` | Imported, not called |
| `GO-2026-5024` | `golang.org/x/sys` | `v0.29.0` | `v0.44.0` | Module level only, Windows platform |

Neither is on a called path today, which is why the gate is green. Both are
stale-version problems, not code problems, and an explicit `golang.org/x/text`
and `golang.org/x/sys` bump in `go.mod` fixes both without waiting for Viper.
`docs/implementation-status.md` tracks this as a gap. Re-run
`govulncheck -show verbose ./...` after any dependency change, because a future
Windows or text-handling call site turns either advisory into a reachable
finding.

## Rules for adding a dependency

1. Try the standard library first, and record why it was not enough.
2. Check the license before the code. MIT, Apache-2.0, BSD, and ISC are
   acceptable. Copyleft is not.
3. Check maintenance: latest release, release cadence, and open advisories.
4. Add the module in the same commit as the first code that imports it, so
   `go mod tidy` stays clean.
5. Add a row and a rationale here. Write an ADR instead when the choice is
   consequential enough to need alternatives and consequences recorded.
6. Never let a dependency own a security decision. Keep precedence,
   validation, redaction, and policy in this project's own packages, behind
   local interfaces.
