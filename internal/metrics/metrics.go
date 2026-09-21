// Package metrics is the Prometheus seam for the MCP server.
//
// It mirrors internal/mcplog deliberately. The method set is closed and there is
// no free-form label channel, so a caller cannot put a request body, a token, a
// health reading or a Garmin payload into a series. The tool-call recorder takes
// the very same mcplog.ToolEvent the logger takes, so the two cannot describe
// different calls.
//
// Two of that event's fields are never rendered: Arguments and Reason are free
// text, and a label is retained by a scraper for months with no redaction path.
//
// The registry is private to the Recorder. The default global registerer is
// package-level mutable state, and two servers in one test binary would panic on
// it.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/tamcore/garmin-mcp/internal/mcplog"
)

// namespace prefixes every metric this package owns.
const namespace = "garmin_mcp"

// These are label names shared by several vectors below; the goconst linter
// flags the repeated literal.
const (
	labelCategory = "category"
	labelEndpoint = "endpoint"
	labelOutcome  = "outcome"
	labelTier     = "tier"
)

// latencyBuckets are tuned to Garmin rather than left at the library default.
// One tool call fans out into roughly ten sequential upstream requests, which
// routinely lands past the default's top bucket, where an untuned histogram
// reports only "slow".
func latencyBuckets() []float64 {
	return []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
}

// sizeBuckets run from a small selector to the megabyte range where the
// decompressed-size bound in internal/garmin/client cuts a transfer off.
func sizeBuckets() []float64 { return prometheus.ExponentialBuckets(256, 2, 10) }

// Config is what a Recorder needs that it cannot derive.
type Config struct {
	// Version and Commit are the ldflags-injected build identity, published as
	// the build_info gauge so a dashboard can attribute a change to a rollout.
	Version string
	Commit  string
}

// A Recorder records the server's metrics onto its own registry.
//
// A nil *Recorder is a valid no-op on every method, so a deployment that
// configured no metrics leaves the field nil and no caller branches on it. It is
// safe for concurrent use.
type Recorder struct {
	registry *prometheus.Registry

	toolCalls         *prometheus.CounterVec
	toolDuration      *prometheus.HistogramVec
	toolArgumentBytes *prometheus.HistogramVec
	toolResultBytes   *prometheus.HistogramVec

	upstreamRequests      *prometheus.CounterVec
	upstreamDuration      *prometheus.HistogramVec
	upstreamResponseBytes *prometheus.HistogramVec

	tokenRefreshes  *prometheus.CounterVec
	loginAttempts   *prometheus.CounterVec
	registeredTools *prometheus.GaugeVec
}

// toolVectors builds the four metrics that describe one MCP tool call.
func toolVectors() (calls *prometheus.CounterVec, duration, argBytes, resultBytes *prometheus.HistogramVec) {
	calls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "tool_calls_total",
		Help: "Completed MCP tool calls by outcome.",
	}, []string{"tool", labelCategory, labelTier, labelOutcome, "status", "principal"})

	duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "tool_duration_seconds",
		Help: "MCP tool call latency.", Buckets: latencyBuckets(),
	}, []string{"tool", labelCategory, labelTier})

	argBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "tool_argument_bytes",
		Help: "Size of the arguments an MCP tool call arrived with.", Buckets: sizeBuckets(),
	}, []string{labelCategory, labelTier})

	resultBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "tool_result_bytes",
		Help: "Size of the serialized result an MCP tool call returned.", Buckets: sizeBuckets(),
	}, []string{labelCategory, labelTier})

	return calls, duration, argBytes, resultBytes
}

// upstreamVectors builds the three metrics that describe one Garmin request.
func upstreamVectors() (requests *prometheus.CounterVec, duration, responseBytes *prometheus.HistogramVec) {
	requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "upstream_requests_total",
		Help: "Requests made to the Garmin Connect service.",
	}, []string{labelEndpoint, "op", "status"})

	duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "upstream_duration_seconds",
		Help: "Garmin Connect request latency.", Buckets: latencyBuckets(),
	}, []string{labelEndpoint})

	responseBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "upstream_response_bytes",
		Help: "Size of a Garmin Connect response body.", Buckets: sizeBuckets(),
	}, []string{labelEndpoint})

	return requests, duration, responseBytes
}

