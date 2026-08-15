package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestGetStressSummaryComputesUpstreamsDistribution pins the four shares against the
// thresholds the pinned upstream applies: rest under 26, low 26 to 50, medium 51 to 75,
// high 76 and above, over the strictly positive readings only.
func TestGetStressSummaryComputesUpstreamsDistribution(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressToolPath(),
		testkit.JSON(http.StatusOK, stressDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetStressSummary, stressArgs())

	if got, _ := result[argDate].(string); got != stressDate {
		t.Errorf("date = %q, want %q", got, stressDate)
	}
	if got := number(t, result, "max_stress_level"); got != 81 {
		t.Errorf("max_stress_level = %v, want 81", got)
	}
	// The fixture holds four readings — 12, 44, 64 and 88 — one per band, plus a
	// gap, a null and a malformed element that are not readings.
	if got := number(t, result, "data_points_count"); got != 4 {
		t.Fatalf("data_points_count = %v, want 4", got)
	}
	for _, key := range []string{
		"rest_percent", "low_stress_percent", "medium_stress_percent", "high_stress_percent",
	} {
		if got := number(t, result, key); got != 25 {
			t.Errorf("%s = %v, want 25", key, got)
		}
	}
}

// TestGetStressSummaryReturnsNoDistributionWithoutReadings proves a day of gaps is an
// answer rather than a division by zero: the shares are absent, not zeroed.
func TestGetStressSummaryReturnsNoDistributionWithoutReadings(t *testing.T) {
	t.Parallel()

	body := `{"calendarDate":"` + stressDate + `",` +
		`"stressValuesArray":[[1738296000000,-1],[1738296180000,-2]]}`
	script := testkit.NewScript().With(stressToolPath(), testkit.JSON(http.StatusOK, body))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetStressSummary, stressArgs())

	if got := number(t, result, "data_points_count"); got != 0 {
		t.Errorf("data_points_count = %v, want 0", got)
	}
	for _, key := range []string{
		"rest_percent", "low_stress_percent", "medium_stress_percent", "high_stress_percent",
	} {
		if _, present := result[key]; present {
			t.Errorf("%s is present, want it omitted when there is nothing to divide", key)
		}
	}
	if has, _ := result["has_data"].(bool); !has {
		t.Error("has_data = false, want true: Garmin held a document for the day")
	}
}

// TestGetStressSummaryReadsTheSameURLAsTheOtherTwoViews proves the three tools are
// three views of one read rather than three copies of the same request.
func TestGetStressSummaryReadsTheSameURLAsTheOtherTwoViews(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressToolPath(),
		testkit.JSON(http.StatusOK, stressDocument))
	h := newToolHarness(t, script)

	for _, tool := range []string{ToolGetStressData, ToolGetStressSummary, ToolGetAllDayStress} {
		h.call(t, tool, stressArgs())
	}

	requests := h.fake.Requests()
	if len(requests) != 3 {
		t.Fatalf("the fake received %d requests, want one per call", len(requests))
	}
	for _, request := range requests {
		if request.Path != stressToolPath() {
			t.Errorf("path = %q, want the one daily-stress URL %q",
				request.Path, stressToolPath())
		}
		if len(request.Query) != 0 {
			t.Errorf("query = %v, want none: the day is a path segment", request.Query)
		}
	}
}

// TestGetStressSummaryDoesNotReturnTheSeries proves the compact view stays compact:
// the point of the tool is that a caller can ask for the day without the timeline.
func TestGetStressSummaryDoesNotReturnTheSeries(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressToolPath(),
		testkit.JSON(http.StatusOK, stressDocument))
	h := newToolHarness(t, script)

	rendered := h.text(t, ToolGetStressSummary, stressArgs())
	for _, forbidden := range []string{"1738296000000", "samples"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the summary carries %q, which belongs to get_stress_data", forbidden)
		}
	}
}

// TestStressSummaryLogValueOmitsTheValueConditionedCount is the coverage-leak
// regression. DataPointsCount is not the length of what was retained: it counts the
// readings that passed a value test, so logging it discloses how much of the day the
// account was actually measured. The presence of the distribution already carries the
// only bit an operator needs.
func TestStressSummaryLogValueOmitsTheValueConditionedCount(t *testing.T) {
	t.Parallel()

	rendered := StressSummary{HasData: true, DataPointsCount: 137}.LogValue().String()

	if strings.Contains(rendered, "137") || strings.Contains(rendered, "dataPoints") {
		t.Errorf("LogValue = %s, want no count of readings that passed a value test", rendered)
	}
	if !strings.Contains(rendered, "hasData=true") {
		t.Errorf("LogValue = %s, want the shape of the answer", rendered)
	}
}
