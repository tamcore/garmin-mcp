package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// stressRegistrations names the stress, body-battery and readiness slice. The tools
// are registered for these tests by the shared harness, which builds the real
// registrar from register.go; this list stays because the slice's contract tests
// assert over a named group rather than over the whole registered surface.
func stressRegistrations() []registration {
	return []registration{
		{getStressDataContract, registerGetStressData},
		{getStressSummaryContract, registerGetStressSummary},
		{getAllDayStressContract, registerGetAllDayStress},
		{getWeeklyStressContract, registerGetWeeklyStress},
		{getBodyBatteryContract, registerGetBodyBattery},
		{getBodyBatteryEventsContract, registerGetBodyBatteryEvents},
		{getTrainingReadinessContract, registerGetTrainingReadiness},
		{getMorningTrainingReadinessContract, registerGetMorningTrainingReadiness},
		{getAllDayEventsContract, registerGetAllDayEvents},
	}
}

// Synthetic identities and fixtures. Nothing here is a recording of a real account.
const (
	stressDate = "2026-01-31"

	// Case names and refused values the stress tests reuse, named once so a rename
	// shows up in one place.
	keyToken        = "token"
	stressTraversal = "../../etc/passwd"
	caseEmptyArray  = "empty array"
	caseNegative    = "negative"
	caseEmptyObject = "empty object"

	// stressDocument mixes the shapes Garmin sends: a plain pair, a numeric string,
	// the negative marker for a gap, a null element and a malformed one.
	stressDocument = `{"calendarDate":"` + stressDate + `","maxStressLevel":81,` +
		`"avgStressLevel":"34","stressValuesArray":[[1738296000000,12],[1738296180000,"44"],` +
		`[1738296360000,64],[1738296540000,88],[1738296720000,-1],null,{"x":1}]}`
)

func stressToolPath() string { return client.PathDailyStressPrefix + "/" + stressDate }

func stressArgs() map[string]any { return map[string]any{argDate: stressDate} }

func TestGetStressDataReturnsTheDaysFiguresAndSeries(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressToolPath(),
		testkit.JSON(http.StatusOK, stressDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetStressData, stressArgs())

	if got, _ := result[argDate].(string); got != stressDate {
		t.Errorf("date = %q, want %q", got, stressDate)
	}
	if has, _ := result["has_data"].(bool); !has {
		t.Error("has_data = false, want true for a measured day")
	}
	if got := number(t, result, "max_stress_level"); got != 81 {
		t.Errorf("max_stress_level = %v, want 81", got)
	}
	if got := number(t, result, "average_stress_level"); got != 34 {
		t.Errorf("average_stress_level = %v, want 34 from the string form", got)
	}
	if got := number(t, result, "sample_count"); got != 7 {
		t.Errorf("sample_count = %v, want every element of the series", got)
	}
	if truncated, _ := result["truncated"].(bool); truncated {
		t.Error("truncated = true, want false for a day inside the bound")
	}
	sample := entry(t, list(t, result, "samples"), 1)
	if got := number(t, sample, "level"); got != 44 {
		t.Errorf("samples[1].level = %v, want 44 from the string form", got)
	}
}

// TestGetStressDataReportsAnUnwornDayAsEmpty proves absence is a first-class outcome.
func TestGetStressDataReportsAnUnwornDayAsEmpty(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{jsonNull: jsonNull, caseEmptyObject: `{}`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(stressToolPath(),
				testkit.JSON(http.StatusOK, body))
			h := newToolHarness(t, script)

			result := h.call(t, ToolGetStressData, stressArgs())
			if has, _ := result["has_data"].(bool); has {
				t.Error("has_data = true, want false for a day with no data")
			}
			if got := number(t, result, "sample_count"); got != 0 {
				t.Errorf("sample_count = %v, want 0", got)
			}
		})
	}
}

// TestGetStressDataBoundsTheSeries proves a long day is cut and says so, rather than
// filling the caller's context.
func TestGetStressDataBoundsTheSeries(t *testing.T) {
	t.Parallel()

	pairs := make([]string, 0, maxStressSamples+10)
	for index := range maxStressSamples + 10 {
		pairs = append(pairs, "[1738296000000,"+strconv.Itoa(index%99+1)+"]")
	}
	body := `{"calendarDate":"` + stressDate + `","stressValuesArray":[` +
		strings.Join(pairs, ",") + `]}`
	script := testkit.NewScript().With(stressToolPath(), testkit.JSON(http.StatusOK, body))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetStressData, stressArgs())

	if got := number(t, result, "sample_count"); got != maxStressSamples {
		t.Errorf("sample_count = %v, want the bound %d", got, maxStressSamples)
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true for a cut series")
	}
}

// TestGetStressDataReadsOnlyTheStressEndpoint proves the read needs no profile call:
// the stress paths carry no display name, so nothing but the principal identifies the
// account.
func TestGetStressDataReadsOnlyTheStressEndpoint(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressToolPath(),
		testkit.JSON(http.StatusOK, stressDocument))
	h := newToolHarness(t, script)

	h.call(t, ToolGetStressData, stressArgs())

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want exactly one", len(requests))
	}
	if requests[0].Path != stressToolPath() {
		t.Errorf("path = %q, want %q", requests[0].Path, stressToolPath())
	}
}

// TestGetStressDataSanitizesAGarminFailure proves no raw body reaches the caller.
func TestGetStressDataSanitizesAGarminFailure(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(stressToolPath(), testkit.JSON(
		http.StatusInternalServerError, `{"error":"synthetic","token":"super-secret-di-token"}`))
	h := newToolHarnessWith(t, script, client.Limits{MaxAttempts: 1})

	advice := h.callError(t, ToolGetStressData, stressArgs())
	assertNoRawPayload(t, advice)
	if strings.Contains(advice, "super-secret-di-token") {
		t.Error("the refusal carries the scripted token")
	}
}
