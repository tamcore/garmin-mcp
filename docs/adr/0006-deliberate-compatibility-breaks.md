# ADR 0006 — Deliberate compatibility breaks

## Status

Open, and permanently open. This ADR is a running register. Every intentional
break from the pinned Taxuspt contract gets an entry here at the moment it is
made.

This register holds two kinds of entry: breaks from the pinned Taxuspt tool
contract, and approved deviations from the literal wording of the build brief.
Both are recorded so that no divergence is silent.

## Context

The pinned Taxuspt commit defines the public tool surface: names, descriptions,
input schemas, output shapes, and filtering behavior. Requirement precedence puts
credential and tenant security above that contract, so some breaks are expected.

Known break classes, from the brief:

- no `login_to_garmin`, `submit_credentials`, or MFA MCP tool exists, because MCP
  arguments reach model context, transcripts, telemetry, and tool logs;
- no tool accepts `user_id`, email, token path, or an account selector, so the
  single-global-client architecture cannot be reproduced;
- write and destructive tools are off by default remotely and need the
  intersection of operator enablement and granted scope;
- destructive tools fail closed when elicitation confirmation cannot be obtained,
  where the house behavior degrades gracefully;
- remote tools cannot write an arbitrary server filesystem path, so path-taking
  download tools return a bounded resource, blob, or short-lived principal-bound
  handle instead.

## Decision

Record each break as a row in the register below, and mirror it in
`docs/parity.md`. Silent breaks are prohibited. Compatibility aliases may be kept
for upstream misspellings or legacy names, but each alias is documented.

Every entry states the upstream contract, what changed, the security reason, the
precedence rule that justifies it, and the client-visible effect.

### Register

No entry yet. Entries are added by the phase that makes the break.

| Upstream contract | Change | Reason | Precedence rule | Client-visible effect |
|-------------------|--------|--------|-----------------|-----------------------|
| *(none recorded)* | | | | |

### Approved brief deviations

Deviations from the literal wording of the build brief. Each one is approved and
reasoned here, so it is documented rather than silently non-compliant.

#### 1 — Runtime-only Dockerfile instead of multi-stage

The brief asks for a multi-stage `Dockerfile`. `Dockerfile` in the repository
root is runtime-only: it starts from a digest-pinned
`gcr.io/distroless/static-debian12:nonroot`, and its single `COPY` takes an
already-built binary from `${TARGETPLATFORM}/garmin-mcp` in the build context.
The `dockers_v2` block in `.goreleaser.yaml` supplies that context at release
time.

Reasons:

- **Single-source builds.** GoReleaser is the one build path. A `go build` stage
  inside the image would be a second, divergent path, and release artifacts and
  image contents could then differ.
- **No compiler in the runtime image.** The final layer holds only the binary. No
  toolchain, shell, or package manager is present, so nothing extra can be
  reached in the container. A multi-stage build reaches the same end state but
  keeps a builder stage in the build graph.
- **Reproducible ldflags owned by GoReleaser.** `-trimpath`,
  `-X main.version`, `-X main.commit`, and `mod_timestamp` are set once, in
  `.goreleaser.yaml`. Duplicating them in a Dockerfile stage would let the image
  report a different version from the archive.

Consequences: a bare `docker build` of the repository root does not produce a
working image. The build context must already hold the binary at
`${TARGETPLATFORM}/garmin-mcp`. Releases get that context from GoReleaser
`dockers_v2`. The CI container job prepares it itself, with a `go build` into
`image-context/linux/amd64` followed by `docker build` on that directory, so the
hardening smoke test does not need release credentials. That CI binary carries no
version ldflags, which is acceptable because the job asserts image hardening
only, never a reported version.

## Consequences

- `docs/parity.md` and this register must agree. A mismatch is a release blocker.
- An exclusion that is not a security-driven break stays release-blocking until a
  maintainer approves it, with evidence.
- Placeholder and `not implemented` handlers are never counted as parity, and are
  not compatibility breaks either.
