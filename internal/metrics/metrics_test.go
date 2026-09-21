package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/metrics"
)

const (
	toolGetActivities  = "get_activities"
	tierReadOnly       = "read-only"
	categoryActivities = "activities"
)

// scrape renders the recorder's current exposition, the way Prometheus would
// read it. Asserting on the rendered text rather than on internal state is what
// makes a wrong metric name or a wrong label a test failure.
func scrape(t *testing.T, recorder *metrics.Recorder) string {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("the metrics handler answered %d, want 200", response.Code)
	}
	return response.Body.String()
}

func TestNilRecorderIsANoOpOnEveryMethod(t *testing.T) {
	var recorder *metrics.Recorder

	// Each call must survive a nil receiver, because a deployment without metrics
	// leaves the field nil and no caller should branch on that.
	recorder.ToolCall(mcplog.ToolEvent{ToolName: toolGetActivities})
	recorder.UpstreamRequest(categoryActivities, "GET", 200, 512, time.Second)
	recorder.TokenRefresh("ok")
	recorder.LoginAttempt("di", "ok")
	recorder.SetRegisteredTools(tierReadOnly, 100)
}

func TestToolCallRecordsTheCounterWithEveryLabel(t *testing.T) {
	recorder := metrics.New(metrics.Config{Version: "v1.2.3", Commit: "abc1234"})

	recorder.ToolCall(mcplog.ToolEvent{
		ToolName:    "get_sleep_data",
		Category:    "health",
		Tier:        tierReadOnly,
		PrincipalID: "principal-7",
		Outcome:     mcplog.OutcomeOK,
		Status:      mcplog.StatusSuccess,
		Latency:     250 * time.Millisecond,
	})

	body := scrape(t, recorder)
	want := `garmin_mcp_tool_calls_total{category="health",outcome="ok",principal="principal-7",` +
		`status="success",tier="read-only",tool="get_sleep_data"} 1`
	if !strings.Contains(body, want) {
		t.Fatalf("the exposition is missing\n%s\ngot:\n%s", want, body)
	}
}

// TestToolCallNeverRendersArgumentsOrReason is the redaction rule as a test. A
// ToolEvent carries two free-text fields, and a label is retained by a scraper
// for months with no redaction path, so neither may ever become one.
func TestToolCallNeverRendersArgumentsOrReason(t *testing.T) {
	recorder := metrics.New(metrics.Config{})

	recorder.ToolCall(mcplog.ToolEvent{
		ToolName:  "add_weigh_in",
		Category:  "health",
		Tier:      "write",
		Outcome:   mcplog.OutcomeError,
		Status:    mcplog.StatusUpstreamError,
		Arguments: `{"weight":81.4}`,
		Reason:    "the upstream service refused the write",
	})

	body := scrape(t, recorder)
	for _, forbidden := range []string{"81.4", "weight", "refused the write"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("the exposition leaked %q:\n%s", forbidden, body)
		}
	}
}

func TestToolCallRecordsTheLatencyHistogram(t *testing.T) {
	recorder := metrics.New(metrics.Config{})

	recorder.ToolCall(mcplog.ToolEvent{
		ToolName: toolGetActivities, Category: categoryActivities, Tier: tierReadOnly,
		Outcome: mcplog.OutcomeOK, Status: mcplog.StatusSuccess,
		Latency: 300 * time.Millisecond,
	})

	body := scrape(t, recorder)
	if !strings.Contains(body, "garmin_mcp_tool_duration_seconds_bucket") {
		t.Fatalf("the latency histogram is missing:\n%s", body)
	}
	// 0.3s lands above the 0.25 bucket and at or below the 0.5 one.
	want := `garmin_mcp_tool_duration_seconds_bucket{category="activities",tier="read-only",` +
		`tool="get_activities",le="0.5"} 1`
	if !strings.Contains(body, want) {
		t.Fatalf("the 0.5s bucket did not count the call:\n%s", body)
	}
}