// authVectors builds the login, refresh and registered-tools metrics.
func authVectors() (refreshes, logins *prometheus.CounterVec, registered *prometheus.GaugeVec) {
	refreshes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "token_refreshes_total",
		Help: "Garmin token refresh attempts by outcome.",
	}, []string{labelOutcome})

	logins = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "login_attempts_total",
		Help: "Garmin login attempts by strategy and outcome.",
	}, []string{"strategy", labelOutcome})

	registered = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace, Name: "registered_tools",
		Help: "Tools registered on this server, by policy tier.",
	}, []string{labelTier})

	return refreshes, logins, registered
}

// New builds a Recorder with its own registry, the Go and process collectors,
// and the build_info gauge already set.
func New(cfg Config) *Recorder {
	toolCalls, toolDuration, toolArgumentBytes, toolResultBytes := toolVectors()
	upstreamRequests, upstreamDuration, upstreamResponseBytes := upstreamVectors()
	tokenRefreshes, loginAttempts, registeredTools := authVectors()

	r := &Recorder{
		registry: prometheus.NewRegistry(),

		toolCalls:         toolCalls,
		toolDuration:      toolDuration,
		toolArgumentBytes: toolArgumentBytes,
		toolResultBytes:   toolResultBytes,

		upstreamRequests:      upstreamRequests,
		upstreamDuration:      upstreamDuration,
		upstreamResponseBytes: upstreamResponseBytes,

		tokenRefreshes:  tokenRefreshes,
		loginAttempts:   loginAttempts,
		registeredTools: registeredTools,
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace, Name: "build_info",
		Help: "Always 1, labelled with the build identity.",
	}, []string{"version", "commit"})
	buildInfo.WithLabelValues(cfg.Version, cfg.Commit).Set(1)

	r.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		r.toolCalls, r.toolDuration, r.toolArgumentBytes, r.toolResultBytes,
		r.upstreamRequests, r.upstreamDuration, r.upstreamResponseBytes,
		r.tokenRefreshes, r.loginAttempts, r.registeredTools, buildInfo,
	)
	return r
}

// Handler serves this Recorder's exposition. A nil Recorder serves 404, so a
// disabled deployment that still routes the path discloses nothing.
func (r *Recorder) Handler() http.Handler {
	if r == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}

// ToolCall records one completed tool call from the same event the logger takes.
//
// event.Arguments and event.Reason are deliberately ignored: both are free text,
// and a label outlives the redaction rules that bound a log line.
func (r *Recorder) ToolCall(event mcplog.ToolEvent) {
	if r == nil {
		return
	}
	r.toolCalls.WithLabelValues(event.ToolName, event.Category, event.Tier,
		event.Outcome.String(), event.Status.String(), event.PrincipalID).Inc()
	r.toolDuration.WithLabelValues(event.ToolName, event.Category, event.Tier).
		Observe(event.Latency.Seconds())
	r.toolArgumentBytes.WithLabelValues(event.Category, event.Tier).
		Observe(float64(event.ArgumentBytes))
	r.toolResultBytes.WithLabelValues(event.Category, event.Tier).
		Observe(float64(event.ResultBytes))
}

// UpstreamRequest records one Garmin call.
//
// endpoint is the client package's own constant label, never a request URL: a
// URL names an activity, a date or an account object, and the label does not.
func (r *Recorder) UpstreamRequest(
	endpoint, op string, status, responseBytes int, latency time.Duration,
) {
	if r == nil {
		return
	}
	r.upstreamRequests.WithLabelValues(endpoint, op, strconv.Itoa(status)).Inc()
	r.upstreamDuration.WithLabelValues(endpoint).Observe(latency.Seconds())
	r.upstreamResponseBytes.WithLabelValues(endpoint).Observe(float64(responseBytes))
}

// TokenRefresh records one Garmin token refresh attempt.
func (r *Recorder) TokenRefresh(outcome string) {
	if r == nil {
		return
	}
	r.tokenRefreshes.WithLabelValues(outcome).Inc()
}

// LoginAttempt records one Garmin login attempt by the strategy that ran it.
func (r *Recorder) LoginAttempt(strategy, outcome string) {
	if r == nil {
		return
	}
	r.loginAttempts.WithLabelValues(strategy, outcome).Inc()
}

// SetRegisteredTools publishes how many tools one tier registered.
func (r *Recorder) SetRegisteredTools(tier string, count int) {
	if r == nil {
		return
	}
	r.registeredTools.WithLabelValues(tier).Set(float64(count))
}
