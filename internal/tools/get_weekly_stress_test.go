package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// weeklyStressDocument is synthetic: two measured weeks and one week Garmin holds
// nothing for, which must survive as an entry with absent fields.
const weeklyStressDocument = `[{"calendarDate":"2026-01-05","value":31},` +
	`{"calendarDate":"2026-01-12","value":"37"},{"calendarDate":null,"value":null}]`

func weeklyStressPath(weeks int) string {
	return client.PathWeeklyStressStatsPrefix + "/" + stressDate + "/" + strconv.Itoa(weeks)
}

func weeklyStressArgs(weeks any) map[string]any {
	args := map[string]any{argEndDate: stressDate}
	if weeks != nil {
		args["weeks"] = weeks
	}
	return args
}

// TestGetWeeklyStressAppliesTheManifestDefault proves an omitted week count reads four
// weeks, which is the default the manifest declares.
func TestGetWeeklyStressAppliesTheManifestDefault(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(weeklyStressPath(defaultStressWeeks),
		testkit.JSON(http.StatusOK, weeklyStressDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetWeeklyStress, weeklyStressArgs(nil))

	if got := number(t, result, "weeks_requested"); got != defaultStressWeeks {
		t.Errorf("weeks_requested = %v, want the default %d", got, defaultStressWeeks)
	}
	if got := number(t, result, "weeks_returned"); got != 3 {
		t.Errorf("weeks_returned = %v, want 3", got)
	}
	if got, _ := result[argEndDate].(string); got != stressDate {
		t.Errorf("end_date = %q, want %q", got, stressDate)
	}

	requests := h.fake.Requests()
	if len(requests) != 1 || requests[0].Path != weeklyStressPath(defaultStressWeeks) {
		t.Fatalf("requested %+v, want %q", requests, weeklyStressPath(defaultStressWeeks))
	}
}

// TestGetWeeklyStressSortsNewestFirstAndKeepsEmptyWeeks pins upstream's ordering and
// proves a week with no data is still an answer.
func TestGetWeeklyStressSortsNewestFirstAndKeepsEmptyWeeks(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(weeklyStressPath(3),
		testkit.JSON(http.StatusOK, weeklyStressDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetWeeklyStress, weeklyStressArgs(3))

	weeks := list(t, result, "weekly_data")
	if len(weeks) != 3 {
		t.Fatalf("%d weeks returned, want 3", len(weeks))
	}
	newest := entry(t, weeks, 0)
	if got, _ := newest["week_start"].(string); got != "2026-01-12" {
		t.Errorf("weekly_data[0].week_start = %q, want the newest week", got)
	}
	if got := number(t, newest, "stress_value"); got != 37 {
		t.Errorf("weekly_data[0].stress_value = %v, want 37 from the string form", got)
	}
	last := entry(t, weeks, 2)
	if _, present := last["week_start"]; present {
		t.Error("the week with no start date carries one, want it omitted")
	}
	if _, present := last["stress_value"]; present {
		t.Error("the week with no value carries one, want it omitted")
	}
}

// TestGetWeeklyStressBoundsTheWeekCount proves the range is enforced before dispatch,
// so no out-of-range week count ever reaches Garmin.
func TestGetWeeklyStressBoundsTheWeekCount(t *testing.T) {
	t.Parallel()

	for name, weeks := range map[string]int{
		"zero":       0,
		caseNegative: -1,
		"over cap":   maxStressWeeks + 1,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newToolHarness(t, testkit.NewScript())
			advice := h.callError(t, ToolGetWeeklyStress, weeklyStressArgs(weeks))
			assertNoRawPayload(t, advice)
			if got := len(h.fake.Requests()); got != 0 {
				t.Errorf("the fake received %d requests, want none", got)
			}
		})
	}
}

// TestGetWeeklyStressCutsASurplusAnswer proves a Garmin answer wider than the request
// is cut and reported, rather than silently handed on as if it had been asked for.
func TestGetWeeklyStressCutsASurplusAnswer(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(weeklyStressPath(1),
		testkit.JSON(http.StatusOK, weeklyStressDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetWeeklyStress, weeklyStressArgs(1))

	if got := number(t, result, "weeks_returned"); got != 1 {
		t.Errorf("weeks_returned = %v, want 1", got)
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true for a cut week list")
	}
}

// TestGetWeeklyStressRefusesAMalformedEndDate proves the window argument is validated
// with the same strictness as every other date on this surface.
func TestGetWeeklyStressRefusesAMalformedEndDate(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript())
	for _, value := range []string{"", malformedDate, "2026-02-30", stressTraversal} {
		advice := h.callError(t, ToolGetWeeklyStress, map[string]any{argEndDate: value})
		assertNoRawPayload(t, advice)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestGetWeeklyStressDeclaresItsBoundsInTheSchema proves the enforced bounds are the
// published ones, so a client builds a valid call from the schema alone.
func TestGetWeeklyStressDeclaresItsBoundsInTheSchema(t *testing.T) {
	t.Parallel()

	properties := getWeeklyStressContract().Schema.Properties()
	if len(properties) != 2 {
		t.Fatalf("declared %d properties, want end_date and weeks", len(properties))
	}
	for _, property := range properties {
		switch property.Name {
		case argEndDate:
			if !property.Required || property.Format != formatDate || property.Pattern == "" {
				t.Errorf("end_date is not strict enough: %+v", property)
			}
		case "weeks":
			assertWeekCountProperty(t, property)
		default:
			t.Errorf("unexpected property %q", property.Name)
		}
	}
}

// assertWeekCountProperty pins the published week-count bounds against the enforced
// ones.
func assertWeekCountProperty(t *testing.T, property Property) {
	t.Helper()

	switch {
	case property.Minimum == nil || *property.Minimum != 1:
		t.Errorf("weeks minimum = %v, want 1", property.Minimum)
	case property.Maximum == nil || *property.Maximum != maxStressWeeks:
		t.Errorf("weeks maximum = %v, want %d", property.Maximum, maxStressWeeks)
	case property.Default != defaultStressWeeks:
		t.Errorf("weeks default = %v, want %d", property.Default, defaultStressWeeks)
	case property.Required:
		t.Error("weeks is required, want it optional with a default")
	}
}

// TestGetWeeklyStressAdviceNamesTheOffendingArgument proves a refusal is actionable
// without quoting what the caller sent.
func TestGetWeeklyStressAdviceNamesTheOffendingArgument(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript())
	advice := h.callError(t, ToolGetWeeklyStress, weeklyStressArgs(0))
	if !strings.Contains(advice, "weeks") {
		t.Errorf("the refusal %q does not name the offending argument", advice)
	}
}
