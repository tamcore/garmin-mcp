package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The fixtures below are hand-written from the field names upstream reads. None is a
// recording, and every value is invented.
const (
	trendStartDate = "2026-01-29"
	trendEndDate   = "2026-01-31"

	hrvBody = `{"hrvSummary":{"calendarDate":"2026-01-31","lastNightAvg":42.7,` +
		`"lastNight5MinHigh":78.3,"weeklyAvg":"41.5","status":"BALANCED",` +
		`"feedbackPhrase":"HRV_BALANCED","baseline":{"balancedLow":35.0,` +
		`"balancedUpper":55.0,"lowUpper":30.0}},` +
		`"sleepStartTimestampLocal":"2026-01-30T22:10:00.0",` +
		`"sleepEndTimestampLocal":"2026-01-31T06:10:00.0",` +
		`"hrvReadings":[{"readingTimeLocal":"2026-01-31T02:05:00.0","hrvValue":44.5}]}`

	// hrvTrendBody uses the other spelling upstream reads, and carries no baseline.
	hrvTrendBody = `{"hrvSummary":{"calendarDate":"2026-01-31","lastNight":39.5,` +
		`"weeklyAvg":41.0,"status":"LOW","feedbackPhrase":"HRV_LOW","baseline":null}}`

	trendStatusBody = `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{` +
		`"1001":{"calendarDate":"2026-01-31","primaryTrainingDevice":false,` +
		`"trainingStatus":9,"trainingStatusFeedbackPhrase":"OTHER_DEVICE"},` +
		`"3001":{"calendarDate":"2026-01-31","primaryTrainingDevice":true,` +
		`"trainingStatus":3,"trainingStatusFeedbackPhrase":"PRODUCTIVE_1",` +
		`"fitnessTrend":1,"acuteTrainingLoadDTO":{"dailyTrainingLoadAcute":320.5,` +
		`"dailyTrainingLoadChronic":300.5,"dailyAcuteChronicWorkloadRatio":1.07,` +
		`"acwrStatus":"OPTIMAL","acwrPercent":63,"minTrainingLoadChronic":250.0,` +
		`"maxTrainingLoadChronic":420.0}}}},` +
		`"mostRecentVO2Max":{"generic":{"calendarDate":"2026-01-31",` +
		`"vo2MaxValue":52.0,"vo2MaxPreciseValue":52.3},"cycling":null},` +
		`"mostRecentTrainingLoadBalance":{"metricsTrainingLoadBalanceDTOMap":{` +
		`"3001":{"calendarDate":"2026-01-31","primaryTrainingDevice":true,` +
		`"trainingBalanceFeedbackPhrase":"AEROBIC_HIGH_SHORTAGE",` +
		`"monthlyLoadAerobicLow":900.9,"monthlyLoadAerobicLowTargetMin":800.0,` +
		`"monthlyLoadAerobicLowTargetMax":1200.0,"monthlyLoadAerobicHigh":120.0,` +
		`"monthlyLoadAerobicHighTargetMin":200.0,"monthlyLoadAerobicHighTargetMax":400.0,` +
		`"monthlyLoadAnaerobic":90.0,"monthlyLoadAnaerobicTargetMin":50.0,` +
		`"monthlyLoadAnaerobicTargetMax":150.0}}}}`
)

func newTrainingTrends(t *testing.T, h harness) *api.TrainingTrends {
	t.Helper()

	trends, err := api.NewTrainingTrends(h.rc)
	if err != nil {
		t.Fatalf("NewTrainingTrends() = %v", err)
	}
	return trends
}

func TestTrainingTrendsRefusesANilRequestLayer(t *testing.T) {
	t.Parallel()

	if _, err := api.NewTrainingTrends(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Errorf("NewTrainingTrends(nil) = %v, want ErrNotConfigured", err)
	}
}

func TestTrainingTrendsIsReachableFromTheWellnessClient(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	wellness, err := api.NewWellness(h.rc)
	if err != nil {
		t.Fatalf("NewWellness() = %v", err)
	}
	if wellness.TrainingTrends() == nil {
		t.Fatal("Wellness.TrainingTrends() = nil, want a client over the same request layer")
	}
}

func TestParseProgressMetricAcceptsAGarminFieldName(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		nameDistance:     true,
		"elevationGain":  true,
		"movingDuration": true,
		"metric2":        true,
		"":               false,
		"  ":             false,
		"has space":      false,
		"has-dash":       false,
		"9leading":       false,
		"../traversal":   false,
	}

	for value, want := range cases {
		metric, err := api.ParseProgressMetric(value)
		if got := err == nil; got != want {
			t.Errorf("ParseProgressMetric(%q) accepted = %v, want %v", value, got, want)
		}
		if want && metric.String() == "" {
			t.Errorf("ParseProgressMetric(%q) rendered empty", value)
		}
	}
}

