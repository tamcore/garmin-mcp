# Implementation status

This file is the resume point. A cold agent run resumes from `AGENTS.md` plus
this file alone. If those two files are not sufficient, fix that before any
further feature work.

Every stopping point updates this file in the same commit as the work it
describes. Never mark an item done on the strength of a placeholder or
`not implemented` handler.

Last updated: 2026-08-21.

## Phase status

| Phase | State |
|-------|-------|
| 0 — native login feasibility gate | **CLOSED — GO** (see `docs/adr/0001-garmin-login-feasibility.md`) |
| 1 — inventory, docs skeleton, and CI | **CLOSED** with the recorded gaps below |
| 2 — core auth and storage (M1) | **CLOSED** |
| 3 — MCP foundation (M1) | **CLOSED** |
| 4 — remote multi-user (M2) | **CLOSED.** The MCP conformance requirement is **blocked upstream**, with evidence below, not outstanding work |
| 5 — compatibility breadth (M3) | **DONE** — 137 of the 138 upstream tools are implemented, plus 6 the pinned manifest does not carry, for 143 registered. The one refusal is `set_fit_download_dir` (ADR 0006). All 5 resources are implemented |
| 6 — hardening and release | **IN PROGRESS.** The security review ran and its three release blockers plus five more findings are fixed, and `v0.0.1` is published. The findings under "The Phase 6 security review ran" are still open, none blocking |

Phase definitions are in `docs/phases.md`.

## Released versions

There is no `CHANGELOG.md`; GoReleaser generates each release's notes from the
commit subjects, and this table is the durable record.

| Tag | State | What it carries |
|-----|-------|-----------------|
| `v0.0.1` | published | First release. The pipeline itself was the point, and it found one real defect: the `signs` block still used cosign v2 flags while the pinned installer had moved to v3, which no gate could have caught because keyless signing cannot run outside a release job. |
| `v0.0.2` | published | A confidential OAuth client may now supply its secret digest inline through `secret-hash`, not only through `secret-hash-file`. The file form cannot be satisfied by a Kubernetes projected Secret volume at all: its keys are symlinks, which the hardened file layer refuses before it even reaches the owner-only mode check, so no mode or `fsGroup` setting can fix it. Also: a public client carrying a digest is now refused in the composition root rather than silently ignored, and a batch of documentation that contradicted the build was corrected — the tool counts, the resource status, the health and readiness probes, the scheduled cleanup, and the destructive-confirmation shape a client actually receives. |
| `v0.0.3` | published | The state directory can no longer be a Kubernetes volume mount root, because a mount root is owned by uid 0 and `chmod` needs ownership; `restrict` now verifies instead of chmodding when the mode is already exactly right, the permission-denied error names the remedy, and the supported subdirectory shape is documented. Also: the `master-key-file` example dropped, since that setting names a directory and the default is already correct, and the refresh-failure documentation now enumerates all three refusal descriptions. |
| `v0.0.4` | published | Refresh-token reuse is detected regardless of the presented token's own expiry. Previously the grant refused an expired token before reaching the transaction that detects reuse, so replaying a consumed-and-expired token revoked nothing while a later generation of its family was still live — the theft signal was discarded. Consumed rows are now retained while their family is live, bounded to the most recent 200 generations so growth stays bounded; the revocation is filed under the reuse reason rather than a generic one, and an unrecognised reason is an error rather than a default; and one clock read serves both checks, so a token live at the first and expired at the second can no longer escape detection. |
| `v0.0.5` | published | `tools/list` now advertises only what the caller can actually call — the granted scopes intersected with the operator's enabled tiers, decided by the same `policy.Decide` the call path uses, plus the elicitation capability a destructive tool needs to be confirmable. The filtered result is marked privately cacheable, because it is caller-specific. `server_info` reports the effective tiers, the granted scopes and the visible tool count beside the registered total, and each tool carries its tier in `_meta`. |
| `v0.0.6` | published | The browser authorization flow works in Chrome. `form-action 'self'` blocked the consent page's redirect to the client's registered redirect URI, because Chrome enforces the directive across a form submission's whole redirect chain — so the code was minted and never delivered, in every Chrome session, for every client. `form-action` now also names the origin of the transaction's validated redirect URI, on the consent response and on a refused authorization, which are the two responses that redirect outward. |
| `v0.0.7` | published | The consent redirect reaches the client in Chrome. v0.0.6 had put the client's origin on the POST response and on the authorization refusal redirect, both no-ops: `form-action` is enforced against the policy of the document that *contains* the form, so the origin belongs on the `GET /login/consent` response. Also refuses an IPv6-literal redirect URI at registration, because CSP3's host-source grammar admits no bracketed literal and a browser silently drops such a source. |
| `v0.0.8` | published | A secret file owned by another account no longer passes validation on its mode alone — the owner is compared to the effective uid, on the open descriptor. The SQLite database and its `-wal`/`-shm` sidecars are validated before the database is opened, rather than after it has been pinged. `LoadKey` verifies the key directory on every read. Deliberately absent: a hard-link check, an exclusive-rename install, a fallback, orphan recovery, a temporary sweeper and doctor parity — each introduced a state a deployment could not recover from, and each is recorded above. |
| `v0.0.9` | published | `garmin-mcp unlink --principal <id>` and `garmin-mcp revoke --principal <id>`, so an operator can answer a data-deletion request without opening the database by hand. unlink runs the Garmin cascade; revoke kills the OAuth token families and consents and leaves the link intact. Both take one explicit principal, are idempotent, and refuse an unknown one. A busy database is now named as such, including when the write lock is refused at open time. Also carries the e2e test pinning which stream a destructive confirmation is written to. |
| `v0.0.10` | published | `garmin-mcp repair-permissions [--dry-run]`. Extending validation to the database made a platform-widened mode an upgrade wall rather than a restart problem: a deployment whose state predates the check could not start on the new version at all. The command brings every guarded path this uid owns to exactly the mode the server requires, metadata only, never opening the database, so it works from an init container. Exit non-zero when anything is unresolved, and under `--dry-run` when a real run would change something. |

## Measured coverage

Statement coverage from `go test -count=1 -cover ./...`, measured on 2026-08-18.

| Package | Untagged |
|---------|----------|
| `internal/cmd` | 82.1% |
| `internal/config` | 90.8% |
| `internal/cryptostore` | 87.5% |
| `internal/garmin/api` | 90.7% |
| `internal/garmin/auth` | 65.9% (88.3% with `-tags=fakegarmin`) |
| `internal/garmin/client` | 94.3% |
| `internal/garmin/protocol` | 96.7% |
| `internal/identity` | 97.7% |
| `internal/loginweb` | 82.6% |
| `internal/mcpserver` | 89.3% |
| `internal/notices` | 89.3% |
| `internal/oauthserver` | 92.4% |
| `internal/oauthstore` | 84.6% |
| `internal/policy` | 91.7% |
| `internal/ratelimit` | 94.8% |
| `internal/securefile` | 85.8% |
| `internal/store` | 82.9% |
| `internal/testkit` | 91.5% |
| `internal/tokenlink` | 80.0% |
| `internal/tools` | 85.8% |
| `migrations` | 100.0% |

Every package is at or above the 80% floor `AGENTS.md`'s "Testing" section
states as universal, enforced by `ci.yaml` in both directions.

## 2026-08-21: Go 1.27.0 toolchain baseline

`go.mod` now sets Go 1.27.0 as the project baseline. Every CI and release job
continues to resolve that exact baseline through `go-version-file: go.mod`; no
workflow carries a second hard-coded Go version. The golangci-lint pin moved
from v2.12.2 to v2.13.1, whose published binary is built with Go 1.27.0.

`go fix ./...` applied the Go 1.27 standard-library and composite-literal
modernizations across the existing source. A second `go fix -diff ./...` emits
no changes. The linked module set and generated third-party notices are
unchanged under the new toolchain.

## 2026-08-18: the secure-file hardening attempt, cut back to what reviewed clean

A change to `internal/securefile`, `internal/cryptostore` and `internal/store`
— the layer that holds the master key, the token store and the database —
went through five review rounds. Each round fixed real defects and introduced
new ones. Two pieces of the change survived two review rounds clean; the rest
kept producing HIGH-severity findings: a temporary file that was no longer
synced before its only durable link was removed (a crash could lose it), a
cleanup failure silently joined with the ordinary "someone else already
created this" race outcome (a caller couldn't tell the two apart and could
start work believing a clean loss when an orphan was actually left behind),
and a `doctor` command that mutated the state it was supposed to be
inspecting three separate times across the rounds. Two intermediate designs
were tried and abandoned in turn: an `st_nlink == 1` check on secret files
(reverted — creating a second hard link to a file this process owns already
needs the same uid or root, so the check bought little threat coverage for
the machinery it cost), and an atomic exclusive rename as the file-install
primitive (reverted — `RENAME_NOREPLACE`/`RENAME_EXCL` is not available on
every filesystem this server has to run on, NFS above all, and the no-fallback
version could not install a key at all on such a mount).

**Decision: keep only what reviewed clean twice, revert everything else to
`HEAD` (released, working code).**

Kept:

1. The ownership check on secret files and directories — a descriptor-based
   `st_uid == euid` comparison (`checkOwner`/`checkStatOwner` for files,
   `checkDirOwner` for directories), refusing a foreign-owned object that
   previously passed on mode alone. Wired into `dir.restrictExisting` (files)
   and `dir.restrict` (directories).
2. The SQLite database file and its `-wal`/`-shm` sidecars are validated with
   `restrictDatabaseFiles` **before** `OpenDatabase` lets the driver touch
   them, closing a window where a pre-existing insecure or symlinked file's
   bytes could be read (and a WAL replayed) before any check ran.
3. `cryptostore.LoadKey` now verifies (and, where this process owns it,
   silently tightens) the key directory through
   `securefile.RestrictExistingDir` on every read, not only when a key is
   first installed, so a directory widened after installation is caught on
   the next `serve`, `auth`, or key-rotation read rather than surviving
   indefinitely.
