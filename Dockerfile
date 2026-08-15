# Runtime-only image. The binary is built outside this Dockerfile (GoReleaser or
# `go build`) and copied in, so no toolchain, shell, or package manager ships in
# the final image. The base is pinned by the multi-arch *index* digest (never a
# single-platform manifest digest, which would break the arm64 image); a
# dependency updater keeps it current.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:1b7b9f0f0e0a1d2155f531db587cc48ec26aaf97ab64364225f5bf18a054e66a

ARG TARGETPLATFORM

LABEL org.opencontainers.image.title="garmin-mcp" \
      org.opencontainers.image.description="Model Context Protocol server for Garmin Connect" \
      org.opencontainers.image.source="https://github.com/tamcore/garmin-mcp" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.base.name="gcr.io/distroless/static-debian12:nonroot"

COPY --chown=65532:65532 ${TARGETPLATFORM}/garmin-mcp /usr/local/bin/garmin-mcp

# The MIT license and the upstream MIT material recorded in the notices both
# require the notice to travel with the distribution, and an image is a
# distribution. The files go in a project-named subdirectory so they add to
# /licenses instead of replacing it: this base keeps its own Debian licenses in
# /usr/share/common-licenses and /usr/share/doc/*/copyright and creates no
# /licenses of its own today, but a subdirectory stays correct if it ever does.
COPY LICENSE THIRD_PARTY_NOTICES.md /licenses/garmin-mcp/

# Numeric nonroot uid/gid so the image works with read-only root filesystems and
# `runAsNonRoot` admission policies that cannot resolve names.
USER 65532:65532

# The root filesystem is expected to be read-only; all mutable state (SQLite
# database, master key) lives here and must be a mounted volume.
VOLUME ["/data"]

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/garmin-mcp"]