func TestProgressSummaryReadsTheAggregateAndSendsUpstreamsParameters(t *testing.T) {
	t.Parallel()

	body := `[{"date":"2026-01-31","countOfActivities":12,"stats":{"running":{"distance":` +
		`{"count":8,"sum":84000.0,"avg":10500.0,"min":5000.0,"max":21000.0}}}}]`
	h := newHarness(t, testkit.NewScript().With(client.PathFitnessStatsActivity,
		testkit.JSON(http.StatusOK, body)), client.Limits{})

	metric, err := api.ParseProgressMetric(nameDistance)
	if err != nil {
		t.Fatalf("ParseProgressMetric() = %v", err)
	}
	summaries, err := newTrainingTrends(t, h).ProgressSummary(t.Context(), h.session,
		mustRange(t, trendStartDate, trendEndDate), metric)
	if err != nil {
		t.Fatalf("ProgressSummary() = %v", err)
	}

	if summaries.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", summaries.Len())
	}
	entry, ok := summaries.First()
	if !ok {
		t.Fatal("First() reported no entry")
	}
	if got, ok := entry.CountOfActivities.Int64(); !ok || got != 12 {
		t.Errorf("countOfActivities = %d (set %v), want 12", got, ok)
	}
	if _, ok := entry.Stats["running"]["distance"]; !ok {
		t.Errorf("stats carried no running/distance entry: %v", entry.Stats)
	}

	query := h.server.Requests()[0].Query
	for name, want := range map[string]string{
		client.QueryStartDate:                 trendStartDate,
		client.QueryEndDate:                   trendEndDate,
		client.QueryAggregation:               client.AggregationLifetime,
		client.QueryMetric:                    nameDistance,
		client.QueryGroupByParentActivityType: "True",
	} {
		if got := query.Get(name); got != want {
			t.Errorf("query %s = %q, want %q", name, got, want)
		}
	}
}

func TestProgressSummaryAcceptsASingleObject(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript().With(client.PathFitnessStatsActivity,
		testkit.JSON(http.StatusOK, `{"date":"2026-01-31","countOfActivities":1}`)),
		client.Limits{})

	metric, _ := api.ParseProgressMetric(nameDistance)
	summaries, err := newTrainingTrends(t, h).ProgressSummary(t.Context(), h.session,
		mustRange(t, trendStartDate, trendEndDate), metric)
	if err != nil {
		t.Fatalf("ProgressSummary() = %v", err)
	}
	if summaries.Len() != 1 {
		t.Errorf("Len() = %d, want the single object to decode as one entry", summaries.Len())
	}
}

func TestProgressSummaryRefusesAnUnsetMetricBeforeDispatch(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	_, err := newTrainingTrends(t, h).ProgressSummary(t.Context(), h.session,
		mustRange(t, trendStartDate, trendEndDate), api.ProgressMetric{})
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("ProgressSummary() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestProgressSummaryRefusesAWindowPastTheConfiguredBound(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 1})
	metric, _ := api.ParseProgressMetric(nameDistance)
	_, err := newTrainingTrends(t, h).ProgressSummary(t.Context(), h.session,
		mustRange(t, trendStartDate, trendEndDate), metric)
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("ProgressSummary() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestHRVDecodesBothNightlySpellings(t *testing.T) {
	t.Parallel()

	path := client.PathHRVPrefix + "/" + trendEndDate
	cases := map[string]struct {
		body string
		want float64
	}{
		"lastNightAvg": {body: hrvBody, want: 42.7},
		"lastNight":    {body: hrvTrendBody, want: 39.5},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, testkit.NewScript().With(path,
				testkit.JSON(http.StatusOK, tc.body)), client.Limits{})
			day, err := newTrainingTrends(t, h).HRV(t.Context(), h.session,
				mustDate(t, trendEndDate))
			if err != nil {
				t.Fatalf("HRV() = %v", err)
			}
			if day.Summary == nil {
				t.Fatal("HRV() decoded no summary")
			}
			got, ok := day.Summary.NightAverage().Float64()
			if !ok || got != tc.want {
				t.Errorf("NightAverage() = %v (set %v), want %v", got, ok, tc.want)
			}
		})
	}
}

func TestHRVDecodesTheStringWeeklyAverageAndTheReadings(t *testing.T) {
	t.Parallel()

	path := client.PathHRVPrefix + "/" + trendEndDate
	h := newHarness(t, testkit.NewScript().With(path,
		testkit.JSON(http.StatusOK, hrvBody)), client.Limits{})

	day, err := newTrainingTrends(t, h).HRVForTrend(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("HRVForTrend() = %v", err)
	}
	if got, ok := day.Summary.WeeklyAvg.Float64(); !ok || got != 41.5 {
		t.Errorf("weeklyAvg = %v (set %v), want the numeric string to decode as 41.5", got, ok)
	}
	if len(day.Readings) != 1 {
		t.Fatalf("readings = %d, want 1", len(day.Readings))
	}
	if got, ok := day.Readings[0].HRVValue.Float64(); !ok || got != 44.5 {
		t.Errorf("reading = %v (set %v), want 44.5", got, ok)
	}
	if day.Summary.Baseline == nil {
		t.Error("the baseline band did not decode")
	}
}

func TestHRVRefusesAnUnsetDateBeforeDispatch(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newTrainingTrends(t, h).HRV(t.Context(), h.session,
		client.Date{}); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("HRV() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