4. The error-path tests this work added to `internal/securefile`, which took
   the package's coverage from 79.2% to 85.8%. The handful that assumed an
   unprivileged process (opening a `000` file, writing inside a `0500`
   directory — both of which succeed for root) now skip under `os.Geteuid()
   == 0` rather than failing there.

Reverted to `HEAD`, byte-for-byte where possible:

- `dir.installNewFile`, `dir.installFailure` and `dir.writeTemporary` in
  `internal/securefile/dir.go` — back to the plain link-then-remove sequence
  with an unchecked deferred `Remove`, exactly as released.
- `internal/cmd/doctor.go`, `internal/cmd/doctorremote.go` and their tests —
  back to classifying state from `os.Stat` and its mode bits directly, with no
  "diagnose without mutating" parity layer against `serve`'s own repair
  behavior. Doctor/serve parity produced more defects across five rounds than
  it delivered value, and effectively required each read-path function
  (`LoadKey`, `OpenDatabase`'s directory check, `EnsureDir`) to grow a second,
  non-mutating twin (`InspectKey`, `DiagnoseDatabaseDir`, `DiagnoseRestrictable(Dir)`)
  that had to be kept in exact sync with the first by hand.
- Every symbol that existed only to support the above and had no remaining
  caller once both were reverted: `securefile.RestrictableState` and its four
  values, `securefile.DiagnoseRestrictable`, `securefile.DiagnoseRestrictableDir`,
  `securefile.CheckSecure`, `cryptostore.InspectKey`, `cryptostore.KeyDirMode`,
  `store.DiagnoseDatabaseFiles`, `store.DiagnoseDatabaseDir`,
  `store.DatabaseFileMode`, `store.TokenDirMode`, and the `dir` methods that
  backed them (`diagnoseRestrictable`, `checkSecure`, `diagnoseRestrictDir`,
  `restrictDirState`). Their tests went with them
  (`internal/securefile/diagnose_test.go`, `internal/store/sqlite_db_dir_test.go`,
  and the `report_test.go` cases exercising the removed `doctor` parity states).
- `docs/operations.md` and `AGENTS.md` no longer claim rename-based
  installation or hard-link verification; both describe the released
  link-then-remove install and the ownership check that actually ships.

The kept items are exactly the ones with two clean review rounds and no
review-round history of findings against them. Nothing here adds a feature;
this pass only narrows an in-flight change to its proven part.

## Invariants (true at every tag, no exceptions)

- The repository builds and runs with no Python/Garth runtime or subprocess.
- Garmin tokens and sensitive identity fields are encrypted at rest; secrets
  never appear in logs, metrics, traces, errors, tool results, or the handoff.
- Every Garmin client, token set, cookie jar, cache entry, and tool result
  belongs to exactly one principal. No global cross-user client exists.
- `go test -race -count=1 ./...`, `go vet ./...`, `golangci-lint run`,
  `govulncheck ./...`, and `go build ./...` pass.
- `goreleaser check` and a snapshot release succeed; the container image builds
  and passes a non-root/read-only smoke test.
- This file matches reality, and `git status --short` is clean.
- No placeholder or `not implemented` handler is counted as working behavior.

## M1 — local single-user stdio server

**Complete**, except the named gaps below, which are carried forward rather than
closed silently.

- [x] The phase-0 login gate is closed with a recorded outcome. — GO, ADR 0001.
- [x] Native 0.3.10 login, MFA continuation, DI exchange, refresh with rotation,
      `.com`/`.cn` host selection, and the full failure classification pass
      against the fake Garmin service.
      Done: login, MFA continuation, DI exchange over the candidate client IDs,
      session validation, refresh with rotation and CAS, host selection, the
      request-time host guard, the fallback classification, a distinct
      rejected-OTP outcome, and explicit widget MFA code delivery, all under
      `-tags=fakegarmin`. **Not done: the `JWT_WEB` cookie fallback**, which was
      implemented and then deliberately removed — a credential this architecture
      can never carry to a second call. That is a recorded decision rather than
      outstanding work, which is why this item is checked; the deviation is in
      `docs/parity.md` and the ADR 0006 register.
- [x] `garmin-mcp serve --transport=stdio` binds exactly one principal from
      process-local configuration, rejects ambiguous multi-account configuration,
      and keeps stdout reserved for MCP frames.
- [x] Tokens are stored owner-only and encrypted; hostile-umask, symlink and
      atomic-write tests pass. The platform-ACL half of this item is **gone with
      the platform**: Windows is no longer supported, so there is no ACL to test.
      `internal/securefile` compiles on unix only, and the hostile-umask, symlink,
      ancestry and atomic-write tests all execute on the platforms that ship.

      This item was unchecked for a long time on the strength of the ACL half, and
      the sequence is worth recording. A `windows-acl` CI job was added to make the
      security-descriptor syscalls execute for the first time, and its first run
      immediately failed — usefully. `internal/securefile` passed, so the syscall
      layer worked, but three `internal/store` legacy-import tests failed because
      files the test process had just created were owned by `S-1-5-32-544`,
      `BUILTIN\Administrators`, and the owner check refused them as not owned by
      the current user. That is standard Windows behaviour: a process holding an
      elevated token stamps *Administrators* as owner of what it creates, and
      `currentUserSID` compared against `GetTokenUser` instead. An operator running
      elevated would have had their own token file refused. Rather than fix a
      platform nobody here runs, Windows was dropped.
- [x] `garmin-mcp auth` completes the one-shot loopback browser login and MFA
      flow, plus the explicit TTY fallback.
- [x] At least one representative read-only tool per major Garmin payload style
      is registered with accurate annotations, strict schemas, bounded results,
      and sanitized errors, and each has name/schema snapshot tests.
- [x] Refresh singleflight, rotating-token CAS, and cache-invalidation tests pass
      under the race detector. The collapsing is hand-rolled from `sync.Mutex`
      plus a per-principal in-flight map with a done channel; there is no
      `singleflight` package involved. One shared `auth.TokenGate` is wired and
      asserted. No cache exists, so cache invalidation has nothing to test.
- [x] CI, cross-platform builds, and the release pipeline are green.

## M2 — remote multi-user server

**Complete.** Every checked item is covered by tests in this repository. The two
unchecked items are the conformance requirement, which is blocked upstream with
evidence, and the operations documentation, which is real remaining work.

- [x] Streamable HTTP resolves the principal only from a verified bearer token
      on every applicable `POST`, `GET`, and `DELETE`; no `user_id`, email,
      token path, or account selector is ever a tool argument.
- [x] Protected Resource Metadata, the RFC 6750 challenge, authorization-server
      metadata, PKCE S256, resource indicators, exact issuer/redirect matching,
      and per-client consent behave as specified.
- [x] Transaction-gated browser login and MFA work end to end against the fake
      Garmin service; no credential-entry MCP tool exists.
- [x] Encrypted per-principal Garmin tokens, per-client consent records, hashed
      opaque MCP token material, and transactional revocation/unlink all persist
      and cascade correctly, failing closed on partial deletion.
- [x] Cross-user isolation and concurrent refresh pass under
      `go test -race -count=1`; session and event identifiers cannot
      authenticate, resume, read, or delete another principal's or client's data.
- [x] The OAuth negative matrix, rate limits, security headers, cookie
      attributes, request-size limits, redaction, and encrypted-store tamper
      tests pass.
- [x] Write and destructive tools are off by default remotely and require both a
      granted scope and operator enablement; destructive actions fail closed when
      confirmation cannot be obtained.
- [ ] **The selected MCP server conformance suite passes with no unexplained
      baseline entry. BLOCKED upstream**, with measured evidence. See
      [MCP conformance is blocked](#mcp-conformance-is-blocked). This item cannot
      close without an upstream change, and it is not a to-do this repository can
      pick up.
- [x] Documentation covers remote deployment, reverse proxy/TLS, security
      assumptions, backup/restore, migrations, and key rotation.
      `docs/operations.md` covers all six across its eight sections. This entry
      claimed the document did not exist long after it was added in `1307f39`;
      the claim, not the work, was the gap.

## M3 — full Taxuspt parity

- [x] The generated parity matrix accounts for every tool and resource at the
      pinned Taxuspt commit. `docs/parity.md` carries per-tool status. **All 5
      resources are served**, and **137 of the 138 tools are implemented**. The
      single remaining row is `set_fit_download_dir`, a documented refusal rather
      than remaining work, so no tool is outstanding. What is still open is the
      **regenerator**, not the matrix: the extractor scripts are not committed, so
      drift against a new upstream pin cannot be diffed in CI.
- [x] Every required contract has passing name/schema/behavior tests, or a
      documented exclusion with evidence. All 137 implemented tools do. The
      documented exclusions are in `docs/parity.md` and in the ADR 0006 register.
- [x] 0.3.2 to 0.3.10 behavior differences affecting those contracts are
      reconciled and recorded. See `docs/upstream-pins.md`: **all 10** numbered
      requirements are landed. Explicit widget MFA code delivery was the last and
      closed on 2026-08-16.

## Commands to run and report at every milestone

```sh
go test -race -count=1 ./...
go test -race -count=1 -tags=fakegarmin ./...
go test -race -count=1 -tags=e2e -timeout=10m ./e2e/...
go vet ./...
golangci-lint run
govulncheck ./...
go build ./...
goreleaser check
goreleaser release --snapshot --clean
```

All three tagged suites hold real tests, so report their results, not just their
exit status. There is no conformance command to add; see the next section.

The live layer is **not** a milestone command: it contacts the real Garmin
service and it is opt-in. Run it deliberately, and record its outcome:

```sh
GARMIN_USERNAME=... GARMIN_PASSWORD=... \
GARMIN_LIVE_ACK=i-accept-live-garmin-traffic \
GARMIN_LIVE_WRITE_ACK=i-accept-live-garmin-writes \
go test -race -count=1 -tags=garminlive ./live/...
```

## The live layer

`live/` carries the `garminlive` tag, in two halves. The test count is not
restated here, because it rots on every added test and states nothing: what keeps
the layer honest is `TestEveryReadOnlyToolIsAccountedFor` and
`TestEveryWriteAndDestructiveToolIsAccountedFor`, which fail when a registered
tool is neither driven by the suite nor listed with a reason.

The **read half** is read-only by construction — every domain client and every
tool of that half reaches Garmin through a caller that refuses anything but a
`GET`, a `HEAD`, or a `POST` whose body is one of the GraphQL query documents
the request layer itself renders, so no mutation can reach the gateway — and
it is gated three ways: the build tag, `GARMIN_USERNAME`/`GARMIN_PASSWORD`, and
`GARMIN_LIVE_ACK` set to the exact value `i-accept-live-garmin-traffic`.

The **write half** needs a fourth gate on top of those three:
`GARMIN_LIVE_WRITE_ACK` set to the exact value `i-accept-live-garmin-writes`.
It is default off, so acknowledging live traffic never acknowledges live
mutation. With it shut every write check skips and the read half behaves exactly
as it did before.

A missing gate is a skip, never a failure. No workflow builds the tag and none
may. `AGENTS.md` holds the full how-to.

It asserts cross-source consistency and never a golden value. Nothing in the
package is pinned to the account under test, and a failure names the field and
the relative delta rather than the reading, so a failing run cannot print health
data into a terminal.

**Run on 2026-08-15 against the dedicated test account.** What passed:

- Login through the `mobile_ios` strategy, the DI exchange, session validation
  against the API tier, and a second read on the same stored token set.
- The read-only caller refusing `POST`, `PUT`, `PATCH` and `DELETE` on a write
  path while still passing the GraphQL calendar read.
- All nineteen account-scoped read-only tools: every one answered, obeyed its
  declared bounds and truncation flags, and carried no coordinate, credential or
  raw payload.
- `get_full_name` and `get_devices` agreeing with the profile and device domain
  clients.
- The accounting test that fails when a registered read-only tool is neither
  exercised nor excused with a reason.

**The accounting test caught the health slice.** The 27 health-and-wellness tools
were registered without a live sweep entry, and because no workflow builds the
`garminlive` tag that landed on master with `TestEveryReadOnlyToolIsAccountedFor`
failing — the exact decay the test exists to stop, proven by the test rather than
by a reviewer. `live/healthsweep_test.go` now drives all 27 through the same paced
read-only caller, the same bound and truncation-flag checks and the same leak scan
as the rest of the sweep, with the argument shape each contract declares: a
calendar day, an inclusive range (`get_body_composition`, `get_daily_steps`,
`get_body_battery`, `get_blood_pressure`) or an end date plus a week count
(`get_weekly_steps`, `get_weekly_intensity_minutes`, `get_weekly_stress`). It is a
separate file only because `surface_test.go` would otherwise cross the 400-line
limit.

**Run on 2026-08-16 against the dedicated test account: all 27 passed, and none is
excused.** The account records almost no wellness data, so most answered with an
empty day. That is a pass and not a skip, and what the run proves is now stated
exactly rather than generously.

**What a sweep entry proves, and what it does not.** The first version of this
sweep asserted only that a result was non-empty and leaked nothing, which a handler
returning a well-formed object without ever contacting Garmin would satisfy. Two
assertions closed that, and both were run against the real service in a failing
state before being trusted:

- **Transport evidence.** `readOnlyCaller` counts what it dispatches, and
  `assertToolAnswers` requires the count to rise across every call. Removing the
  one declared exception made `get_exercise_types` fail with `answered without
  dispatching a request to Garmin`, which is the assertion working.
- **Result shape.** `live/shapes_test.go` declares, per swept tool, the keys that
  tool's own answer always carries; a key that is also an argument must repeat the
  value that was sent. Adding a key no answer has made `get_stats` fail with
  `returned no "a_key_no_answer_has"`.
  `TestEverySweptToolDeclaresItsShape` pins the table to the sweep in both
  directions and needs no credentials, so a tool added to one and not the other
  fails offline.

So the claim the sweep supports is: **each tool dispatched a request and its answer
carried its own shape, its declared bounds, boolean truncation flags, and no
coordinate, credential or raw payload.** It does not claim the readings are
correct — that is what the FIT cross-check and the domain-client agreement checks
are for, and neither covers the wellness surface.

`get_exercise_types` is the one read-only tool that legitimately dispatches
nothing through `readOnlyCaller`: it answers from the strength catalog the process
loaded before the sweep started. That load is the published-catalog read, and it
runs on its own anonymous client rather than through the suite's caller, so the
`answersLocally` assertion still holds after the fetch landed — it was verified,
not assumed. Its reason in `live/shapes_test.go` was rewritten to say that, and
`live/exercisecatalog_test.go` is the drift detector for the URL itself: it fails
when the published document stops answering, stops carrying muscle groups, or
shrinks past a floor.

That drift test is gated like every other request this suite makes, and so is the
start-up read the two environments perform: `gatedExerciseCatalog` checks the
acknowledgement itself rather than trusting its callers, because it is the one
place in the suite that contacts Garmin outside the authenticated session. A
build tag alone therefore dispatches nothing — verified by running
`go test -tags=garminlive ./live/...` with no acknowledgement and no credentials
through a recording proxy, which observed no connection at all, and by a
gate-free test that counts fetch **attempts** rather than inspecting the returned
catalog, since an unreachable network would otherwise make a leak look like a
pass. `TestEverySweptToolDeclaresItsShape` stays gate-free and network-free.

On the first run the account held **zero** activities and an empty workout
library, so everything needing one skipped — including the FIT cross-check, the
whole reason this layer exists. The skip stated the account's own activity
count, so an empty account was never mistaken for a listing this server can no
longer read.

**Second run, after one activity with a device file and one workout were added
to that account: the whole suite passes.** Every previously skipped check ran:
the FIT-against-summary cross-check, the session-coverage invariant, the nine
activity-scoped tools, `get_activity` and `get_activity_fit_data` agreement, and
`get_workout_by_id`/`download_workout`. The decoded device file reproduced
Garmin's own distance, elapsed time, heart rate, ascent and calories inside the
stated tolerances, so ADR 0007's replacement is now confirmed against the real
service and not only against three files decoded by hand.

**The layer earned its keep on that run.** `get_personal_record` — registered,
shipped and green in every fixture test — failed against the live account with
`malformed_payload`. Garmin sends `prStartTimeGmt` as a number, an epoch in
milliseconds, and the model demanded a string, so the tool was broken for every
real account. The fixture had declared `prStartTimeLocal` as a string and
omitted `prStartTimeGmt` entirely: it was written to the same wrong assumption
as the model, which is exactly the blind spot this layer exists to remove. The
fields are `client.Text` now, and a regression test pins both the numeric and
the string form.

### The write half

The write half drives **every registered write and destructive tool but one**
against the real service — 38 of the 39 as of 2026-08-17, and the count is
deliberately not restated per slice, because an accounting test is what keeps it
true and a number in prose is what goes stale. `upload_workout` is the one
exclusion, and it is recorded rather than silent: `upload_workouts` sends the same document to the same endpoint
through the same api-layer method and additionally proves the per-item reporting
the single form has none of. An accounting test fails when a registered write or
destructive tool is neither exercised nor excused with a reason, so this list
cannot decay.

What makes it safe is structural, not conventional:

- A **write caller** refuses any mutating request whose target is not an object
  this suite created, before the request leaves the process. The recognised
  endpoint set is an allowlist, so a mutating endpoint a later slice adds is
  refused until the guard is taught how to own its objects.
- The **ownership ledger has no way to declare ownership**. It exposes three
  entry points and every one of them verifies: `ownCreated` reads the assigned
  identifier out of Garmin's own create response, so a tool that creates and then
  immediately mutates its own creation inside one call still passes; `ownSwept`
  parses the object's name and requires an earlier run's stamp; `ownScheduled`
  takes a calendar entry that was read back and names an already-owned workout,
  so the entry and the workout it belongs to come from the same answer rather
  than from a caller. Go has no file-level visibility, so this is a boundary
  every path respects rather than one the compiler draws — what it does remove is
  any way to *assert* ownership without evidence.
- Both halves of that guard have tests, and one of them drives `delete_activity`
  through the whole real stack — registry, policy with both tiers enabled and
  both scopes held, confirmation middleware with a consenting client — against
  a non-owned identifier, and it is still refused.
- Every created object is removed: by `t.Cleanup` so a failing assertion still
  cleans up, and by an end-of-suite pass over anything the ledger still holds. A
  removal that fails is reported and never swallowed.
- Every created object carries a generated name — the reserved
  `garmin-mcp-live-` prefix, a label, the run stamp and a counter. The sweeper at
  suite start removes a leftover **only** when that whole shape parses and the
  run stamp lies between a compiled-in floor and the instant the current run
  began, so a prefix alone is never taken for ownership and nothing the current
  run created can be swept. The residual — a hand-written name that reproduces
  the exact generated shape with a past stamp — is stated in the code rather than
  hidden. The read half skips anything merely carrying the prefix, which is the
  safe direction for a reader.

A second adversarial review found six defects in the suite's own safety
machinery, and all six are fixed:

- **A create response's identifier was taken as ownership.** It is a number the
  service chose: deduplication, a cache or drift could name an object the suite
  never created, after which the guard would permit mutating and deleting it —
  inside one call, for the three tools that create and then write to their own
  creation. The guard now reads the created object back and admits it only when
  it carries the name the create sent, and `ownCreated` takes that binding rather
  than a response body.
- **The read-only guard judged `GetBody` and dispatched `Body`.** Those are
  independent fields, so a benign query in the replay copy admitted a mutation in
  the body. It now reads the bytes that will be sent, judges those, and puts
  exactly them back.
- **The sweeper's licence was too wide.** Its floor predated the suite's own
  first run, and the name parser accepted empty labels, unknown labels and
  non-positive counters — none of which a generated name has. The floor is now
  the month the write half was written, labels are a declared closed set, and the
  counter must be positive. Name matching still cannot prove creation ownership,
  and the code says so.
- **Deletes released the ledger entry on the tool's own report.** A stale success
  or a no-op removal left a real object untracked, invisible to the leak report
  and beyond any retry. An entry is now released only after the object is proven
  absent, and an object that cannot be proven absent stays in the ledger so the
  cleanup retries it.
- **The absence proofs failed open.** The calendar accepted the first omitted
  result from a gateway the code itself documents as lagging in both directions,
  and the workout path read *any* tool error — a rate limit, an expired session,
  a decode failure — as proof of deletion. Absence now needs two consecutive
  agreeing reads, and for a record it needs the tool layer's own not-found
  advice, never "an error occurred, therefore it is gone".
- **"Exactly one outcome per requested item" was not tested.** Aggregate counts
  and slice lengths pass on a batch that reported the first item twice and
  omitted the rest. Every outcome is now matched against the identifier, date and
  status sent at that position, and the identifiers must be distinct.

Two of the suite's package-level mutables are gone with it: the name counter and
the run stamp are per-run state on the environment, fed by one injected clock, so
the stamp every name carries and the cut-off the sweeper compares against are the
same instant. One package-level handle remains, because `go test` gives a suite
exactly one non-test entry point and hands a test nothing but its own `*testing.T`;
everything a run accumulates lives inside the environment it holds.

**Re-run on 2026-08-15 against the dedicated test account after both rounds of
adversarial review fixes, all four gates open: the whole suite passes**, with one
skip. It was run twice: the second run's sweeper reported nothing, which is what
proves the first left nothing behind.

- `TestLiveWorkoutLifecycle` — create through `create_run_workout`, read back,
  schedule, `update_workout` in place, the calendar entry still points at the
  same workout afterwards, unschedule, delete, gone.
- `TestLiveManualActivityLifecycle` — create, read back, all six metadata writes
  and their read-back, delete, gone.
- `TestLiveStrengthActivityLifecycle` — create with sets, replace the whole set
  list, re-read and compare position by position, delete, gone.
- `TestLiveWorkoutBatchToolsApplyEachItemSeparately` — `upload_workouts`,
  `schedule_workouts`, `schedule_week`, `unschedule_workouts`,
  `delete_workouts`, each item applied and reported on its own.
- `TestLiveRemainingWorkoutBuildersUpload` — the three other builders.
- `TestLiveDownloadActivityFileAnswersForEveryFormat` — `fit`, `tcx`, `gpx`.
- The four guard tests.
- `TestLiveGearLinkAndUnlinkOnACreatedActivity` **skipped**: the account links no
  gear to the activity the read half analyses, so no gear identifier can be
  derived. The skip states the reason. Link a piece of gear to that activity and
  the check runs.

Nothing leaked: the run ended with no outstanding-object report, and the
following run's sweeper removed nothing, which is what proves it. The account
still holds exactly the pre-existing activity and workout it started with — the
FIT cross-check, the session-coverage invariant, the nine activity-scoped tools
and the derived-argument tests all selected them in the same run and passed.

### A third adversarial review found six more, and all six are fixed

The review cleared three areas outright — the read guard, the coordinate scrub and
the two file splits — and they are untouched. What it found:

- **The ownership read-back checked the name and not the identifier.** A generated
  name carries a one-second run stamp and a per-run counter, so two runs starting
  inside the same second render byte-identical names: a stale or drifted create
  identifier naming the *other* run's object satisfied a name-only comparison. The
  fixture that let this pass omitted the identifier from the read-back entirely, so
  no test could tell the two apart. The read-back now returns the identifier as
  well as the name, `ownCreated` requires the fetched object to report the
  identifier being adopted, and the fixture carries one — with a case whose
  read-back names a *different* object under the right name, which fails without
  the check.
- **The sweeper could delete a concurrent run's objects.** Names carry whole
  seconds and the cut-off carried nanoseconds, so a run starting later in the same
  second read a live run's stamp as strictly earlier and swept it. The cut-off is
  now truncated to the resolution the name carries, so two runs in one second
  compare equal and equal is not earlier. Two further holes in the same parser: it
  accepted integer spellings `strconv.FormatInt` cannot produce (`+1`, zero
  padding), which are now refused by round-tripping every numeric field; and the
  stamp floor sat at the midnight *before* the write half existed, admitting almost
  fifteen hours of seconds no run of it ever stamped. The floor is now the author
  instant of the commit that introduced the write half, and the test asserts
  equality rather than a range, so loosening it fails.
- **The FIT affordability assertion priced one pass and the analysis makes five.**
  `deriveSegment` walks a span's records through `distanceOf`, `ascentOf`, the
  accumulator loop, `powerSeries` and `dynamicsOf`. The honest worst case is
  therefore **66,300,000 record visits** — 221 spans (20 sessions, 200 laps, the
  whole-activity segment) walked 5 times over 60,000 records — plus **19,094,400**
  per-second series elements in normalized power, each costing a fourth power. It
  was measured end to end at **0.85 s of CPU** on the development machine, roughly
  a third of it the series. Both figures are now asserted at exactly the current
  product, so any widening of any bound fails; the reviewed-not-enforced status of
  the walk count is written into the test rather than implied. The bounds were
  **not** lowered: 0.85 s is proportionate to a call that first streams a 12 MB
  device file, and it is now interruptible — `AnalyzeFIT` takes a
  `context.Context`, checks it between every whole-activity stage and before every
  span, and reports a cancelled caller as itself. The reviewer's "roughly 152 MB of
  power-series allocation" is right as a *cumulative* figure and is not a memory
  figure: the series are built and dropped one span at a time, so peak residency is
  one 86,400-element buffer, about 691 KB.
- **The workout identifier comparison was not exact.** `client.Number` parses
  through `ParseFloat`, so `Int64()` truncated: an answer naming `123.9` compared
  equal to a requested `123`, and at 2^53 two identifiers one apart compared equal
  in either direction. `Number` now keeps the payload's own spelling and
  `Int64Exact` answers from it, refusing a fractional literal, an exponent form and
  anything outside the int64 range rather than rounding into a neighbouring object.
  `Update`, its 204 read-back and the live sweeper's delete target all use it.
- **Two calendar absences do not prove a deletion, and now say so.** The record
  paths were already correct — they require the tool layer's own not-found advice,
  and a rate limit or a 401 does not count. The calendar has **no authoritative
  not-found**: the GraphQL gateway answers a day with the entries it holds, and an
  entry that never replicated is indistinguishable from one that was deleted.
  Repetition raises the number of replicas that must all have missed it and cannot
  rule out one lagging replica answering every read. The proof is now a value
  carrying its own strength — three agreeing reads for the calendar against two for
  a record — and the code states the residual plainly instead of implying
  certainty. What actually guarantees the calendar is clean is the removal of the
  workout template the entry points at, which *is* proven authoritatively and which
  every scheduling test performs.
- **A raw transport error was printed with `fmt.Fprintf`.** A `*url.Error` carries
  the request URL, and for this suite that URL is a Garmin object path. Every
  diagnostic in `live/` now goes through one structured `slog` logger to stderr,
  and every error through `safeError`, which renders a `*client.APIError` with the
  request layer's own redacting renderer, names a cancelled context as itself, and
  reduces everything else to the Go type of the deepest error in the chain.
- **Four package-level mutables.** Three were `time.Date` values that cannot be
  constants; each is now a function. The fourth, `theWriteSuite`, cannot move —
  `go test` gives a suite one non-test entry point and hands a test nothing but its
  own `*testing.T` — so it and `live_test.go`'s three start-up handles are now
  recorded as the two named exceptions in AGENTS.md's own rule rather than left
  silently violating it.

### The write half earned its keep too: two shipped tools were broken

Both were green in every fixture test and both failed on first contact with the
real service. Neither could have been caught by a fixture, because in both cases
the fixture was written to the same wrong assumption as the code.

**`update_workout` failed on every real update.** Garmin answers an in-place
workout `PUT` with **204 and an empty body** — it names neither the workout nor
the name it stored. `SavedWorkout.ID()` then reported `malformed_payload`, so
the tool returned an error for an update that had already succeeded. The fixture
had scripted a `200` with a full body, which no deployment sends. `Update` now
reads the workout back when the answer carries no identifier, which keeps the
rule the type documents — the identifier and the name are the server's, not the
caller's — instead of echoing what was sent.

**`set_activity_strength_exercise_sets` and `create_strength_training_activity`
failed on every real write.** Garmin refused the set list with HTTP 400 and
`{"message":"Activity ID should not be Null in the Exercises Object"}`. The
replace-all envelope carried only `exerciseSets`; Garmin also requires
`activityId` at the **envelope root**, and it wants it there specifically —
repeating the identifier inside a set or inside an exercise object leaves the
same refusal. `renderSets` now names the activity in the envelope, and the unit
test asserts it with the reason written down. Both tools work live now.

**One catalog entry does not survive a real write.** With the envelope fixed,
Garmin rejected the `SQUAT` / `BACK_SQUAT` pair with
`{"message":"Invalid Sub-Category Passed in the request"}`, while
`BENCH_PRESS` / `BARBELL_BENCH_PRESS` and any known category with a null
exercise name were accepted. That is a state
`internal/garmin/api/exercisecatalog.go` already documents — it is "a documented
subset, not a mirror", and "a name it lists is still rejected if Garmin's enum
disagrees" — so it is recorded rather than patched: verifying the whole
compiled-in catalog against the live service is its own slice, and guessing at
one entry would not make the rest trustworthy. The live suite uses a pair
Garmin accepts.

**One behaviour of the service is worth recording for anyone writing calendar
code.** Garmin serves the workout calendar from a GraphQL gateway that does not
always answer with an entry the REST tier accepted a moment earlier, and the lag
runs in both directions — a removed entry can still be listed. The live suite
re-reads a bounded number of times rather than asserting on the first answer; it
is a wait, not a weaker assertion, because the bound still fails.

## MCP conformance is blocked

This is a measured result, not an unstarted task. The suite was run for real and
cannot score a domain server.

**What the suite is.** `modelcontextprotocol/conformance` is a TypeScript CLI
published to npm as `@modelcontextprotocol/conformance`, plus a composite GitHub
Action. It is not a Go package and not a container. In server mode it connects to
a running server as an MCP client over Streamable HTTP.

**What was run.** A live deployment of this server: a generated TLS certificate,
a master key, an empty database and one preregistered public client, serving at
`https://127.0.0.1:8443/mcp` with protocol version `2026-07-28` and 48 tools,
which was the registered count on the day of that run.
Result: **45 passed, 106 failed.** Every one of the 36 scored server scenarios
failed except three, and two of those three passed vacuously with zero checks.

**Two independent blockers**, both verified in the suite's own source rather than
inferred:

1. **Version gap.** The only stable release, `v0.1.16` (tag commit
   `21a9a2febd7100d7c17ac1021ee7f2ed9f66a1e0`), knows specification versions only
   up to `2026-02-12`. Support for the pinned `2026-07-28` exists solely on the
   `0.2.0-alpha` line, so running the pinned wire version means pinning a
   prerelease.
2. **The suite's server leg cannot authenticate, and its scored scenarios require
   the SDK's reference fixture server.** Its `ServerOptionsSchema` accepts only a
   `url` and a `scenario` — no header, no token, no client credentials — while
   this server authenticates every `POST`, `GET` and `DELETE` from the
   `Authorization` header. Even with a token, the scored scenarios call fixture
   tools by literal name (`test_simple_text`, `test_image_content`,
   `test_audio_content`, `test_tool_with_progress`, and fixture prompts,
   resources and completion flows). A missing tool is recorded as a **failure**
   rather than skipped, so a domain server fails by construction.

**No baseline was written, deliberately.** A baseline covering roughly 35
scenarios would encode "this is not the SDK reference fixture", which is not a
verified SDK limitation and could never legitimately clear. The brief permits a
baseline only for a verified limitation.

**What would unblock it**, none of which was attempted:

- a header or bearer input on the suite's server leg, which is an upstream
  change; or
- a conformance fixture profile in this server exposing the suite's expected
  tool, prompt and resource surface — which would test the SDK rather than this
  product, so it is refused; or
- an upstream requirement set for domain servers.

Do not re-open this as "wire the conformance job". It is wire-able only against a
fixture this project must not become. Re-check it when the suite ships a stable
release that knows `2026-07-28` **and** accepts a credential on the server leg.

## Known gaps

These are deliberate and tracked, not silently dropped.

### Closed: an operator can now answer a data-deletion request without database access

`garmin-mcp unlink --principal <id>` and `garmin-mcp revoke --principal <id>` are
implemented in `internal/cmd/unlink.go` and `internal/cmd/revoke.go`, wired into
`NewRootCommand`. Both need `--database-path` (the multi-user store only; there
is no `FileStore` counterpart, the same restriction `migrate` already carries —
`rotate-key` is not the same comparison, since it supports the FileStore
backend with no `--database-path` at all), both take exactly one `--principal`
with no default and no "all", and both are idempotent — a second run reports a
zero result and still succeeds. `unlink` calls `store.UnlinkGarminAccount`, which itself reports
`ErrPrincipalNotFound` for an unknown principal. `RevokePrincipalTokens`, which
`revoke` calls, does not — it treats an unknown principal as an already-revoked
no-op — so `revoke` adds its own `PrincipalByID` existence check first and
refuses the same way `unlink` does; that difference, and the mutation test that
would fail without the check, are recorded here rather than only in a commit
message. Neither command's output carries an email, a Garmin account
identifier, or a token; see `docs/operations.md` §5 for what each command
prints and does not print.

Neither command can proactively close a session a live `serve` process already
holds open — the in-process revocation event bus in `internal/cmd/revocations.go`
is only reachable from within that process, and an offline command has no
connection to it. What holds regardless: every **new** client request
re-verifies against the database (`internal/oauthserver`'s
`VerifyAccessToken`, reached through `internal/mcpserver/http.go`'s middleware
chain, which wraps every request, not only the one that opens a session) and
fails immediately, including the first request on a session opened before the
command ran. An **already-open** stream is different and is NOT torn down by
an offline command: it can keep receiving server-initiated traffic until the
stream or session closes, times out, or the process restarts — regardless of
whether a further client request arrives on it in the meantime, because the
teardown path is the in-process bus above (`internal/mcpserver/http.go`'s watch
loop calling `terminate`) and an offline command's store has no `Revocations`
sink connected to it. An operator who needs certainty should restart the
server after a deletion. See `docs/operations.md` §5 for the exact claim.

### The Phase 6 security review ran, and these findings are still open

The final security review is **done**. It read the three boundaries against the
code rather than against the claims, and its three release blockers plus five more
findings are fixed: the refresh grant that advertised a narrowed scope while
persisting the wide one, `X-Forwarded-For` read client-most so a caller chose the
rate limiter's key, the credentials page promising the email was not written to
disk, the missing panic barrier, the inert `max-response-bytes`, the principal that
could be silently rebound to another Garmin account, and the SQLite temp store
spilling to a path the read-only container cannot write.

What it verified as holding is worth as much as what it found, because these are
the properties an operator is trusting: the MCP access token cannot reach Garmin
(the outbound request type has no header field at all, so no tool can inject one);
the Garmin DI token cannot reach an MCP client (`internal/tools` and
`internal/resources` do not import the auth, store or crypto packages, and the
response type retains no headers, so a Garmin `Set-Cookie` cannot ride out in a
tool result); credentials cannot become tool arguments (no login tool exists and
none of the 143 tools has a credential-shaped field); tenant isolation holds
structurally, because every outbound path needs a session that cannot be built
without a principal, and the only construction site reads the principal from the
request context; write and destructive gating holds, including on stdio where the
scope source is empty by construction; path traversal is impossible rather than
merely unused, since the tool, resource and transport packages contain no
filesystem calls at all; and key rotation cannot strand a record. Refresh-token
reuse detection is now bounded by the family's lifetime rather than by the
presented token's own expiry: `oauthserver.RefreshToken` carries a `Consumed`
field, populated from the row's `consumed_at` column by `internal/oauthstore`, and
`refreshGrant` checks it before the expiry check, so a consumed token replayed
after its own expiry revokes the whole family through the same `RevokeFamily` call
every other revocation path uses, instead of being waved through as merely
expired. The in-transaction detection inside `RotateRefreshToken` is unchanged and
still the only path for a replay of a still-live consumed token, which keeps that
case atomic against a concurrent refresher.

An adversarial review of that pre-check found seven problems, all fixed. The
cleanup sweep in `internal/store/sqlite_cleanup.go` used to remove a consumed
refresh row as soon as it expired, regardless of whether its family was still
live, which meant the pre-check above only worked until the next cleanup tick; the
retention predicate now keeps a consumed row for as long as its family holds
anything that is not itself expired or revoked, proven against the real
SQLite-backed store as well as the fake. A failed `RevokeFamily` call in the
pre-check used to be folded into the cause with `%v`, which broke `errors.Is`; it
is now wrapped with `%w`, so the failure is recoverable upstream even though the
client's answer is deliberately unchanged. The pre-check used to read the clock
twice, once for its own consumed-and-expired judgement and again for the plain
expiry check, which let a token that was live at the first read and expired by the
second slip onto the plain-expiry path instead of being caught as reuse; both
checks now share one captured `now`. The pre-check's revocation used to be filed
under the generic `authorization_revoked` audit reason instead of
`refresh_token_reuse`, which discarded the theft signal the whole mechanism exists
to preserve; `TokenStore.RevokeFamily` now takes an explicit `RevokeReason` and the
pre-check passes `RevokeReasonReplay`. And `TestConcurrentRefreshesOfOneLiveTokenMintExactlyOnePair`
was renamed to `TestConcurrentRefreshGrantCallsStillProduceExactlyOneWinner`, with a
comment pointing at `internal/oauthstore/race_test.go`'s
`TestRotateRefreshTokenElectsOneWinnerAndKillsTheFamily` for the real atomicity
proof, because the fake-store version never proved storage-layer atomicity in the
first place.

A second, adversarial review pass of that same fix found four more problems, all
fixed. The retention the first pass added — keep a consumed row past its own
expiry while its family is live — had no bound of its own: a continuously
rotating client renews its family's liveness on every refresh, so every past
generation was retained for as long as the client kept refreshing, which is
unbounded per-family growth (roughly 8,760 rows a year at an hourly refresh
cadence). `deleteExpiredTokens` now additionally requires the row to be within
the family's most recent 200 generations (`retainedConsumedGenerations` in
`internal/store/sqlite_cleanup.go`), so retention past a row's own expiry is
capped at 200 extra rows per live family regardless of how long or how often it
rotates; `docs/operations.md`'s cleanup section and the reuse-detection bullets
under "What a client author must know about strict rotation" now state the actual
bound instead of claiming unqualified bounded growth. Second,
`oauthstore.reasonFor` used to map any `RevokeReason` it did not recognize —
including the zero value — onto `authorization_revoked`, silently mislabeling the
audit trail for a future enum member; it now returns an error for anything but
the two known reasons, and `RevokeFamily` refuses before touching the store, so a
rejected reason can never both fail loudly and still revoke. Third, no test
asserted that the consumed-and-expired pre-check in `refreshGrant` passes
`RevokeReasonReplay` rather than the generic `RevokeReasonClient`; the fake
store's `lastRevokeReason` was already recorded but never checked, so mutating
that one argument passed every test. Fourth,
`TestRefreshRevocationFailureStillReportsInvalidGrantButIsRecoverable` checked
the error code and description but not the HTTP status, so a mutant that kept
both and returned 500 instead of 400 survived — and a 500 there is exactly how a
caller could detect that its replay failed to take effect, which
`invalid_grant`'s constant-response guarantee exists to prevent. All four are
covered by tests that fail against the reviewed mutant and pass against the
fix.

**Still open, none blocking, in the order I would take them:**

- `/authorize` and both credential-form routes are mounted OUTSIDE the rate-limit
  gate that covers the token, revocation and metadata endpoints, and the login
  session registry refuses at 256 rather than evicting. 256 unauthenticated
  `GET /authorize` calls therefore deny every user's login for the transaction TTL,
  repeatably. Separately there is no per-IP login-attempt limit at all — the only
  budget is per-transaction and resets with each new one — so password guessing
  through this server is unthrottled on that side, with the operator's egress IP
  absorbing Garmin's response. `docs/threat-model.md` lists "login attempts must be
  limited per IP" as a **must** and it appears in neither the landed nor the
  not-landed list.
- `walkPages` (`internal/garmin/api/activities.go`) exits only when a page comes
  back short, so a Garmin response that ignores the requested `limit` and returns a
  full page every time walks all 100 pages accumulating into one slice with no
  total ceiling. The sibling walk in `challenges.go` names this exact attack,
  enforces a cap inside the loop, and has a test for it.
- The refresh grant never re-checks the client's CURRENT registration, so removing
  a scope from a client leaves existing families minting tokens with it for the
  full 30-day refresh lifetime. In the same area: withdrawing a redirect URI or
  flipping a client to public revokes no family and no consent, and a replayed
  authorization code is refused without revoking the family already minted from it
  (RFC 6749 §4.1.2 SHOULD).
- The pre-registered client secret is compared against an unsalted single-round
  SHA-256 digest with no entropy floor on the secret behind it. The comparison is
  constant time and a public client cannot present a secret, so this is about what
  a leaked digest file or backup is worth offline.
- TLS certificates are loaded once into `tls.Config.Certificates` with no
  `GetCertificate` callback, so a cert-manager rotation is never picked up and the
  pods serve the expired leaf until restarted, with no signal.
- `hasTransportProtection` counts a non-empty `trusted-proxy-cidrs` as transport
  protection, so a cleartext `0.0.0.0` bind is accepted with no override when one
  CIDR is listed — and nothing enforces that connections actually come from those
  CIDRs. On a flat pod network every workload can reach the listener in cleartext.
  The gate is a declaration, not a control.
- Test seams weaker than they read: `oauthserver`'s fake store honoured the scope
  contract the real adapter broke, which is why nothing caught the blocker above;
  `config`'s credential-field guards match on field NAMES and do not descend into
  `config.OAuthClient`, so adding a `Password` field there is invisible to both;
  and one remote test proves the no-fallback half of "the principal comes only from
  a verified token" without ever presenting a forged token.

Two smaller ones recorded so they are not rediscovered: `internal/loginweb`'s
`dropCredentials` clears its locals but not `r.PostForm`, which still holds the
password; and `client.Number`/`client.Text` accept `"NaN"` and `"Inf"` as present
values, so a tool returns a `json: unsupported value` marshal error instead of a
sanitized refusal — which is the opposite of what that package's own fuzz target
asserts.

### Deliberate deviations from the upstream contract

Each of these is also recorded in `docs/parity.md` and in the ADR 0006 register.
A reader who assumes parity from the tool name alone would be wrong about all of
them.

- **`download_activity_file` writes nothing to a server path.** The manifest's
  `output_dir` argument, the `GARMIN_FIT_DOWNLOAD_DIR` environment variable and
  the persisted download directory are all absent. No path is accepted from a
  caller and no file is opened; the bytes come back as a bounded embedded
  resource under `garmin://activity/{id}.{format}`, and a payload over the bound
  is refused rather than truncated. The manifest classifies the tool
  `external-side-effect`; **this server puts it in the write tier**, so it is
  gated like any other write.
- **The scheduling tools have no duplicate avoidance.** Upstream's
  `_is_already_scheduled` pre-check is a GraphQL calendar read that this server
  does not make, so `schedule_workout` and `schedule_workouts` are honestly
  non-idempotent. Their annotations say so, and upstream's `Idempotent:` opening
  sentence is deliberately absent from every description in this server — a
  registration test asserts that no description contains it.
- **`set_activity_description` cannot clear a description with an empty string.**
  `api.requireText` refuses an empty write field with `client.ErrValidation`, and
  the tool layer rejects it before that.
- **`get_exercise_types` reads Garmin's published catalog once at start-up**,
  from the compiled-in URL
  `https://connect.garmin.com/web-data/exercises/Exercises.json`, and serves that
  immutable snapshot for the process lifetime. The compiled-in subset of the FIT
  `exercise_category` enum remains **only as the fallback** for a read that
  failed, and the result names which catalog answered in a `source` field. The
  published document is preferred over the vendored FIT profile because the two
  sets differ in both directions: the web catalog carries values Garmin's own
  client writes that the enum cannot express — bare category-name entries, and
  names with a leading digit such as `_3_WAY_CALF_RAISE` — and it carries muscle
  groups, which the enum has no equivalent for. The read is anonymous, bounded,
  refuses a redirect, refuses a document smaller than the compiled-in subset, and
  cannot fail a start-up. Categories are validated against a closed set — the
  fetched catalog merged over the compiled-in one, so the fetch only widens it —
  and an exercise name gets a lexical check only, with Garmin authoritative.
- **`get_workout_by_id` serves the numeric identifier only.** The UUID form that
  adaptive Garmin Coach plans use is not served.
- **`decoupling_percent` carries the opposite sign to upstream's
  `hr_drift_pct`.** This server reports `(first - second) / first * 100` over the
  per-half power-to-heart-rate ratios, which is the standard convention: positive
  means the ratio fell, negative means it rose. Upstream computes the inverse and
  still calls it drift, so its label contradicts its own sign. The arithmetic here
  is not changed to match; the convention is stated in the `api.FITDrift` doc
  comment and in the schema description, and the reasoning is in
  `docs/parity.md`. **No interpretation label is served** — upstream's
  `well_coupled` needs a threshold nobody published, so a label would be an
  invented cut-off served as a finding.
- **`get_activities` returns three keys the manifest does not pin.** `steps`,
  `elevation_gain_meters` and `elevation_loss_meters` are on each list entry, as
  upstream returns them. The manifest record pins the input schema only, so the
  naming follows this server's own list result. All three are omitted when the
  activity does not carry them.
- **`get_activity_fit_data` reports `descent_meters` and `max_cadence`** beside
  ascent and average cadence on every session, lap and whole-activity segment,
  from the FIT profile's `total_descent` and `max_cadence` by the same route
  ascent and average cadence take, with the record-derived walk as the fallback in
  both directions. Ascent and descent are absent, not zero, when the file carries
  no altitude series; a stream that carried altitude and did not move reports a
  measured zero.
- **The FIT cadence keys name no unit**, where upstream's say `_rpm`. Only the
  session and lap fields are sport-dynamic — on a running session they are
  `avg_running_cadence` and `max_running_cadence` in strides per minute — so the
  suffix is wrong for every run there. `Record.Cadence` has no dynamic form and is
  always rpm, so the descriptions split by surface: segments say rpm or
  strides/min, and everything derived from the record stream says rpm.
  `average_cadence` was corrected together with the newer `max_cadence`.
- **The whole-activity FIT summary refuses a fold over a subset of sessions.**
  When a multisport file's sessions disagree in provenance, the folded total, peak
  or average is absent and the complete record-derived figure stands. A total over
  a subset under-reports, a peak over a subset is a lower bound printed as a
  maximum, and neither says a session is missing from it. Every folded field was
  audited against its fallback; `total_calories` is the one with none, so there
  absence is terminal and the per-session figures carry what is known — unless
  `sessions_truncated` is set, when even those are a subset.
- **A truncated FIT decode reports absence rather than a prefix.** Past a decode
  bound the retained stream is a part, so figures derived from it are left absent
  and the whole-stream aggregates — curve, grade bands, temperature split,
  decoupling — are not computed. Device figures are untouched, and lists of
  detected events are kept with their own truncation flag. `samples_truncated`,
  `sessions_truncated` and `laps_truncated` are reported separately, because each
  voids only what it touched: lap truncation voids nothing but the lap list, and
  in particular does not disable the session fold. A suppressed segment also
  withholds `end_time` and `duration_seconds` unless the file declared the window,
  since the last retained sample is where the bound fell rather than where the
  segment ended. `get_power_duration_curve` skips a truncated file rather than
  folding a lower bound into a season best.
- **One tool is left unregistered rather than stubbed**: `set_fit_download_dir`
  (it would persist a caller-supplied server filesystem path, and is refused by
  design). `get_activity_fit_data` was on this list before FIT decoding landed;
  it is registered now. The three calendar tools that were also unregistered —
  `get_scheduled_workouts`, `get_training_plan_workouts` and `schedule_week` —
  are registered now that the client layer builds the GraphQL request they need.

Five registered tools are **not** in the pinned manifest at all, because they
come from open upstream pull requests rather than the pinned commit:
`get_exercise_types`, `set_activity_strength_exercise_sets`,
`create_strength_training_activity`, `update_workout` and `delete_activity`.

### What actually gates a write tool today

The write and destructive tiers need the **intersection** of operator enablement
and a granted scope, and the two halves behave differently per transport:

- **stdio**: `internal/cmd/wiring.go` leaves the scope source nil, which becomes
  `policy.NoScopes`, so every write and destructive tool is refused however the
  operator sets `enable-write-tools`.
- **streamable-http**: `internal/cmd/remotescopes.go` reads the scopes from the
  verified bearer token and nothing else. An operator who registers an OAuth
  client carrying `garmin:write` **and** enables the write tier gets working
  writes. Nothing in the repository blocks that combination, and it is the
  intended M2 behavior. Both halves default off, so the default deployment is
  read-only.

The package comment in `internal/policy/tier.go` still says that no scope is
issued anywhere in this repository. That was true before the remote path landed
and is now true only of the default configuration. Correct it in the next commit
that touches the package.

### Fail-safe limits that are known and accepted

- `mcpserver.Revocation` carries principal, client and family, and **no resource
  selector**, while a session binding and a consent key both carry the resource.
  Revoking one consent therefore closes slightly more sessions than that grant
  covered. The direction is fail-safe, so it is accepted rather than fixed.
- A revocation event dropped under buffer pressure (a 256-entry channel with a
  counted non-blocking send) costs the affected session its early termination
  only. The database stays the authority and the token check refuses the next
  request on that session.

### Gates the pipeline still needs

The **coverage threshold** gate already existed in `ci.yaml` (the `test` job's
"Enforce the per-package coverage floor" step), contrary to what this file
previously said; it computes each package's percentage from `cover.out` and
fails in both directions against the explicit exception list
(`cmd/garmin-mcp`, `internal/garmin/auth`). A bounded **fuzz smoke** job
(`fuzz-smoke`) and a **two-clean-build reproducibility** check
(`reproducible-build`) are now added too. **Container image signing,
container image and per-binary SBOMs, and build provenance attestation** are
now configured; each is described below, and each carries an explicit note on
what is proven locally versus what only a real tag push can prove. The MCP
conformance job is not on this list: it is blocked upstream, not unstarted.

- Supply-chain attestation beyond checksum signing is **out of scope by
  decision**, not pending. `.goreleaser.yaml` signs the checksum file
  (`signs: artifacts: checksum`, keyless cosign `sign-blob`) and that stays: it is
  the minimal integrity guarantee, it needs no extra machinery, and a downloader
  can verify an archive against it.

  Container image signing, SBOMs of any kind, and build provenance attestation
  were configured and then removed. They are not wanted here. The removal also
  deletes their failure modes: every one of them could only ever run inside a
  tagged release job with a live OIDC token, so none was verifiable before the
  first real tag, and each was a step that could fail a publish part-way for
  reasons unrelated to the artifact being correct. `--skip=sbom` is gone from the
  CI snapshot invocation with them, and a local snapshot now produces exactly four
  archives and no SBOM documents.

- The CI unit job writes `cover.out` with `-covermode=atomic`, and the `test`
  job's coverage-floor step enforces the documented 80% rule per package,
  checked in both directions against the exception list. See
  [Measured coverage](#measured-coverage) for the current numbers.
- Fuzz targets exist for the parsers most exposed to untrusted or drifting
  input: `internal/garmin/protocol` (`FuzzClassifyJSONLogin`,
  `FuzzClassifyWidgetPages`, `FuzzParseWidgetMFAVars`), `internal/garmin/client`
  (`FuzzNumberUnmarshalJSON`, `FuzzTextUnmarshalJSON`, `FuzzParseDate`),
  `internal/tools` (`FuzzSanitizeUntyped`) and `internal/garmin/api`
  (`FuzzParseFITActivity`). The `fuzz-smoke` CI job discovers every declared
  `FuzzX` function and runs each for a bounded 10s; none performs I/O or reaches
  the network. The race detector is deliberately off in that job: it slows
  fuzzing by about an order of magnitude, and on a GitHub runner that made the
  engine miss its own shutdown deadline and fail the build on timing rather than
  on a finding. Those parsers still run under `-race` in the unit, fakegarmin
  and e2e jobs.
- The `reproducible-build` job builds `cmd/garmin-mcp` twice in one job, each
  from its own `GOCACHE` so the second build is a real recompile, with
  `-trimpath` and fixed `-ldflags` version/commit literals, and fails if the
  two binaries' SHA-256 hashes differ.
- GitHub-native **secret scanning** is a repository setting, not a workflow file,
  and still needs enabling. Dependency and license review **is** an enforced CI
  gate: `ci.yaml` runs a SHA-pinned `actions/dependency-review-action` on
  `pull_request` with `fail-on-severity: low` and an explicit `allow-licenses`
  list that matches `docs/dependencies.md`.

### Commands: no declared gaps remain

`garmin-mcp migrate` and `garmin-mcp tools list` were the last two commands that
validated configuration and then returned a `*cmd.NotImplementedError`. Both are
wired now: `tools list` prints the registered surface with its tier and effect
and exits 0, and `migrate` applies the embedded migrations, refusing with a
configuration error when no database path is set rather than guessing a
location. The `cmd.ErrNotImplemented` sentinel no longer appears anywhere in the
source.

### Platform and environment limits

- The OS keyring backends in `internal/cryptostore` are cgo-free **no-ops** that
  report unavailable, which keeps `CGO_ENABLED=0` cross-compilation working per
  ADR 0005. The owner-only key file is the only real backend.
- `internal/securefile` installs a completed temporary under its final name by
  hard-linking it into place (`link(2)`, which fails atomically with `EEXIST`
  when the name is taken), then removes the temporary either way. This needs a
  filesystem that supports hard links, which every filesystem this server
  needs to run on — including NFS-backed persistent volumes — does; an atomic
  exclusive rename (`renameat2`'s `RENAME_NOREPLACE`,
  `renameatx_np`'s `RENAME_EXCL`) and an `st_nlink == 1` check on every secret
  file were both tried and reverted: the rename primitive is not available on
  every filesystem this server has to run on (NFS has no wire-protocol
  equivalent), and the link-count check caught little — creating a second link
  against a `0600` file this process owns already needs the same uid or root
  that controls it, since Linux's `protected_hardlinks`, default on, blocks
  anyone else — while costing real recoverability. The ownership check
  (`st_uid == euid`) on both files and directories stays; see
  `docs/operations.md`'s key-directory section for the operator-facing version
  of this.
- Symlink checking refuses a store or key directory reached **through** a
  symlinked path. On macOS that refuses anything under `/var`, `/tmp` or `/etc`,
  because `/var` is a symlink to `/private/var`. Four test suites work around it
  with `filepath.EvalSymlinks(t.TempDir())`. Decide whether that is the final
  contract and document it, or normalize before checking.
- `FileStore` now serializes one principal's record across processes with an
  `flock(2)` advisory lock held for the whole read-modify-write, alongside the
  per-principal in-process mutex. Key rotation forced this: `rotate-key` is a
  separate process from `serve`, and the prescribed content-equality re-check
  narrows the window without closing it, because a Go-level read-then-write is
  two operations rather than one atomic engine statement. The lock is advisory
  and host-local, so the store must not sit on a network filesystem. Both
  deployments stay single-active-instance by design; the change is that a second
  process can no longer silently overwrite a newer record with an older one.
- `modernc.org/sqlite` is a direct dependency, and `modernc.org/libc` must move
  only with it. Both are on the manual-review list in `.github/renovate.json` and
  neither may be automerged.

### Storage and key-management gaps

- Key rotation is **landed**, not open: `garmin-mcp rotate-key` re-seals every
  sealed record in both backends, and `docs/operations.md` §4 carries the
  procedure. The residual limits are that it is offline, that the retiring key is
  never deleted automatically, and that a FileStore run can only speak for the
  principal the configuration binds. This entry described it as unavailable for
  several commits after it shipped.
- Backup and restore are **out of scope by decision**: the database sits on an
  operator-controlled volume and backing it up is the operator's job.
  `docs/operations.md` carries the procedure, including that the database and the
  master key are two halves of one backup and that a restore rolls consents back
  to the backup's moment.
- The record schema version is 2. A schema-1 record reports corruption rather
  than decoding, because its additional data does not match. No migration exists
  and none is needed: nothing has shipped. The next bump after a release carries
  schema 2 does need one.
- Down migrations are deliberately unsupported, because a down migration that
  drops a column silently destroys token material.

### Carried-over uncertainties

- Live `MFA_REQUIRED` continuation is unproven against the real service. See
  ADR 0001.
- Phase-0 evidence comes from one residential source IP. Datacenter and CI egress
  may be scored differently by Cloudflare. See ADR 0001.
- The widget MFA path now parses the page's inline JS variables and requests code
  delivery. `ClassifyWidgetLogin` reads `customerGuid`, `mfaMethod`, `locale`,
  `clientId` and `codeSentTo`, the parsed method outranks the title guess, and
  `Authenticator.requestWidgetMFACode` POSTs `PathWidgetRequestMFACode` when the
  method is email or SMS and no code has been sent yet. A confirmed delivery
  clears `MFADeliveryUncertain`, which now means the code may not have been sent
  rather than that this server never asked. A failed request does not fail the
  login, deliberately, and is never retried. A rejected OTP now has its own
  outcome: `protocol.OutcomeMFARejected`, produced only by
  `ClassifyMFAVerifyJSON` and `ClassifyMFAVerifyWidget`, which classify the
  mobile/portal/widget verify responses specifically and reinterpret a
  credential-shaped rejection there — never a bare status, never the initial
  credential POST — as a wrong code rather than a wrong password. This is not an
  upstream distinction: 0.3.10 folds both into one `GarminConnectAuthenticationError`.
  The registry's existing lease and attempt-budget behavior already kept the
  transaction retryable on any verify failure, so the addition is classification
  and error-matching (`errors.Is(err, protocol.ErrMFARejected)`) only.
- The `JWT_WEB` cookie fallback is **deliberately not ported**. It was built,
  reviewed and then removed, and the reason is architectural rather than a
  matter of effort. Upstream recovers with it because one long-lived Python
  process holds the fallback session and the next API call in the same
  in-memory object. This server has no such continuity: every tool call
  authenticates through `Refresher.Do`, which reads the **persisted** per-principal
  DI token set, and upstream itself never persists `jwt_web` (`Client.dumps`
  serializes only `di_token`, `di_refresh_token` and `di_client_id`, and its
  own JWT_WEB refresh depends on the CAS ticket-granting cookie inside the same
  in-memory session). On stdio the `auth` command exits before `serve` starts,
  so process memory cannot bridge the two. A credential that no later call can
  read is not a fallback.

  The removal followed a working implementation, so what it cost is recorded
  honestly: the port passed both tagged suites, sealed the cookie two pointers
  deep with alias-stripping leak tests, and narrowed the trigger well below
  upstream's bare `except Exception`. What it could not do was reach a second
  call. Review also found that shipping it would have made a previously
  unreachable state reachable in `internal/cmd`: `remoteLogin.bind` resolves and
  **links** a Garmin account durably before `commit` stores the token set, so a
  result carrying no token set would have linked an account and then failed, while
  `internal/cmd/tty.go` printed "the tokens are stored encrypted". Reintroducing
  `JWT_WEB` requires a deliberate durable credential lifecycle first — a
  process-local map is not one — and is not currently in scope.

  Related latent defect, independent of `JWT_WEB` and **not fixed**: that same
  resolve-before-commit ordering means any `commit` failure — expired staged
  tokens, a cancelled token gate, a store read or save error — leaves a
  principal created and a Garmin account linked with no token set. It is
  self-healing on retry, because the next successful login resolves the
  already-linked principal and commits onto it, which is why it is recorded here
  rather than treated as a release blocker. The durable fix is one transaction
  spanning principal creation, account linkage and the token write; reversing the
  call order alone cannot work, because the token row requires the principal.
- The login state machine is still not on the top-level login path.
  `auth.Machine` is exhaustively tested, but `Authenticator.Login` and
  `CompleteMFA` report progress through `Result` states, and the machine is
  stepped only by the MFA transaction registry. Either route the top-level flow
  through it or state plainly that it governs transactions only.
- The container job now proves start-up, not only that the binary executes. Beyond
  the original **nonroot, read-only, shell-free** smoke test it runs the image with
  a volume at `/data`, polls `/readyz` until the server reports ready rather than
  merely alive, and checks that the database and the encryption key appear under
  that volume owner-only. A read-only `/data` must make start-up fail promptly:
  every reserved docker exit status is rejected and the log must name a read-only
  filesystem, so an image whose entrypoint cannot execute at all — which would
  otherwise satisfy "it failed, as expected" — fails the job instead. Verified
  locally against a real engine: ready on the first poll, both files `600` owned
  `65532:65532`, and the read-only case exiting `1` with
  `mkdirat keys: read-only file system`.

  One operational fact this surfaced, and it matters for any test deployment: the
  authorization server refuses to name a cleartext issuer at **any** origin,
  loopback included, and `allow-insecure-http` does not cover that refusal
  (`internal/cmd/remoteendpoints.go`). A remote deployment therefore cannot be
  smoke-tested over plain HTTP even on `127.0.0.1`; the job generates a
  throwaway self-signed certificate.
- The parity extractor scripts are not committed; `docs/parity.md` documents the
  algorithm instead. The obligation is **narrowed rather than open**: a full Go
  re-implementation is rejected (AST extraction over Python source needs either a
  third-party Go Python parser, which this repository requires an ADR and a
  notices entry for, or pattern matching that emits wrong contracts the first time
  upstream reformats), and committing the Python scripts has an unmet
  prerequisite — a committed generator becomes CI's authority, so it must first
  reproduce the **reviewed** artifacts byte for byte, and these manifests have
  been hand-corrected since generation.

  What is enforced instead, offline and in the ordinary `test` job, is the
  coupling: `TestUpstreamPinsAgreeBetweenTheDocAndBothManifests` fails when
  `docs/upstream-pins.md` and the commit embedded in either manifest disagree.
  That is the failure a pin bump actually has, and it was silent before. Content
  regeneration stays the documented manual procedure in `docs/parity.md`.

## The write safety delay exists

`AGENTS.md` instructed for a long time that a configurable safety delay be applied
before write and destructive execution, and nothing implemented it. All 23 write and
5 destructive tools were registered under that instruction without one. It is now
built, and the instruction that described it as a per-tool step is gone: a tool
inherits the delay from its tier and must never carry a sleep of its own.

Where it lives: `Server.awaitSafetyDelay` in `internal/mcpserver/middleware.go`,
inside the policy middleware, after the tier and scope gate and after destructive
confirmation. The setting is `config.Config.SafetyDelay`, flag `--safety-delay`,
default `0`, ceiling `MaxSafetyDelay` of 5 minutes.

Four properties are pinned by tests in `internal/mcpserver/safetydelay_test.go`, and
each was checked against a mutant that breaks it:

1. Writes and destructive calls wait; reads never do.
2. Zero disables the pause, which is what every existing deployment gets.
3. A cancellation during the wait stops the call: the handler never runs.
4. A refused call never waits, so a refusal costs neither the server the wait nor
   the prober the timing signal.

`internal/cmd`'s `TestServeCarriesTheSafetyDelayIntoTheServer` pins the wiring, so a
setting that parses and validates but never reaches the middleware fails the build.

What it deliberately is not: a second confirmation. Destructive tools already require
elicitation that fails closed. The delay's value is on the write tier, which has no
interactive gate, and there the cancellation window is the only one a caller gets.

## The five MCP resources are served

`internal/resources` publishes all five documents the pinned upstream declares:
four workout templates and the structure reference. They are compiled in, reach no
Garmin endpoint, and carry nothing of the caller's, so they are registered through
`mcpserver.AddResource` rather than as tools and hold no tier.

Why they are not gated like tools, deliberately rather than by omission: the rate
limiter and the logging middleware both scope themselves to `tools/call`, and
`internal/ratelimit/middleware.go` states the reason — reading a resource costs the
Garmin account nothing, so charging a budget for it would only make discovery
unreliable. That reasoning holds exactly for a constant document. The gate that does
apply on remote is the HTTP layer's, which authenticates every `POST`, `GET` and
`DELETE` before dispatch, so a resource read still needs a verified bearer token.
Principal resolution already ran for every method, not only tool calls.

What is pinned by tests: the manifest set in both directions, each resource's name,
description and media type against the manifest, that every document renders and
parses, that every template is accepted by this server's own upload path, that no
`stepOrder` repeats inside a document, and that the structure reference lists every
value the templates use. At the server layer: listing and reading over a session, an
unknown URI refused, a duplicate URI refused before it reaches the SDK — whose own
`AddResource` replaces on conflict — a scheme-less URI refused before the SDK panics
on it, and a nil registrar refused at construction.

The one thing not claimed: byte-identical template contents. The contract fields and
the vocabulary are upstream's; the step counts, durations and descriptions inside
each template were written here. `docs/parity.md` says so rather than implying
equivalence that was never verified.

## Next task

The tool surface is finished: 137 of 138 manifest tools and all 5 resources, with
the one refusal documented. Phase 5 is closed, and every numbered item below is now
closed too — they are kept struck through rather than deleted so the reasoning
survives.

**What is actually next** is the open-findings list in "The Phase 6 security review
ran" under Known gaps. Nothing there blocks a release. In rough order of value:
`/authorize` outside the rate-limit gate together with the absent per-IP login
limit, then `walkPages`' missing total ceiling, then the OAuth lifecycle gaps
(client re-check on refresh, redirect-URI withdrawal, code-replay revocation).

The closed items, in the order they were taken:

1. ~~A store-level key-rotation driver.~~ **Landed.** `garmin-mcp rotate-key`
   re-seals every sealed record in both backends, resumably and without
   double-sealing, with the CAS interaction inside `internal/store`. ADR 0005's two
   open items are closed; see `docs/operations.md` §4.
2. ~~A Go parity regenerator plus a CI drift check.~~ **Narrowed and closed.** A
   full Go re-implementation is rejected on dependency and correctness grounds,
   and committing the Python extractor requires a golden reproduction of
   hand-corrected manifests that does not exist. The pin/manifest coupling is
   enforced by a test instead, which catches the failure a pin bump actually has.
   See `docs/parity.md`.
3. ~~The `remoteLogin.bind` half-write.~~ **Fixed.** `store.BindGarminAccount`
   resolves-or-creates the principal, links the account and saves the token set in
   one transaction, and `internal/cmd` no longer orchestrates a multi-step durable
   write. I had recorded this as self-healing on retry; that was only true of one of
   its three cases — a linkage failure after the principal was created left an
   unlinked principal durably claiming an email, and a concurrent same-account race
   left a loser row that a retry never touched, because it resolves straight to the
   winner. Those accumulated.

4. ~~The final security review.~~ **Done.** Three release blockers and five more
   findings are fixed; what it verified as holding, and the findings still open, are
   in "The Phase 6 security review ran" under Known gaps. Nothing open is a blocker,
   and the first two entries there are the ones with a real deployment consequence:
   `/authorize` sits outside the rate-limit gate, and `walkPages` accumulates
   without a ceiling.

Explicitly **not** work, so that a cold agent does not go looking for it:

- MCP conformance is blocked upstream, not unstarted. ADR 0002 and the section
  above carry the evidence.
- The `JWT_WEB` cookie fallback is deliberately not ported.
- Windows is deliberately unsupported.
- Backup and restore are the operator's responsibility, documented in
  `docs/operations.md` rather than tested here.
- SBOMs, container image signing and build provenance were configured and then
  removed by decision. Checksum signing stays.
- `docs/operations.md` **exists** — eight sections, including remote deployment,
  TLS, the database, key management, revocation, lifetimes, upgrades and the
  limits. Several places in this file used to claim it did not.

A note on reading this file at all: it has repeatedly said work was outstanding
after that work was finished, and the checkbox state was wrong in three places at
once while every one of those entries' own prose said "done". A cold agent resumes
from `AGENTS.md` plus this file, so verify a claim against the repository before
acting on it, and fix the line in the same commit as the work.
