# Dependencies

`AGENTS.md` requires the standard library first. Every nontrivial dependency
needs a rationale, a license, and a maintenance note in an ADR or in this file.
This file is that record for direct module requirements that no ADR covers.

Last updated: 2026-08-15. Verified against `go.mod`, `go mod graph`, the module
cache license files, and `proxy.golang.org`.

## Direct requirements [NOW]

This table matches the first `require` block of `go.mod` exactly: six modules, no
more and no fewer.

| Module | Version | License | Released | Used by |
|--------|---------|---------|----------|---------|
| `github.com/modelcontextprotocol/go-sdk` | `v1.7.0` | Apache-2.0 with a residual MIT subset; see below | 2026-07-28 | `internal/mcpserver`, `internal/tools`, `internal/oauthserver` |
| `github.com/spf13/cobra` | `v1.10.2` | Apache-2.0 (`LICENSE.txt`) | 2025-12-03 | `internal/cmd` |
| `github.com/spf13/pflag` | `v1.0.10` | BSD-3-Clause | 2025-09-02 | `internal/config` |
| `github.com/spf13/viper` | `v1.21.0` | MIT | 2025-09-08 | `internal/config` |
| `golang.org/x/sys` | `v0.47.0` | BSD-3-Clause | 2026-06-30 | `internal/securefile` (Windows-tagged files only) |
| `modernc.org/sqlite` | `v1.56.0` | BSD-3-Clause | 2026-08-03 | `internal/store` (multi-user backend) |

All six are at the latest version published by `proxy.golang.org` on the
verification date. `golang.org/x/sys` sits far above the version Viper selects,
which is what its advisory required.

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

### `golang.org/x/sys`

**Version.** `v0.47.0`. License BSD-3-Clause.

**Rationale.** `internal/securefile` reads a Windows security descriptor from an
open handle to decide whether a key file or a token record is owner-only. The
standard library exposes no ACL API, and the alternative that this replaced was a
subprocess call to `icacls`, which cannot inspect the object that was actually
opened and so cannot close the gap between the check and the open. Only the
`golang.org/x/sys/windows` subpackage is used, and only from Windows-tagged files,
so no other platform links it.

**Maintenance.** Published by the Go team, versioned with the toolchain. The pin
is far above the version Viper selects, because that older version carried a
Windows advisory.

### `modernc.org/sqlite`

**Version.** `v1.56.0`. License BSD-3-Clause.

**Rationale.** The multi-user deployment needs a migration-backed relational store,
and release binaries must stay `CGO_ENABLED=0`. This driver is pure Go, so the
cross-compiled artifacts keep building: verified for linux/amd64, linux/arm64,
darwin/arm64 and windows/amd64 with cgo off. The obvious alternative,
`mattn/go-sqlite3`, is a cgo binding and would break every cross-compiled release.

**Maintenance.** Actively maintained, with a release every two to four weeks.
`v1.56.0` bundles SQLite 3.53.3 and patches an upstream data-corruption bug in
journal rollback, which argues for taking it rather than against it.

**Operational constraint.** Upstream requires the exact `modernc.org/libc` version
this driver's `go.mod` pins. Moving `libc` on its own is a supported way to break
the driver, so both modules are on the manual-review list in `.github/renovate.json`
and neither may be automerged. They move together or not at all.

**Cost.** The driver brings eight indirect requirements: `modernc.org/libc`,
`modernc.org/memory`, `modernc.org/mathutil`, `github.com/dustin/go-humanize`,
`github.com/ncruces/go-strftime`, `github.com/mattn/go-isatty`,
`github.com/remyoudompheng/bigfft` and `github.com/google/uuid`, the last four of
them through `modernc.org/libc`. Internal principal identifiers are generated
with `crypto/rand`, not with `github.com/google/uuid`, so no code in this module
imports that package and it stays indirect.

### House-stack note

Cobra plus Viper is the maintainer's house stack in the other Go MCP servers.
Reusing it keeps flag naming, environment-variable conventions, and command-tree
shape consistent across those servers, so operator knowledge transfers. That
consistency is why the pair was selected, not a claim that a smaller alternative
could not work.

### `github.com/modelcontextprotocol/go-sdk`