// TestToolDurationCarriesNoPrincipalLabel pins the one cardinality rule the
// design does keep: a principal on a ten-bucket histogram multiplies series for
// a number the counter plus a per-tool histogram already give.
func TestToolDurationCarriesNoPrincipalLabel(t *testing.T) {
	recorder := metrics.New(metrics.Config{})

	recorder.ToolCall(mcplog.ToolEvent{
		ToolName: toolGetActivities, Category: categoryActivities, Tier: tierReadOnly,
		PrincipalID: "principal-7", Outcome: mcplog.OutcomeOK, Latency: time.Second,
	})

	for line := range strings.SplitSeq(scrape(t, recorder), "\n") {
		if strings.HasPrefix(line, "garmin_mcp_tool_duration_seconds") &&
			strings.Contains(line, "principal=") {
			t.Fatalf("the duration histogram carries a principal label: %s", line)
		}
	}
}

func TestToolCallRecordsTheSizeHistograms(t *testing.T) {
	recorder := metrics.New(metrics.Config{})

	recorder.ToolCall(mcplog.ToolEvent{
		ToolName: toolGetActivities, Category: categoryActivities, Tier: tierReadOnly,
		Outcome: mcplog.OutcomeOK, ArgumentBytes: 64, ResultBytes: 4096,
	})

	body := scrape(t, recorder)
	for _, name := range []string{
		"garmin_mcp_tool_argument_bytes_bucket",
		"garmin_mcp_tool_result_bytes_bucket",
	} {
		if !strings.Contains(body, name) {
			t.Fatalf("%s is missing:\n%s", name, body)
		}
	}
}

func TestUpstreamRequestRecordsTheEndpointAndStatus(t *testing.T) {
	recorder := metrics.New(metrics.Config{})

	recorder.UpstreamRequest("activity-service", "GET", 200, 2048, 120*time.Millisecond)

	body := scrape(t, recorder)
	want := `garmin_mcp_upstream_requests_total{endpoint="activity-service",op="GET",status="200"} 1`
	if !strings.Contains(body, want) {
		t.Fatalf("the exposition is missing\n%s\ngot:\n%s", want, body)
	}
	for _, name := range []string{
		"garmin_mcp_upstream_duration_seconds_bucket",
		"garmin_mcp_upstream_response_bytes_bucket",
	} {
		if !strings.Contains(body, name) {
			t.Fatalf("%s is missing:\n%s", name, body)
		}
	}
}

func TestAuthCountersRecord(t *testing.T) {
	recorder := metrics.New(metrics.Config{})

	recorder.TokenRefresh("ok")
	recorder.TokenRefresh("error")
	recorder.LoginAttempt("di-oauth", "ok")

	body := scrape(t, recorder)
	for _, want := range []string{
		`garmin_mcp_token_refreshes_total{outcome="ok"} 1`,
		`garmin_mcp_token_refreshes_total{outcome="error"} 1`,
		`garmin_mcp_login_attempts_total{outcome="ok",strategy="di-oauth"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the exposition is missing\n%s\ngot:\n%s", want, body)
		}
	}
}

func TestBuildInfoAndRegisteredToolsAreGauges(t *testing.T) {
	recorder := metrics.New(metrics.Config{Version: "v1.2.3", Commit: "abc1234"})
	recorder.SetRegisteredTools(tierReadOnly, 100)
	recorder.SetRegisteredTools("write", 35)

	body := scrape(t, recorder)
	for _, want := range []string{
		`garmin_mcp_build_info{commit="abc1234",version="v1.2.3"} 1`,
		`garmin_mcp_registered_tools{tier="read-only"} 100`,
		`garmin_mcp_registered_tools{tier="write"} 35`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the exposition is missing\n%s\ngot:\n%s", want, body)
		}
	}
}

// TestRuntimeCollectorsAreRegistered proves the Go and process collectors reach
// the private registry. They are the reason this package takes a dependency
// instead of writing the exposition format by hand.
func TestRuntimeCollectorsAreRegistered(t *testing.T) {
	body := scrape(t, metrics.New(metrics.Config{}))

	for _, want := range []string{"go_goroutines", "go_memstats_alloc_bytes", "process_"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the runtime collector metric %q is missing:\n%s", want, body)
		}
	}
}

// TestTwoRecordersDoNotCollide proves the registry is private to the Recorder.
// Against the default global registerer the second New would panic on duplicate
// registration, which is exactly what a test binary running two servers hits.
func TestTwoRecordersDoNotCollide(t *testing.T) {
	first := metrics.New(metrics.Config{})
	second := metrics.New(metrics.Config{})

	first.TokenRefresh("ok")
	if strings.Contains(scrape(t, second), `garmin_mcp_token_refreshes_total{outcome="ok"} 1`) {
		t.Fatal("the two recorders share a registry")
	}
}
