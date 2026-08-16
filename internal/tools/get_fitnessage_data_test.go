package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// fitnessAgePath is the dated path of the fitness-age read.
// badCalendarDate is a date that parses as a shape and is not a real day.
const badCalendarDate = "2026-02-30"

// componentBMI is the one component name the fixtures carry.
const componentBMI = "bmi"

const fitnessAgePath = client.PathFitnessAgePrefix + "/" + scoresEndDate

// fitnessAgeDocument is one day with one component. Every value is invented.
const fitnessAgeDocument = `{"chronologicalAge":41,"fitnessAge":36.44,` +
	`"achievableFitnessAge":34.24,"previousFitnessAge":37.1,` +
	`"lastUpdated":"` + scoresEndDate + `T06:12:00.0","components":{` +
	`"bmi":{"value":23.4,"targetValue":22.0,"improvementValue":1.4,"potentialAge":35.44,` +
	`"priority":1,"stale":false,"lastMeasurementDate":"2026-01-30"}}}`

func fitnessAgeArgs() map[string]any {
	return map[string]any{argDate: scoresEndDate}
}

func TestGetFitnessAgeDataReturnsTheAgesAndTheirDifference(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(fitnessAgePath,
		testkit.JSON(http.StatusOK, fitnessAgeDocument))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetFitnessAgeData, fitnessAgeArgs())

	if reported, _ := result["reported"].(bool); !reported {
		t.Error("reported = false, want true for a day Garmin holds an age for")
	}
	// Upstream rounds every age to one decimal.
	if got := number(t, result, "fitness_age_years"); got != 36.4 {
		t.Errorf("fitness_age_years = %v, want 36.4", got)
	}
	if got := number(t, result, "achievable_fitness_age_years"); got != 34.2 {
		t.Errorf("achievable_fitness_age_years = %v, want 34.2", got)
	}
	if got := number(t, result, "chronological_age_years"); got != 41 {
		t.Errorf("chronological_age_years = %v, want 41", got)
	}
	if got := number(t, result, "age_difference_years"); got != 4.6 {
		t.Errorf("age_difference_years = %v, want 4.6", got)
	}
	if _, present := result["components"]; present {
		t.Error("the breakdown was returned without being asked for")
	}
}

func TestGetFitnessAgeDataReturnsTheBreakdownOnRequest(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(fitnessAgePath,
		testkit.JSON(http.StatusOK, fitnessAgeDocument))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetFitnessAgeData,
		map[string]any{argDate: scoresEndDate, argNameDetails: true})

	component := object(t, object(t, result, "components"), componentBMI)
	if got := number(t, component, "value"); got != 23.4 {
		t.Errorf("value = %v, want 23.4", got)
	}
	if got := number(t, component, "target"); got != 22 {
		t.Errorf("target = %v, want 22", got)
	}
	if got := number(t, component, "potential_age_if_improved"); got != 35.4 {
		t.Errorf("potential_age_if_improved = %v, want 35.4", got)
	}
	if got, _ := component["last_measurement"].(string); got != "2026-01-30" {
		t.Errorf("last_measurement = %q, want 2026-01-30", got)
	}
}

// TestGetFitnessAgeDataCutsAnImplausibleBreakdown proves the component bound is applied
// and stated.
func TestGetFitnessAgeDataCutsAnImplausibleBreakdown(t *testing.T) {
	t.Parallel()

	components := make([]string, 0, maxFitnessAgeComponents+2)
	for i := range maxFitnessAgeComponents + 2 {
		components = append(components, `"component`+strconv.Itoa(i)+`":{"value":1}`)
	}
	body := `{"fitnessAge":36,"components":{` + strings.Join(components, ",") + `}}`
	script := testkit.NewScript().With(fitnessAgePath, testkit.JSON(http.StatusOK, body))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetFitnessAgeData,
		map[string]any{argDate: scoresEndDate, argNameDetails: true})

	if got := len(object(t, result, "components")); got != maxFitnessAgeComponents {
		t.Errorf("components holds %d entries, want the bound %d", got, maxFitnessAgeComponents)
	}
	if truncated, _ := result["components_truncated"].(bool); !truncated {
		t.Error("components_truncated = false, want true for a cut breakdown")
	}
}

// TestGetFitnessAgeDataReportsADayWithoutAnAge proves a new account is a normal answer.
func TestGetFitnessAgeDataReportsADayWithoutAnAge(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(fitnessAgePath, testkit.JSON(http.StatusOK, `{}`))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetFitnessAgeData, fitnessAgeArgs())
	if reported, _ := result["reported"].(bool); reported {
		t.Error("reported = true, want false for a day with no fitness age")
	}
	if _, present := result["age_difference_years"]; present {
		t.Error("a difference was computed without both ages")
	}
}

func TestGetFitnessAgeDataRefusesABadDateBeforeDispatch(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, testkit.NewScript())

	advice := h.callError(t, ToolGetFitnessAgeData, map[string]any{argDate: badCalendarDate})
	assertNoRawPayload(t, advice)
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestFitnessAgeDataLogValueReportsShapeOnly proves the log record carries no reading.
func TestFitnessAgeDataLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	age := 36.4
	value := FitnessAgeData{
		Date:            scoresEndDate,
		Reported:        true,
		FitnessAgeYears: &age,
		Components:      map[string]FitnessAgeComponent{componentBMI: {Value: &age}},
	}.LogValue().String()

	if strings.Contains(value, "36.4") || strings.Contains(value, componentBMI) {
		t.Errorf("the log value %q carries a reading", value)
	}
	if !strings.Contains(value, "fitnessAgeData") {
		t.Errorf("the log value %q does not name the model", value)
	}
}
