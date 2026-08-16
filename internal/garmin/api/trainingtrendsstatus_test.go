package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The training-status half of the trend tests: the aggregated status document, the
// max-metrics series, the profile fallback, the epoch reload and the redaction proof.
// The fixtures live in trainingtrends_test.go and are synthetic there too.

func TestMaxMetricsReadsTheWholeWindowInOneRequest(t *testing.T) {
	t.Parallel()

	body := `[{"calendarDate":"2026-01-29","generic":{"calendarDate":"2026-01-29",` +
		`"vo2MaxValue":51.0}},{"generic":{"calendarDate":"2026-01-31","vo2MaxValue":52.0},` +
		`"cycling":{"calendarDate":"2026-01-31","vo2MaxValue":44.0}}]`
	path := client.PathMaxMetricsPrefix + "/" + trendStartDate + "/" + trendEndDate
	h := newHarness(t, testkit.NewScript().With(path,
		testkit.JSON(http.StatusOK, body)), client.Limits{})

	metrics, err := newTrainingTrends(t, h).MaxMetrics(t.Context(), h.session,
		mustRange(t, trendStartDate, trendEndDate))
	if err != nil {
		t.Fatalf("MaxMetrics() = %v", err)
	}

	days := metrics.Days()
	if len(days) != 2 {
		t.Fatalf("Days() = %d, want 2", len(days))
	}
	if date, ok := days[1].Day(); !ok || date != trendEndDate {
		t.Errorf("Day() = %q (ok %v), want the section's own calendar date", date, ok)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want one for the whole window", got)
	}
}

func TestMaxMetricsRefusesAWindowPastTheConfiguredBound(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 1})
	_, err := newTrainingTrends(t, h).MaxMetrics(t.Context(), h.session,
		mustRange(t, trendStartDate, trendEndDate))
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("MaxMetrics() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestProfileVO2MaxReadsTheSettingsDocument(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript().With(client.PathUserSettings,
		testkit.JSON(http.StatusOK, `{"userData":{"vo2MaxRunning":51.0,"vo2MaxCycling":44.0}}`)),
		client.Limits{})

	profile, err := newTrainingTrends(t, h).ProfileVO2Max(t.Context(), h.session)
	if err != nil {
		t.Fatalf("ProfileVO2Max() = %v", err)
	}
	running, ok := profile.Running()
	if !ok {
		t.Fatal("Running() reported no estimate")
	}
	if got, _ := running.Float64(); got != 51 {
		t.Errorf("Running() = %v, want 51", got)
	}
	if _, ok := profile.Cycling(); !ok {
		t.Error("Cycling() reported no estimate")
	}
}

func TestProfileVO2MaxReportsAnAbsentSection(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript().With(client.PathUserSettings,
		testkit.JSON(http.StatusOK, `{"userData":null}`)), client.Limits{})

	profile, err := newTrainingTrends(t, h).ProfileVO2Max(t.Context(), h.session)
	if err != nil {
		t.Fatalf("ProfileVO2Max() = %v", err)
	}
	if _, ok := profile.Running(); ok {
		t.Error("Running() reported an estimate for a null userData")
	}
	if _, ok := profile.Cycling(); ok {
		t.Error("Cycling() reported an estimate for a null userData")
	}
}
