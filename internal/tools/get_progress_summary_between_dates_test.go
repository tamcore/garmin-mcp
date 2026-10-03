package tools

import (
	"errors"
	"math"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// metricDistance and typeCycling name the values these fixtures reuse.
const (
	metricDistance = "distance"
	typeCycling    = "cycling"
)

// progressBody is synthetic: four activity types, one of which carries a different
// metric and one of which counts no activity, so the filters are visible in the result.
const progressBody = `[{"date":"2026-01-31","countOfActivities":99,"stats":{` +
	`"running":{"distance":{"count":8,"sum":84000.0,"avg":10500.0,"min":5000.0,` +
	`"max":21000.0}},` +
	`"cycling":{"distance":{"count":4,"sum":120000.0,"avg":30000.0}},` +
	`"walking":{"distance":{"count":0,"sum":0.0}},` +
	`"swimming":{"duration":{"count":2,"sum":3600.0}}}}]`

func progressScript(body string) testkit.Script {
	return testkit.NewScript().With(client.PathFitnessStatsActivity,
		testkit.JSON(http.StatusOK, body))
}

func TestProgressSummaryReportsOneEntryPerContributingActivityType(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, progressScript(progressBody))
	out, err := h.svc.readProgressSummary(h.ctx, getProgressSummaryInput{
		StartDate: trendStart, EndDate: trendEnd, Metric: metricDistance,
	})
	if err != nil {
		t.Fatalf("readProgressSummary() = %v", err)
	}

	if !out.HasData {
		t.Errorf("summary = %+v, want data", out)
	}
	// Garmin's own countOfActivities matches neither the activities nor the per-type
	// counts, so the result sums the counts it reports (training.py:409-411).
	if out.CountOfActivities == nil || *out.CountOfActivities != 12 {
		t.Errorf("count_of_activities = %v, want 12", out.CountOfActivities)
	}
	if len(out.StatsByActivityType) != 2 {
		t.Fatalf("stats = %+v, want only the two types counting distance", out.StatsByActivityType)
	}
	// Sorted, so two identical calls report the same list.
	if out.StatsByActivityType[0].ActivityType != typeCycling {
		t.Errorf("first entry = %q, want the list ordered",
			out.StatsByActivityType[0].ActivityType)
	}
	if out.Truncated {
		t.Error("a two-entry summary reports itself truncated")
	}
	running := out.StatsByActivityType[1]
	if running.Sum == nil || *running.Sum != 84000 {
		t.Errorf("running sum = %v, want 84000", running.Sum)
	}
	if running.Min == nil || running.Max == nil {
		t.Error("running carried no min or max")
	}
}

// TestProgressSummaryReportsCaloriesInKilocalories undoes the factor Garmin stores
// the calories metric under (training.py:29-41).
func TestProgressSummaryReportsCaloriesInKilocalories(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, progressScript(`[{"stats":{"running":{"calories":`+
		`{"count":2,"sum":4190.0,"avg":2095.0,"min":838.0,"max":3352.0}}}}]`))
	out, err := h.svc.readProgressSummary(h.ctx, getProgressSummaryInput{
		StartDate: trendStart, EndDate: trendEnd, Metric: "calories",
	})
	if err != nil {
		t.Fatalf("readProgressSummary() = %v", err)
	}
	if len(out.StatsByActivityType) != 1 {
		t.Fatalf("stats = %+v, want one entry", out.StatsByActivityType)
	}
	stats := out.StatsByActivityType[0]
	for name, pair := range map[string]struct {
		got  *float64
		want float64
	}{"sum": {stats.Sum, 1000}, "avg": {stats.Avg, 500}, "min": {stats.Min, 200}, "max": {stats.Max, 800}} {
		if pair.got == nil || math.Abs(*pair.got-pair.want) > 1e-9 {
			t.Errorf("%s = %v, want %v kcal", name, pair.got, pair.want)
		}
	}
}

func TestProgressSummaryReportsAnEmptyDocument(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, progressScript(`[]`))
	out, err := h.svc.readProgressSummary(h.ctx, getProgressSummaryInput{
		StartDate: trendStart, EndDate: trendEnd, Metric: metricDistance,
	})
	if err != nil {
		t.Fatalf("readProgressSummary() = %v", err)
	}
	if out.HasData || len(out.StatsByActivityType) != 0 {
		t.Errorf("summary = %+v, want no data and an empty list", out)
	}
}

func TestProgressSummaryRefusesABadMetricBeforeAnyGarminCall(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, progressScript(progressBody))
	for _, metric := range []string{"", "has space", "../../etc", "9leading"} {
		_, err := h.svc.readProgressSummary(h.ctx, getProgressSummaryInput{
			StartDate: trendStart, EndDate: trendEnd, Metric: metric,
		})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("metric %q = %v, want ErrInvalidArgument", metric, err)
		}
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestProgressSummaryRefusesAMalformedWindowBeforeAnyGarminCall(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, progressScript(progressBody))
	_, err := h.svc.readProgressSummary(h.ctx, getProgressSummaryInput{
		StartDate: trendEnd, EndDate: trendStart, Metric: metricDistance,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("readProgressSummary() = %v, want ErrInvalidArgument", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestProgressSummaryRefusesARequestWithNoPrincipal(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, progressScript(progressBody))
	_, err := h.svc.readProgressSummary(t.Context(), getProgressSummaryInput{
		StartDate: trendStart, EndDate: trendEnd, Metric: metricDistance,
	})
	if !errors.Is(err, identity.ErrNoPrincipal) {
		t.Fatalf("readProgressSummary() = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestProgressSummaryReportsGarminsFailureAsAdvice(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, testkit.NewScript().With(client.PathFitnessStatsActivity,
		testkit.JSON(http.StatusNotFound, `{"error":"synthetic"}`)))
	_, err := h.svc.readProgressSummary(h.ctx, getProgressSummaryInput{
		StartDate: trendStart, EndDate: trendEnd, Metric: metricDistance,
	})
	if !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("readProgressSummary() = %v, want ErrNotFound", err)
	}
	if err.Error() != AdviceNoSuchRecord {
		t.Errorf("advice = %q, want the authored no-record advice", err.Error())
	}
}
