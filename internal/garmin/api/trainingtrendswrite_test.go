package api_test

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The write and the redaction half of the trend tests: the epoch reload, the
// respiration day the trend reuses, and the proof that no model logs a reading. The
// fixtures live in trainingtrends_test.go and are synthetic there too.

func TestRequestReloadPostsTheDayWithNoBody(t *testing.T) {
	t.Parallel()

	path := client.PathEpochReloadRequestPrefix + "/" + trendEndDate
	h := newHarness(t, testkit.NewScript().With(path,
		testkit.JSON(http.StatusOK, `{"accepted":true}`)), client.Limits{})

	result, err := newTrainingTrends(t, h).RequestReload(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("RequestReload() = %v", err)
	}
	if result.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", result.Status)
	}

	requests := h.server.Requests()
	if len(requests) != 1 || requests[0].Method != http.MethodPost {
		t.Fatalf("requests = %+v, want one POST", requests)
	}
	if len(requests[0].Body) != 0 {
		t.Errorf("the reload carried a body of %d bytes, want none", len(requests[0].Body))
	}
}

// TestRequestReloadIsRetriedAfterATransientFailure pins the declared effect. The
// reload is keyed by the account and the day and creates no record, so
// EffectIdempotentWrite is what lets a 503 be retried instead of surfacing as a failed
// reload the caller has to repeat by hand.
func TestRequestReloadIsRetriedAfterATransientFailure(t *testing.T) {
	t.Parallel()

	path := client.PathEpochReloadRequestPrefix + "/" + trendEndDate
	h := newHarness(t, serverErrors(path), retryLimits())

	_, err := newTrainingTrends(t, h).RequestReload(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if !errors.Is(err, client.ErrServer) {
		t.Fatalf("RequestReload() = %v, want ErrServer", err)
	}
	if got := len(h.server.Requests()); got != 3 {
		t.Errorf("the fake received %d requests, want 3 attempts of the idempotent POST", got)
	}
}

func TestRequestReloadRefusesAnUnsetDateBeforeDispatch(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newTrainingTrends(t, h).RequestReload(t.Context(), h.session,
		client.Date{}); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("RequestReload() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestRespirationTrendDayReadsTheHealthTiersDocument(t *testing.T) {
	t.Parallel()

	path := client.PathDailyRespirationPrefix + "/" + trendEndDate
	body := `{"calendarDate":"2026-01-31","lowestRespirationValue":11.0,` +
		`"highestRespirationValue":19.0,"avgWakingRespirationValue":14.0,` +
		`"avgSleepRespirationValue":13.0}`
	h := newHarness(t, testkit.NewScript().With(path,
		testkit.JSON(http.StatusOK, body)), client.Limits{})

	wellness, err := api.NewWellness(h.rc)
	if err != nil {
		t.Fatalf("NewWellness() = %v", err)
	}
	day, err := wellness.Cardio().RespirationTrendDay(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("RespirationTrendDay() = %v", err)
	}
	if got, ok := day.AvgSleepRespirationValue.Float64(); !ok || got != 13 {
		t.Errorf("avgSleepRespirationValue = %v (set %v), want 13", got, ok)
	}
	if got := h.server.Requests()[0].Path; got != path {
		t.Errorf("path = %q, want the health tier's daily respiration path", got)
	}
}

// TestMaxMetricsAcceptsASingleObjectAndTheEntryLevelDate covers the tolerant shapes:
// a singular document rather than a list, and a date carried on the entry rather than
// inside the per-sport section.
func TestMaxMetricsAcceptsASingleObjectAndTheEntryLevelDate(t *testing.T) {
	t.Parallel()

	body := `{"calendarDate":"2026-01-31","generic":{"vo2MaxValue":52.0}}`
	path := client.PathMaxMetricsPrefix + "/" + trendStartDate + "/" + trendEndDate
	h := newHarness(t, testkit.NewScript().With(path,
		testkit.JSON(http.StatusOK, body)), client.Limits{})

	metrics, err := newTrainingTrends(t, h).MaxMetrics(t.Context(), h.session,
		mustRange(t, trendStartDate, trendEndDate))
	if err != nil {
		t.Fatalf("MaxMetrics() = %v", err)
	}

	days := metrics.Days()
	if len(days) != 1 {
		t.Fatalf("Days() = %d, want the single object to decode as one entry", len(days))
	}
	if date, ok := days[0].Day(); !ok || date != trendEndDate {
		t.Errorf("Day() = %q (ok %v), want the entry's own date", date, ok)
	}
	if got, _ := days[0].Generic.Value().Float64(); got != 52 {
		t.Errorf("Value() = %v, want the rounded value when no precise one arrived", got)
	}
	if metrics.Payload().Status() != http.StatusOK {
		t.Errorf("Payload() status = %d, want 200", metrics.Payload().Status())
	}

	var undated api.MaxMetricsDay
	if date, ok := undated.Day(); ok {
		t.Errorf("Day() on an undated entry = %q, want it reported absent", date)
	}
}

// TestMaxMetricsRefusesAnUndecodableBody keeps a shape drift a decode failure rather
// than a silently empty series.
func TestMaxMetricsRefusesAnUndecodableBody(t *testing.T) {
	t.Parallel()

	path := client.PathMaxMetricsPrefix + "/" + trendStartDate + "/" + trendEndDate
	h := newHarness(t, testkit.NewScript().With(path,
		testkit.JSON(http.StatusOK, `"not-a-document"`)), client.Limits{})

	_, err := newTrainingTrends(t, h).MaxMetrics(t.Context(), h.session,
		mustRange(t, trendStartDate, trendEndDate))
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Fatalf("MaxMetrics() = %v, want ErrMalformedPayload", err)
	}
}

// TestTrainingTrendModelsAreNotLoggable proves the trend models report shape rather
// than a reading, which is the rule every model in this package follows.
func TestTrainingTrendModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	statusPath := client.PathTrainingStatusPrefix + "/" + trendEndDate
	hrvPath := client.PathHRVPrefix + "/" + trendEndDate
	h := newHarness(t, testkit.NewScript().
		With(hrvPath, testkit.JSON(http.StatusOK, hrvBody)).
		With(statusPath, testkit.JSON(http.StatusOK, trendStatusBody)),
		client.Limits{})

	trends := newTrainingTrends(t, h)
	hrv, err := trends.HRV(t.Context(), h.session, mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("HRV() = %v", err)
	}
	status, err := trends.TrainingLoadDay(t.Context(), h.session, mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("TrainingLoadDay() = %v", err)
	}

	settings := newHarness(t, testkit.NewScript().With(client.PathUserSettings,
		testkit.JSON(http.StatusOK, `{"userData":{"vo2MaxRunning":51.7}}`)), client.Limits{})
	profile, err := newTrainingTrends(t, settings).ProfileVO2Max(t.Context(), settings.session)
	if err != nil {
		t.Fatalf("ProfileVO2Max() = %v", err)
	}
	if profile.Payload().Status() != http.StatusOK {
		t.Errorf("Payload() status = %d, want 200", profile.Payload().Status())
	}

	metricsHarness := newHarness(t, testkit.NewScript().With(
		client.PathMaxMetricsPrefix+"/"+trendStartDate+"/"+trendEndDate,
		testkit.JSON(http.StatusOK,
			`[{"generic":{"calendarDate":"2026-01-31","vo2MaxValue":52.3}}]`)), client.Limits{})
	metrics, err := newTrainingTrends(t, metricsHarness).MaxMetrics(t.Context(),
		metricsHarness.session, mustRange(t, trendStartDate, trendEndDate))
	if err != nil {
		t.Fatalf("MaxMetrics() = %v", err)
	}

	needles := []string{"42.7", "78.3", "44.5", "320.5", "52.3", "900.9", "51.7"}
	models := map[string]slog.LogValuer{
		"HRVDay":         hrv,
		"TrainingStatus": status,
		"ProfileVO2Max":  profile,
		"MaxMetrics":     metrics,
	}
	for name, value := range models {
		// The timestamp is dropped before matching: a wall clock contains arbitrary
		// digits, and a needle that matched one would be a false alarm, not a leak.
		var logged strings.Builder
		options := &slog.HandlerOptions{ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		}}
		slog.New(slog.NewTextHandler(&logged, options)).Info("garmin read", "model", value)

		rendered := logged.String()
		for _, needle := range needles {
			if strings.Contains(rendered, needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
			}
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}