**Version.** `v1.7.0`, pinned by ADR 0002 and `docs/upstream-pins.md`, and a
direct requirement in `go.mod` since the MCP foundation slice.

**Rationale.** ADR 0002 selects the SDK and the MCP specification version. It is
the official SDK; `mark3labs/mcp-go` is deliberately not used. It supplies the
MCP types, tool registration, the stdio transport, the Streamable HTTP handler,
and the resource-server half of the OAuth surface. The authorization-server role
is local code, per ADR 0003.

**License.** The `LICENSE` file at the `v1.7.0` tag records a licensing
transition. New and relicensed contributions are Apache-2.0. Contributions whose
authors have not granted relicensing consent stay MIT. Documentation excluding
specifications is CC-BY-4.0. Both code licenses are compatible with distribution
of this project. Re-check the mixed state whenever the pin moves.

**Maintenance.** `v1.7.0` is the latest version published by `proxy.golang.org`
on the verification date, and it is a stable release (`prerelease=false`,
`draft=false`). `docs/upstream-pins.md` holds the tag and commit evidence;
`docs/mcp-version-matrix.md` holds the per-feature obligations.

**Verification cost.** The official MCP conformance suite cannot score this
server, so an SDK bump is validated by this repository's own tests only. See
ADR 0002.

**Cost.** The SDK brings seven indirect requirements:
`github.com/google/jsonschema-go`, `github.com/yosida95/uritemplate/v3`,
`github.com/segmentio/encoding`, `github.com/segmentio/asm`,
`golang.org/x/oauth2`, `golang.org/x/sync` and `golang.org/x/time`.

## Indirect set [NOW]

`go.mod` carries 26 `// indirect` requirements, from four direct modules:

- 9 reach the module only through Viper — `fsnotify/fsnotify`,
  `go-viper/mapstructure/v2`, `pelletier/go-toml/v2`, `sagikazarmark/locafero`,
  `sourcegraph/conc`, `spf13/afero`, `spf13/cast`, `subosito/gotenv`,
  `golang.org/x/text`. `golang.org/x/sys` was in this set until
  `internal/securefile` began to import it, and it is now direct.
- 1 comes from Cobra alone — `inconshreveable/mousetrap`.
- 1 is required by both Cobra and Viper — `go.yaml.in/yaml/v3`.
- 8 come from `modernc.org/sqlite` — `modernc.org/libc`, `modernc.org/memory`,
  `modernc.org/mathutil`, `dustin/go-humanize`, `ncruces/go-strftime`,
  `mattn/go-isatty`, `remyoudompheng/bigfft` and `google/uuid`.
- 7 come from the MCP SDK — `google/jsonschema-go`, `yosida95/uritemplate/v3`,
  `segmentio/encoding`, `segmentio/asm`, `golang.org/x/oauth2`,
  `golang.org/x/sync` and `golang.org/x/time`.

`segmentio/asm` is the one entry `go mod why -m` reports as not needed by the
main module: it is reached from `segmentio/encoding` under build constraints
this build does not select, and `go mod tidy` keeps the requirement for the
platforms that do.

Licenses across the set are MIT, Apache-2.0, and BSD-3-Clause only. No copyleft
and no unlicensed module is present.

`sourcegraph/conc` is pinned by Viper at a pseudo-version
(`v0.3.1-0.20240121214520-5f936abd7ae8`), not at a release tag. That is Viper's
choice, and it cannot be changed without replacing Viper.

### Known advisories in the indirect set

`govulncheck ./...` reports **no vulnerabilities**, and it reports no advisory of
any kind. Two advisories did apply to the versions Viper selects, and both are
resolved by an explicit bump in `go.mod`:

| Advisory | Module | Viper selects | This module pins |
|----------|--------|---------------|------------------|
| `GO-2026-5970` | `golang.org/x/text` | `v0.28.0` | `v0.39.0` |
| `GO-2026-5024` | `golang.org/x/sys` | `v0.29.0` | `v0.47.0` |

Neither was on a called path even before the bump, so the gate was green either
way. They were bumped because a stale transitive version becomes a real finding as
soon as a call site appears, and `internal/securefile` then added exactly such a
call site for `golang.org/x/sys/windows`. Keep both requirements above Viper's
choice until Viper catches up, and re-run `govulncheck -show verbose ./...` after
any dependency change.

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
