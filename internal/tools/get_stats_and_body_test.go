package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func TestGetStatsAndBodyReturnsBothHalvesSeparately(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(dailySummaryPath(), testkit.JSON(http.StatusOK, dailyStatsBody)).
		With(client.PathBodyComposition, testkit.JSON(http.StatusOK, dailyBodyBody)))

	got := h.call(t, ToolGetStatsAndBody, map[string]any{argDate: dailyDate})
	if got["date"] != dailyDate {
		t.Errorf("date = %v, want the day that was asked for", got["date"])
	}

	stats, ok := got["stats"].(map[string]any)
	if !ok {
		t.Fatalf("stats = %T, want the curated object", got["stats"])
	}
	if _, present := stats["total_steps"]; !present {
		t.Error("the stats half lost its curated keys")
	}
	average, ok := got["body_composition_average"].(map[string]any)
	if !ok {
		t.Fatalf("body_composition_average = %T, want the averaged composition",
			got["body_composition_average"])
	}
	// The account records no weight, so the object arrives with its window intact
	// and every metric omitted. Reporting the metrics as zeroes would invent
	// readings Garmin never measured.
	if average["from_epoch_ms"] != float64(1769817600000) {
		t.Errorf("from_epoch_ms = %v, want the epoch milliseconds", average["from_epoch_ms"])
	}
	for _, metric := range []string{keyWeight, "bmi", "body_fat", "trend"} {
		if _, present := average[metric]; present {
			t.Errorf("%s is present for an account that records no weight", metric)
		}
	}
}

// TestGetStatsAndBodyOmitsTheBodyHalfOnlyWhenGarminSendsNoObject draws the line the
// sample settled: a null totalAverage is the one case that omits the half. An account
// that merely records no weight still gets the object, and that case is covered by
// TestGetStatsAndBodyReturnsBothHalvesSeparately.
func TestGetStatsAndBodyOmitsTheBodyHalfOnlyWhenGarminSendsNoObject(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(dailySummaryPath(), testkit.JSON(http.StatusOK, dailyStatsBody)).
		With(client.PathBodyComposition, testkit.JSON(http.StatusOK, `{"totalAverage":null}`)))

	got := h.call(t, ToolGetStatsAndBody, map[string]any{argDate: dailyDate})
	if _, present := got["body_composition_average"]; present {
		t.Error("body_composition_average is present for a response that carried no object")
	}
}

func TestGetStatsAndBodyReportsAWithheldDay(t *testing.T) {
	t.Parallel()

	// Garmin answers a session it does not trust with privacyProtected. That is an
	// authentication failure, not an empty day.
	h := newToolHarness(t, dailyScript().
		With(dailySummaryPath(), testkit.JSON(http.StatusOK, `{"privacyProtected":true}`)))

	advice := h.callError(t, ToolGetStatsAndBody, map[string]any{argDate: dailyDate})
	if !strings.Contains(advice, "Re-authenticate") {
		t.Errorf("advice = %q, want the re-authentication remediation", advice)
	}
	if strings.Contains(advice, "privacyProtected") {
		t.Errorf("advice = %q, want no fragment of the payload", advice)
	}
}

func TestStatsAndBodyLogValueReportsShapeAndNoReading(t *testing.T) {
	t.Parallel()

	steps := 9123
	combined := StatsAndBody{
		Date:                   dailyDate,
		Stats:                  DailyActivityStats{TotalSteps: &steps},
		BodyCompositionAverage: &BodyCompositionAverage{Weight: 72000.0},
	}

	rendered := combined.LogValue().String()
	for _, reading := range []string{"9123", "72000"} {
		if strings.Contains(rendered, reading) {
			t.Errorf("LogValue rendered the reading %s: %s", reading, rendered)
		}
	}
	if !strings.Contains(rendered, "bodyAverage=set") {
		t.Errorf("LogValue = %s, want the presence of the body half", rendered)
	}
}

// TestGetStatsAndBodyDropsIdentifyingFieldsFromTheBodyHalf is the fifth instance of
// the passthrough class: this tool composes the same ten untyped metrics, so it needs
// the same sanitiser and the same count.
func TestGetStatsAndBodyDropsIdentifyingFieldsFromTheBodyHalf(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(dailySummaryPath(), testkit.JSON(http.StatusOK, dailyStatsBody)).
		With(client.PathBodyComposition,
			testkit.JSON(http.StatusOK, dailyBodyIdentifiedBody)))

	got := h.call(t, ToolGetStatsAndBody, map[string]any{argDate: dailyDate})

	average, ok := got["body_composition_average"].(map[string]any)
	if !ok {
		t.Fatalf("body_composition_average = %T, want the averaged composition",
			got["body_composition_average"])
	}
	weight, ok := average[keyWeight].(map[string]any)
	if !ok {
		t.Fatalf("weight = %T, want the object Garmin sent", average[keyWeight])
	}
	if _, present := weight[keyUserProfilePK]; present {
		t.Error("the metric carries an account identifier")
	}
	if got["dropped_fields"] != float64(1) {
		t.Errorf("dropped_fields = %v, want 1", got["dropped_fields"])
	}
}
