package tools

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// respirationDay is one synthetic day of breathing rate.
const respirationDay = `{"calendarDate":"2026-01-31","lowestRespirationValue":11.1,` +
	`"highestRespirationValue":19.2,"avgWakingRespirationValue":14.3,` +
	`"avgSleepRespirationValue":13.4}`

func respirationTrendScript() testkit.Script {
	return trendDailyScript(client.PathDailyRespirationPrefix, allTrendDays(), respirationDay)
}

func TestRespirationTrendWalksEveryDayOfTheWindow(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, respirationTrendScript())
	out, err := h.svc.readRespirationTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readRespirationTrend() = %v", err)
	}

	if len(out.Trend) != 3 || out.DaysWithData != 3 {
		t.Fatalf("trend = %+v, want three days", out.Trend)
	}
	if out.PeriodAvgSleepPerMin == nil || *out.PeriodAvgSleepPerMin != 13.4 {
		t.Errorf("period_avg_sleep_breaths_per_min = %v, want 13.4",
			out.PeriodAvgSleepPerMin)
	}
	if !out.Coverage.Complete {
		t.Errorf("coverage = %+v, want complete", out.Coverage)
	}
	point := out.Trend[0]
	if point.LowestBreathsPerMin == nil || point.HighestBreathsPerMin == nil ||
		point.AvgWakingBreathsPerMin == nil {
		t.Errorf("point = %+v, want every scalar carried through", point)
	}

	// The trend reads the health tier's document, not a path of its own.
	for _, request := range h.fake.Requests() {
		if !strings.HasPrefix(request.Path, client.PathDailyRespirationPrefix) {
			t.Errorf("path = %q, want the daily respiration prefix", request.Path)
		}
	}
}

// TestRespirationTrendStopsOnARateLimitAndReportsWhatItHas is the deliberate choice: a
// rate limit ends the walk instead of collecting the same refusal once per remaining
// day, and the days already read are still returned, marked incomplete.
func TestRespirationTrendStopsOnARateLimitAndReportsWhatItHas(t *testing.T) {
	t.Parallel()

	script := respirationTrendScript().
		With(client.PathDailyRespirationPrefix+"/"+trendMid,
			testkit.JSON(http.StatusTooManyRequests, `{"error":"synthetic"}`))
	h := newTrendHarness(t, script)

	out, err := h.svc.readRespirationTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readRespirationTrend() = %v", err)
	}
	if len(out.Trend) != 1 {
		t.Fatalf("trend = %+v, want the one day read before the limit", out.Trend)
	}
	if !out.Coverage.StoppedEarly || out.Coverage.Complete {
		t.Errorf("coverage = %+v, want stopped early and incomplete", out.Coverage)
	}
	if out.Coverage.StopReason == "" {
		t.Error("the stopped walk reports no reason")
	}
	if got := len(h.fake.Requests()); got != 2 {
		t.Errorf("the fake received %d requests, want the walk to stop at the limit", got)
	}
}

func TestRespirationTrendCountsAnEmptyDay(t *testing.T) {
	t.Parallel()

	script := respirationTrendScript().
		With(client.PathDailyRespirationPrefix+"/"+trendMid,
			testkit.JSON(http.StatusOK, `{"calendarDate":"2026-01-30"}`))
	h := newTrendHarness(t, script)

	out, err := h.svc.readRespirationTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readRespirationTrend() = %v", err)
	}
	if out.Coverage.DaysWithoutData != 1 || out.Coverage.DaysWithData != 2 {
		t.Errorf("coverage = %+v, want one empty day and two with data", out.Coverage)
	}
	if !out.Coverage.Complete {
		t.Error("an empty day made the window incomplete; it is not a failure")
	}
}

func TestRespirationTrendRefusesAnOversizedWindowAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, respirationTrendScript())
	if _, err := h.svc.readRespirationTrend(h.ctx, "2026-01-01",
		"2026-02-15"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("a 46-day window = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.readRespirationTrend(t.Context(), trendStart, trendEnd); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestRespirationTrendResultNeverLogsAReading(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, respirationTrendScript())
	out, err := h.svc.readRespirationTrend(h.ctx, trendStart, trendEnd)
	if err != nil {
		t.Fatalf("readRespirationTrend() = %v", err)
	}
	assertShapeOnly(t, "RespirationTrend", out, "13.4", "14.3", "19.2", "11.1")
}
