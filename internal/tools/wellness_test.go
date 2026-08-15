package tools_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

func TestGetSleepDataReturnsTheDateKeyedSummary(t *testing.T) {
	h := newHarness(t, readScript())

	got := h.call(t, tools.ToolGetSleepData, map[string]any{argDate: testCalendarDate})

	if got["date"] != testCalendarDate {
		t.Errorf("date = %v, want %q", got["date"], testCalendarDate)
	}
	if got["sleep_time_seconds"] != float64(27000) {
		t.Errorf("sleep_time_seconds = %v, want 27000", got["sleep_time_seconds"])
	}
	if got["deep_sleep_seconds"] != float64(5400) {
		t.Errorf("deep_sleep_seconds = %v, want 5400", got["deep_sleep_seconds"])
	}
	if got["sleep_quality"] != "good" {
		t.Errorf("sleep_quality = %v, want %q", got["sleep_quality"], "good")
	}
}

func TestGetSleepDataResolvesTheDisplayNameFromTheProfile(t *testing.T) {
	h := newHarness(t, readScript())

	h.call(t, tools.ToolGetSleepData, map[string]any{argDate: testCalendarDate})

	paths := h.requests()
	if got := countPath(paths, client.PathSocialProfile); got != 1 {
		t.Errorf("profile reads = %d, want 1: the display name comes from the account, not an argument",
			got)
	}
	if got := countPath(paths, sleepPath()); got != 1 {
		t.Errorf("sleep reads = %d, want 1", got)
	}
}

func TestGetSleepDataSendsGarminsOwnNonSleepBuffer(t *testing.T) {
	h := newHarness(t, readScript())

	h.call(t, tools.ToolGetSleepData, map[string]any{argDate: testCalendarDate})

	for _, request := range h.fake.Requests() {
		if request.Path != sleepPath() {
			continue
		}
		if got := request.Query.Get(client.QueryDate); got != testCalendarDate {
			t.Errorf("date query = %q, want %q", got, testCalendarDate)
		}
		if got := request.Query.Get(client.QueryNonSleepBufferMinutes); got != "60" {
			t.Errorf("nonSleepBufferMinutes query = %q, want %q", got, "60")
		}
	}
}

func TestGetUserSummaryReturnsTheDailyTotals(t *testing.T) {
	h := newHarness(t, readScript())

	got := h.call(t, tools.ToolGetUserSummary, map[string]any{argDate: testCalendarDate})

	if got["date"] != testCalendarDate {
		t.Errorf("date = %v, want %q", got["date"], testCalendarDate)
	}
	if got["total_steps"] != float64(9123) {
		t.Errorf("total_steps = %v, want 9123", got["total_steps"])
	}
	if got["resting_heart_rate"] != float64(52) {
		t.Errorf("resting_heart_rate = %v, want 52", got["resting_heart_rate"])
	}
}

func TestGetUserSummaryReportsWithheldDataAsAnAuthenticationFailure(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathSocialProfile, testkit.JSON(http.StatusOK, profileBody)).
		With(summaryPath(), testkit.JSON(http.StatusOK, privacyProtectedSummaryBody))
	h := newHarness(t, script)

	text := h.callError(t, tools.ToolGetUserSummary, map[string]any{argDate: testCalendarDate})

	assertSanitized(t, text)
	if !containsFold(text, "authenticate") {
		t.Errorf("the refusal %q does not name the remediation", text)
	}
}

func TestWellnessToolsRefuseAMalformedOrMissingDate(t *testing.T) {
	h := newHarness(t, readScript())

	cases := map[string]map[string]any{
		"missing date":   nil,
		"empty date":     {argDate: ""},
		"wrong layout":   {argDate: "31-01-2026"},
		"unreal date":    {argDate: "2026-02-30"},
		"padded date":    {argDate: "2026-01-31   x"},
		"path traversal": {argDate: traversalAttempt},
	}
	for _, tool := range []string{tools.ToolGetSleepData, tools.ToolGetUserSummary} {
		for name, args := range cases {
			t.Run(tool+"/"+name, func(t *testing.T) {
				assertSanitized(t, h.callError(t, tool, args))
			})
		}
	}
}

func TestWellnessToolsDoNotReachGarminForAMalformedDate(t *testing.T) {
	h := newHarness(t, readScript())

	h.callError(t, tools.ToolGetSleepData, map[string]any{argDate: "31-01-2026"})

	if paths := h.requests(); len(paths) != 0 {
		t.Errorf("the fake received %v, want nothing: a bad date is refused before dispatch", paths)
	}
}
