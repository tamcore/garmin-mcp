package cmd

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/mcplog"
)

const testMetricsAddress = "127.0.0.1:9090"

// TestMetricsServerServesOnlyTheMetricsPath is the whole contract of the
// separate listener: the exposition is there, and nothing else is.
func TestMetricsServerServesOnlyTheMetricsPath(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding an ephemeral port: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "garmin_mcp_build_info 1\n")
	})
	done := make(chan error, 1)
	go func() { done <- serveMetricsOn(ctx, listener, handler) }()

	base := "http://" + listener.Addr().String()
	if body, code := getForTest(t, base+"/metrics"); code != http.StatusOK ||
		body != "garmin_mcp_build_info 1\n" {
		t.Fatalf("/metrics answered %d %q", code, body)
	}
	for _, path := range []string{"/", "/mcp", "/readyz", "/debug/pprof/"} {
		if _, code := getForTest(t, base+path); code != http.StatusNotFound {
			t.Fatalf("%s answered %d, want 404 — the metrics listener serves one path", path, code)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled context must be a graceful stop, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the metrics server did not stop within 5s of cancellation")
	}
}

func getForTest(t *testing.T, url string) (string, int) {
	t.Helper()

	response, err := http.Get(url) //nolint:noctx // a loopback test request
	if err != nil {
		t.Fatalf("requesting %s: %v", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", url, err)
	}
	return string(body), response.StatusCode
}

// TestNoMetricsAddressBuildsNoRecorder proves metrics are genuinely opt-in: with
// the setting empty, nothing is built and nothing binds.
func TestNoMetricsAddressBuildsNoRecorder(t *testing.T) {
	cfg := localConfig(t)
	cfg.MetricsAddress = ""

	deps, err := newDependencies(cfg, &wiring{Logs: io.Discard})
	if err != nil {
		t.Fatalf("assembling the dependencies: %v", err)
	}
	defer deps.close()

	if deps.metrics != nil {
		t.Fatal("an empty metrics address must build no recorder")
	}
}

// TestMetricsAddressBuildsARecorderWithBuildInfo proves the configured path
// produces a scrapeable recorder carrying the build identity.
func TestMetricsAddressBuildsARecorderWithBuildInfo(t *testing.T) {
	cfg := localConfig(t)
	cfg.MetricsAddress = testMetricsAddress

	deps, err := newDependencies(cfg, &wiring{
		Logs: io.Discard, Version: "v9.9.9", Commit: "deadbee",
	})
	if err != nil {
		t.Fatalf("assembling the dependencies: %v", err)
	}
	defer deps.close()

	if deps.metrics == nil {
		t.Fatal("a configured metrics address must build a recorder")
	}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	deps.metrics.Handler().ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(),
		`garmin_mcp_build_info{commit="deadbee",version="v9.9.9"} 1`) {
		t.Fatalf("the exposition lacks the build identity:\n%s", response.Body.String())
	}
}

// TestPublishedToolCountsCoverEveryTier pins that the registered-tool gauge is
// published per tier rather than as one total.
func TestPublishedToolCountsCoverEveryTier(t *testing.T) {
	cfg := localConfig(t)
	cfg.MetricsAddress = testMetricsAddress

	factory := func(_ ToolDeps) (ToolSet, error) {
		return ToolSet{
			Registrar: &fakeRegistrar{},
			ReadOnly:  []string{fakeReadTool},
			Write:     []string{fakeWriteTool},
		}, nil
	}
	deps, err := newDependencies(cfg, &wiring{Logs: io.Discard, Tools: factory})
	if err != nil {
		t.Fatalf("assembling the dependencies: %v", err)
	}
	defer deps.close()
	deps.publishToolCounts()

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	deps.metrics.Handler().ServeHTTP(response, request)
	body := response.Body.String()
	for _, want := range []string{
		`garmin_mcp_registered_tools{tier="read-only"} 2`,
		`garmin_mcp_registered_tools{tier="write"} 1`,
		`garmin_mcp_registered_tools{tier="destructive"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the exposition lacks %q:\n%s", want, body)
		}
	}
}

// TestServerDepsCarryTheRecorder pins the seam a compiling, fully unit-tested
// build can still leave unwired: serverDeps is the only constructor of
// mcpserver.Deps in the binary, so a missing Metrics field there makes every
// tool metric unreachable while nothing else fails.
func TestServerDepsCarryTheRecorder(t *testing.T) {
	cfg := localConfig(t)
	cfg.MetricsAddress = testMetricsAddress

	deps, err := newDependencies(cfg, &wiring{Logs: io.Discard})
	if err != nil {
		t.Fatalf("assembling the dependencies: %v", err)
	}
	defer deps.close()

	if deps.serverDeps("").Metrics == nil {
		t.Fatal("the MCP server was handed no recorder, so no tool call is ever recorded")
	}
}

// TestServerDepsSurviveANilRecorder covers the seam a configured-off deployment
// takes. The field is a non-nil interface holding a nil *metrics.Recorder, so
// the middleware's nil check passes and the call lands on a nil receiver, which
// is safe only because every Recorder method is nil-safe.
func TestServerDepsSurviveANilRecorder(t *testing.T) {
	cfg := localConfig(t)
	cfg.MetricsAddress = ""

	deps, err := newDependencies(cfg, &wiring{Logs: io.Discard})
	if err != nil {
		t.Fatalf("assembling the dependencies: %v", err)
	}
	defer deps.close()

	observer := deps.serverDeps("").Metrics
	if observer == nil {
		t.Skip("the seam holds no observer, so there is nothing to drive")
	}
	observer.ToolCall(mcplog.ToolEvent{ToolName: fakeReadTool})
}
