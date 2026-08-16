package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// hrvNight is one synthetic night, reusable for every day of a window.
const hrvNight = `{"hrvSummary":{"lastNightAvg":40.4,"lastNight5MinHigh":70.2,` +
	`"weeklyAvg":41.0,"status":"BALANCED","feedbackPhrase":"HRV_BALANCED"}}`

func TestHRVTrendWalksEveryDayOfTheWindow(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, trendDailyScript(client.PathHRVPrefix, allTrendDays(), hrvNight))
	out, err := h.svc.readHRVTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readHRVTrend() = %v", err)
	}

	if len(out.Trend) != 3 || out.DaysWithData != 3 {
		t.Fatalf("trend = %+v, want three days", out.Trend)
	}
	if got := len(h.fake.Requests()); got != 3 {
		t.Errorf("the fake received %d requests, want one per day", got)
	}
	if out.Trend[0].Date != trendStart || out.Trend[2].Date != trendEnd {
		t.Errorf("trend order = %s..%s, want oldest first", out.Trend[0].Date, out.Trend[2].Date)
	}
	if out.PeriodAvgHRVMs == nil || *out.PeriodAvgHRVMs != 40.4 {
		t.Errorf("period_avg_hrv_ms = %v, want 40.4", out.PeriodAvgHRVMs)
	}
	if !out.Coverage.Complete || out.Coverage.DaysRequested != 3 {
		t.Errorf("coverage = %+v, want a complete three-day window", out.Coverage)
	}
}

// TestHRVTrendMarksAPartialWindow is the rule that matters most here: a day that
// failed is named, and the result says it is incomplete rather than just being short.
func TestHRVTrendMarksAPartialWindow(t *testing.T) {
	t.Parallel()

	script := trendDailyScript(client.PathHRVPrefix, allTrendDays(), hrvNight).
		With(client.PathHRVPrefix+"/"+trendMid,
			testkit.JSON(http.StatusServiceUnavailable, `{"error":"synthetic"}`))
	h := newTrendHarness(t, script)

	out, err := h.svc.readHRVTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readHRVTrend() = %v", err)
	}
	if len(out.Trend) != 2 {
		t.Fatalf("trend = %+v, want the two days that were read", out.Trend)
	}
	if out.Coverage.Complete {
		t.Error("a window with a failed day reports itself complete")
	}
	if out.Coverage.DaysFailed != 1 || len(out.Coverage.Failures) != 1 {
		t.Fatalf("coverage = %+v, want one named failure", out.Coverage)
	}
	if out.Coverage.Failures[0].Date != trendMid {
		t.Errorf("failure = %q, want the middle day", out.Coverage.Failures[0].Date)
	}
}

// TestHRVTrendCountsDaysWithoutData keeps an unworn night apart from a failure.
func TestHRVTrendCountsDaysWithoutData(t *testing.T) {
	t.Parallel()

	script := trendDailyScript(client.PathHRVPrefix, allTrendDays(), hrvNight).
		With(client.PathHRVPrefix+"/"+trendMid,
			testkit.JSON(http.StatusOK, `{"hrvSummary":null}`))
	h := newTrendHarness(t, script)

	out, err := h.svc.readHRVTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readHRVTrend() = %v", err)
	}
	if !out.Coverage.Complete {
		t.Error("a window with an empty night reports itself incomplete")
	}
	if out.Coverage.DaysWithoutData != 1 || out.Coverage.DaysFailed != 0 {
		t.Errorf("coverage = %+v, want one day without data and no failure", out.Coverage)
	}
}

func TestHRVTrendFailsWhenNoDayCouldBeRead(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript()
	for _, day := range allTrendDays() {
		script = script.With(client.PathHRVPrefix+"/"+day,
			testkit.JSON(http.StatusServiceUnavailable, `{"error":"synthetic"}`))
	}
	h := newTrendHarness(t, script)

	if _, err := h.svc.readHRVTrend(h.ctx, trendStart, trendEnd); !errors.Is(
		err, client.ErrServer) {
		t.Fatalf("readHRVTrend() = %v, want the underlying failure", err)
	}
}

func TestHRVTrendRefusesAnOversizedWindowAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, trendDailyScript(client.PathHRVPrefix, allTrendDays(), hrvNight))
	if _, err := h.svc.readHRVTrend(h.ctx, "2026-01-01", "2026-04-01"); !errors.Is(
		err, ErrInvalidArgument) {
		t.Errorf("a 91-day window = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.readHRVTrend(t.Context(), trendStart, trendEnd); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestHRVTrendResultNeverLogsAReading(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, trendDailyScript(client.PathHRVPrefix, allTrendDays(), hrvNight))
	out, err := h.svc.readHRVTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readHRVTrend() = %v", err)
	}
	assertShapeOnly(t, "HRVTrend", out, "40.4", "70.2", "BALANCED")
}
