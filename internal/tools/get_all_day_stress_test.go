package tools

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestGetAllDayStressReportsTheDaysCoverage pins the day-level view: the figures, and
// how much of the day is a reading rather than a gap.
func TestGetAllDayStressReportsTheDaysCoverage(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressToolPath(),
		testkit.JSON(http.StatusOK, stressDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetAllDayStress, stressArgs())

	if got, _ := result[argDate].(string); got != stressDate {
		t.Errorf("date = %q, want %q", got, stressDate)
	}
	if got := number(t, result, "max_stress_level"); got != 81 {
		t.Errorf("max_stress_level = %v, want 81", got)
	}
	if got := number(t, result, "average_stress_level"); got != 34 {
		t.Errorf("average_stress_level = %v, want 34 from the string form", got)
	}
	if got := number(t, result, "sample_count"); got != 7 {
		t.Errorf("sample_count = %v, want every element Garmin recorded", got)
	}
	if got := number(t, result, "usable_sample_count"); got != 4 {
		t.Errorf("usable_sample_count = %v, want the four readings", got)
	}
}

// TestGetAllDayStressReportsAnEmptyResponseAsEmpty proves a 204 is a quiet day and not
// a failure.
func TestGetAllDayStressReportsAnEmptyResponseAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressToolPath(),
		testkit.Behavior{Status: http.StatusNoContent})
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetAllDayStress, stressArgs())

	if has, _ := result["has_data"].(bool); has {
		t.Error("has_data = true, want false for an empty response")
	}
	if got := number(t, result, "sample_count"); got != 0 {
		t.Errorf("sample_count = %v, want 0", got)
	}
	if _, present := result["max_stress_level"]; present {
		t.Error("max_stress_level is present, want it omitted for a day with no data")
	}
}

// TestGetAllDayStressReturnsNoSeries proves the day-level view carries no timeline, so
// a caller that wants one has to ask get_stress_data for it.
func TestGetAllDayStressReturnsNoSeries(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressToolPath(),
		testkit.JSON(http.StatusOK, stressDocument))
	h := newToolHarness(t, script)

	rendered := h.text(t, ToolGetAllDayStress, stressArgs())
	for _, forbidden := range []string{"1738296000000", "stressValuesArray"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the all-day view carries %q, which it must not return", forbidden)
		}
	}
}

// TestGetAllDayStressSanitizesAMissingDay proves a day Garmin does not hold comes back
// as authored advice rather than as a raw 404 body.
func TestGetAllDayStressSanitizesAMissingDay(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressToolPath(), testkit.JSON(
		http.StatusNotFound, `{"error":"synthetic not found","trace":"/wellness/dailyStress"}`))
	h := newToolHarness(t, script)

	advice := h.callError(t, ToolGetAllDayStress, stressArgs())
	assertNoRawPayload(t, advice)
	if advice != AdviceNoSuchRecord {
		t.Errorf("advice = %q, want %q", advice, AdviceNoSuchRecord)
	}
}

// The four assertions below cover the whole stress, body-battery and readiness slice
// rather than this tool alone. They live beside the third view of the shared stress
// read because the nine tools share one registrar and one harness, and asserting the
// contract nine times over would say the same thing nine times.

// TestStressToolsDeclareTheReadOnlyContract covers all nine at once: the read-only
// tier, the manifest's health sensitivity, and all four annotation hints. A wrong hint
// is not cosmetic — a client decides whether to prompt its user from it.
func TestStressToolsDeclareTheReadOnlyContract(t *testing.T) {
	t.Parallel()

	want := mcpserver.Annotations{
		ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: true,
	}
	for _, entry := range stressRegistrations() {
		spec := entry.contract().Spec
		t.Run(spec.Name, func(t *testing.T) {
			t.Parallel()

			if spec.Tier != policy.TierReadOnly {
				t.Errorf("tier = %v, want the read-only tier", spec.Tier)
			}
			if spec.Category != categoryHealth {
				t.Errorf("category = %q, want %q", spec.Category, categoryHealth)
			}
			if spec.Annotations != want {
				t.Errorf("annotations = %+v, want %+v", spec.Annotations, want)
			}
			if spec.Title == "" || spec.Description == "" {
				t.Error("the tool declares no title or no description")
			}
		})
	}
}

