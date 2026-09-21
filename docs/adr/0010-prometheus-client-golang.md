# ADR 0010 — the Prometheus client and a private registry

## Status

Accepted, 2026-09-21.

## Context

`internal/mcplog` answers "what happened to this call": one structured line per
tool call, with attribution, sizes and outcome. It cannot answer "is the
failure rate rising" — that question needs an aggregate over time, and Garmin
Connect is an unofficial, undocumented API whose endpoints and schemas can
change shape without notice. When that happens, the first symptom is a tool
that starts failing for everyone, and today that is invisible until a human
goes looking through logs. A pull-based metrics endpoint is the standard way to
make that failure rate a number an operator's own monitoring can alert on,
without this project taking on a push pipeline or a collector dependency of its
own.

## Decision

Take `github.com/prometheus/client_golang` as a direct requirement. Expose a
pull endpoint that renders the Prometheus text exposition format. The registry
backing that endpoint is private to each `metrics.Recorder`, never the
package's default global registerer, so two servers — production and a test —
can coexist in one process without a duplicate-registration panic.

## Alternatives rejected

**A hand-rolled text exposition.** Writing the exposition format by hand is
roughly 150 lines and adds no new module. What it would omit is exactly the
part that is easy to get wrong and hard to notice: correct histogram bucket
selection, and the Go and process collectors (goroutines, heap, GC pauses, file
descriptors) that this project did not write and does not want to maintain. A
metrics system that silently reports the wrong latency distribution is worse
than no metrics system, because it is trusted.

**OTLP push.** The OpenTelemetry SDK is the largest dependency tree of the
three options, requires a collector endpoint the operator must stand up and
maintain, and buys distributed tracing this project has deliberately deferred
(`internal/observability` is `planned`, not `exists`, per `AGENTS.md`). Taking
on that tree for four counters and some histograms is a poor trade.

## Consequences

- Four new indirect modules reach the linked set:
  `github.com/prometheus/client_model`, `github.com/prometheus/common`,
  `github.com/prometheus/procfs`, and `google.golang.org/protobuf`.
  `THIRD_PARTY_NOTICES.md` grows by their license texts.
- The exposition endpoint is unauthenticated, by decision: Prometheus scrapers
  are not OAuth clients, and gating the endpoint behind this server's own bearer
  scheme would make every scrape target carry a rotating credential. Its labels
  carry the pseudonymous principal identifier and the exact tool name, neither
  of which is a raw payload, but both are more than a health check discloses.
  The operator's network boundary — routing `/metrics` only to a scrape target,
  never to the public internet — is therefore load-bearing rather than optional,
  and this is recorded in `docs/threat-model.md`.
- `internal/metrics` mirrors `internal/mcplog`'s redaction stance: its method
  set is closed, there is no free-form label channel, and the two free-text
  `mcplog.ToolEvent` fields, `Arguments` and `Reason`, are never rendered as
  labels, because a label is retained by a scraper for months with no
  redaction path a log rotation would give it.
