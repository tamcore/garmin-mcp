package tools

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// recoveryNow is noon of stressDate, the harness's "today".
func recoveryNow() time.Time { return time.Date(2026, 1, 31, 12, 0, 0, 0, time.Local) }

func recoveryHarness(t *testing.T, script testkit.Script) toolHarness {
	t.Helper()
	return newToolHarnessAt(t, script, recoveryNow)
}

func readinessScript(behavior testkit.Behavior) testkit.Script {
	return testkit.NewScript().With(readinessToolPath(), behavior)
}

func TestGetRecoveryTimeRemainingReadsTheNewestReadinessSnapshot(t *testing.T) {
	t.Parallel()

	h := recoveryHarness(t, readinessScript(testkit.JSON(http.StatusOK,
		`[{"timestampLocal":"2026-01-31T07:00:00.0","recoveryTime":1500},`+
			`{"timestampLocal":"2026-01-31T09:00:00.0","recoveryTime":400,"score":55,"level":"MODERATE"}]`)))

	got := h.call(t, ToolGetRecoveryTimeRemaining, map[string]any{argDate: stressDate})
	want := map[string]any{
		"source": recoverySourceReadiness, "remaining_hours": 6.7, "state": recoveryStateRecovering,
		"readiness_score": float64(55), "readiness_level": "MODERATE", argDate: stressDate,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %v, want %v", key, got[key], value)
		}
	}
}

func TestGetRecoveryTimeRemainingFallsBackToTheMorningSnapshot(t *testing.T) {
	t.Parallel()

	h := recoveryHarness(t, readinessScript(testkit.JSON(http.StatusOK,
		`[{"timestampLocal":"2026-01-31T09:00:00.0"},`+
			`{"timestampLocal":"2026-01-31T06:00:00.0","inputContext":"AFTER_WAKEUP_RESET",`+
			`"recoveryTime":720,"recoveryTimeChangePhrase":"REACHED_ZERO"}]`)))

	got := h.call(t, ToolGetRecoveryTimeRemaining, map[string]any{argDate: stressDate})
	if got["source"] != recoverySourceMorning || got["remaining_hours"] != float64(0) ||
		got["state"] != recoveryStateRecovered {
		t.Errorf("result = %v, want a drained clock from the morning snapshot", got)
	}
}

func TestGetRecoveryTimeRemainingDecaysTheMostRecentActivityToday(t *testing.T) {
	t.Parallel()

	threeHoursAgo := strconv.FormatInt(recoveryNow().Add(-3*time.Hour).UnixMilli(), 10)
	dayAgo := strconv.FormatInt(recoveryNow().Add(-24*time.Hour).UnixMilli(), 10)
	h := recoveryHarness(t, readinessScript(testkit.JSON(http.StatusOK, `[]`)).
		With(client.PathActivitySearch, testkit.JSON(http.StatusOK,
			`[{"activityId":11,"beginTimestamp":`+dayAgo+`,"duration":3600,"recoveryTime":4000},`+
				`{"activityId":12,"beginTimestamp":`+threeHoursAgo+`,"duration":3600,"recoveryTime":600},`+
				`{"activityId":13,"beginTimestamp":`+threeHoursAgo+`,"duration":7200}]`)))

	got := h.call(t, ToolGetRecoveryTimeRemaining, nil)
	// 600 minutes assigned at the end of a run that ended two hours ago leave eight.
	want := map[string]any{
		"source": recoverySourceActivity, "activity_id": float64(12),
		"remaining_hours": float64(8), "state": recoveryStateRecovering, argDate: stressDate,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %v, want %v", key, got[key], value)
		}
	}
}

func TestGetRecoveryTimeRemainingDoesNotDecayActivitiesForAPastDay(t *testing.T) {
	t.Parallel()

	h := recoveryHarness(t, testkit.NewScript().With(
		client.PathTrainingReadinessPrefix+"/2026-01-20", testkit.JSON(http.StatusNotFound, `{}`)))

	got := h.call(t, ToolGetRecoveryTimeRemaining, map[string]any{argDate: "2026-01-20"})
	if got["state"] != recoveryStateUnavailable || got["note"] == nil || got["source"] != nil {
		t.Errorf("result = %v, want unavailable with a note", got)
	}
	for _, request := range h.fake.Requests() {
		if request.Path == client.PathActivitySearch {
			t.Error("a past day read the activity list")
		}
	}
}

func TestGetRecoveryTimeRemainingPropagatesAFailedRead(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		h := recoveryHarness(t, readinessScript(testkit.JSON(status, `{}`)))
		h.callError(t, ToolGetRecoveryTimeRemaining, map[string]any{argDate: stressDate})
	}

	listFails := recoveryHarness(t, readinessScript(testkit.JSON(http.StatusOK, `[]`)).
		With(client.PathActivitySearch, testkit.JSON(http.StatusForbidden, `{}`)))
	listFails.callError(t, ToolGetRecoveryTimeRemaining, nil)
}

func TestRecoveryStateBands(t *testing.T) {
	t.Parallel()

	for hours, want := range map[float64]string{
		0: recoveryStateRecovered, 0.1: recoveryStateNearlyRecovered, 6: recoveryStateNearlyRecovered,
		6.1: recoveryStateRecovering, 24: recoveryStateRecovering, 24.1: recoveryStateNotRecovered,
	} {
		if got := recoveryState(hours); got != want {
			t.Errorf("recoveryState(%v) = %q, want %q", hours, got, want)
		}
	}
}