// TestStressToolsAcceptNoAccountSelector is the principal rule over the whole slice:
// the account comes from the request context and from nowhere else.
func TestStressToolsAcceptNoAccountSelector(t *testing.T) {
	t.Parallel()

	forbidden := []string{keyUserID, keyEmail, "display_name", keyToken, "token_path", "path"}
	for _, entry := range stressRegistrations() {
		contract := entry.contract()
		document := contract.Schema.JSON()
		if additional, ok := document[keyAdditionalProperties].(bool); !ok || additional {
			t.Errorf("%s declares additionalProperties = %v, want false",
				contract.Spec.Name, document[keyAdditionalProperties])
		}
		for _, property := range contract.Schema.Properties() {
			for _, name := range forbidden {
				if property.Name == name {
					t.Errorf("%s declares the argument %q", contract.Spec.Name, name)
				}
			}
		}
	}
}

// TestStressToolsRefuseAMalformedDateBeforeAnyCall proves every date argument is
// validated before Garmin is reached.
func TestStressToolsRefuseAMalformedDateBeforeAnyCall(t *testing.T) {
	t.Parallel()

	dated := []string{
		ToolGetStressData, ToolGetStressSummary, ToolGetAllDayStress,
		ToolGetBodyBatteryEvents, ToolGetTrainingReadiness,
		ToolGetMorningTrainingReadiness, ToolGetAllDayEvents,
	}
	cases := map[string]map[string]any{
		"missing date":   nil,
		"empty date":     {argDate: ""},
		"wrong layout":   {argDate: malformedDate},
		"unreal date":    {argDate: "2026-02-30"},
		"path traversal": {argDate: stressTraversal},
	}
	for _, tool := range dated {
		for name, args := range cases {
			t.Run(tool+"/"+name, func(t *testing.T) {
				t.Parallel()

				h := newToolHarness(t, testkit.NewScript())
				advice := h.callError(t, tool, args)
				assertNoRawPayload(t, advice)
				if got := len(h.fake.Requests()); got != 0 {
					t.Errorf("the fake received %d requests, want none", got)
				}
			})
		}
	}
}

// TestStressResultsLogTheirShapeAndNotTheirContent is the health-redaction test. A
// reading — a stress level, a body-battery figure, a readiness score — must never
// reach a log sink, even if a result does.
func TestStressResultsLogTheirShapeAndNotTheirContent(t *testing.T) {
	t.Parallel()

	level, score, charged := 88, 83, 56
	label := "AFTER_WAKEUP_RESET"
	values := []slog.LogValuer{
		StressData{
			Date: stressDate, HasData: true, MaxStressLevel: &level,
			Samples: []StressSample{{Level: &level}},
		},
		StressSummary{Date: stressDate, HasData: true, MaxStressLevel: &level},
		AllDayStress{Date: stressDate, HasData: true, MaxStressLevel: &level},
		WeeklyStressWindow{EndDate: stressDate, WeeksRequested: 4},
		BodyBatteryWindow{StartDate: stressDate, Days: []BodyBatteryDay{{Charged: &charged}}},
		BodyBatteryEventList{Date: stressDate, Count: 1},
		AllDayEventList{Date: stressDate, Count: 1},
		TrainingReadinessList{Date: stressDate, Entries: []ReadinessEntry{{Score: &score}}},
		MorningReadinessResult{
			Date: stressDate, HasData: true, FromWakeupReset: true,
			Readiness: &ReadinessEntry{Score: &score, Context: &label},
		},
	}
	for _, value := range values {
		rendered := fmt.Sprintf("%v", value.LogValue())
		for _, forbidden := range []string{"88", "83", "56", stressDate, label} {
			if strings.Contains(rendered, forbidden) {
				t.Errorf("%T logged %q, which its LogValue must withhold", value, forbidden)
			}
		}
		if !strings.Contains(rendered, "model=") {
			t.Errorf("%T logged %q, which names no model", value, rendered)
		}
	}
}

// TestAllDayStressLogValueOmitsTheValueConditionedCount separates the two counts.
// SampleCount is the retained length — every element Garmin sent, gaps included — and
// is shape. UsableSampleCount is derived by classifying each element's value, so it
// discloses how much of the day was measured and stays out of the log.
func TestAllDayStressLogValueOmitsTheValueConditionedCount(t *testing.T) {
	t.Parallel()

	rendered := AllDayStress{
		HasData: true, SampleCount: 288, UsableSampleCount: 137,
	}.LogValue().String()

	if strings.Contains(rendered, "137") || strings.Contains(rendered, "usableSamples") {
		t.Errorf("LogValue = %s, want no count of elements that passed a value test", rendered)
	}
	if !strings.Contains(rendered, "samples=288") {
		t.Errorf("LogValue = %s, want the unconditioned retained length", rendered)
	}
}
