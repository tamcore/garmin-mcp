package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// readinessDocument is synthetic: two snapshots, one manual and one after waking, the
// second with a partial field set. Every value is invented.
const readinessDocument = `[{"calendarDate":"` + stressDate + `",` +
	`"timestampLocal":"2026-01-31T06:40:00.0","inputContext":"MANUAL_RESET",` +
	`"level":"MODERATE","score":58,"feedbackShort":"OK","sleepScore":71,` +
	`"recoveryTime":180,"acwrFactorPercent":12,"hrvFactorPercent":"9",` +
	`"hrvWeeklyAverage":74,"acuteLoad":330},` +
	`{"calendarDate":"` + stressDate + `","inputContext":"AFTER_WAKEUP_RESET",` +
	`"level":"HIGH","score":83,"sleepScore":88}]`

// keyHRVWeeklyAverage is a curated readiness key both readiness tests assert on.
const keyHRVWeeklyAverage = "hrv_weekly_avg"

func readinessToolPath() string { return client.PathTrainingReadinessPrefix + "/" + stressDate }

func TestGetTrainingReadinessReturnsEverySnapshot(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(readinessToolPath(),
		testkit.JSON(http.StatusOK, readinessDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetTrainingReadiness, stressArgs())

	if got, _ := result[argDate].(string); got != stressDate {
		t.Errorf("date = %q, want %q", got, stressDate)
	}
	if got := number(t, result, "count"); got != 2 {
		t.Fatalf("count = %v, want 2", got)
	}
	assertReadinessEntry(t, entry(t, list(t, result, "entries"), 0))
}

// assertReadinessEntry pins the curated key set against the upstream tool's.
func assertReadinessEntry(t *testing.T, snapshot map[string]any) {
	t.Helper()

	cases := map[string]float64{
		"score":                        58,
		"sleep_score":                  71,
		"training_load_factor_percent": 12,
		"hrv_factor_percent":           9,
		keyHRVWeeklyAverage:            74,
		"acute_load":                   330,
		// Source: the upstream curation, which divides recoveryTime by 60 and
		// rounds to one decimal. 180 minutes is three hours.
		"recovery_time_hours": 3,
	}
	for key, want := range cases {
		if got := number(t, snapshot, key); got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	if got, _ := snapshot["context"].(string); got != "MANUAL_RESET" {
		t.Errorf("context = %q, want MANUAL_RESET", got)
	}
	if got, _ := snapshot["level"].(string); got != "MODERATE" {
		t.Errorf("level = %q, want MODERATE", got)
	}
}

// TestGetTrainingReadinessOmitsFieldsADeviceDoesNotRecord proves a partial snapshot is
// rendered as absent fields rather than as zeroes, which a model would read as data.
func TestGetTrainingReadinessOmitsFieldsADeviceDoesNotRecord(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(readinessToolPath(),
		testkit.JSON(http.StatusOK, readinessDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetTrainingReadiness, stressArgs())

	second := entry(t, list(t, result, "entries"), 1)
	for _, key := range []string{
		"recovery_time_hours", "acute_load", keyHRVWeeklyAverage, "timestamp",
	} {
		if _, present := second[key]; present {
			t.Errorf("%s is present, want it omitted for a snapshot without it", key)
		}
	}
}

// TestGetTrainingReadinessReportsADayWithNoSnapshotAsEmpty proves absence is an answer.
func TestGetTrainingReadinessReportsADayWithNoSnapshotAsEmpty(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{caseEmptyArray: `[]`, jsonNull: jsonNull} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(readinessToolPath(),
				testkit.JSON(http.StatusOK, body))
			h := newToolHarness(t, script)

			result := h.call(t, ToolGetTrainingReadiness, stressArgs())
			if got := number(t, result, "count"); got != 0 {
				t.Errorf("count = %v, want 0", got)
			}
			if got := len(list(t, result, "entries")); got != 0 {
				t.Errorf("entries holds %d snapshots, want none", got)
			}
		})
	}
}

// TestGetTrainingReadinessBoundsTheSnapshotList proves a drifted answer is cut and says
// so.
func TestGetTrainingReadinessBoundsTheSnapshotList(t *testing.T) {
	t.Parallel()

	entries := make([]string, 0, maxReadinessEntries+3)
	for range maxReadinessEntries + 3 {
		entries = append(entries, `{"score":50}`)
	}
	script := testkit.NewScript().With(readinessToolPath(),
		testkit.JSON(http.StatusOK, "["+strings.Join(entries, ",")+"]"))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetTrainingReadiness, stressArgs())

	if got := number(t, result, "count"); got != maxReadinessEntries {
		t.Errorf("count = %v, want the bound %d", got, maxReadinessEntries)
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true for a cut snapshot list")
	}
}

// TestGetTrainingReadinessReadsTheDateKeyedPath proves the day is a path segment and
// that no profile read is needed.
func TestGetTrainingReadinessReadsTheDateKeyedPath(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(readinessToolPath(),
		testkit.JSON(http.StatusOK, readinessDocument))
	h := newToolHarness(t, script)

	h.call(t, ToolGetTrainingReadiness, stressArgs())

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want exactly one", len(requests))
	}
	if requests[0].Path != readinessToolPath() {
		t.Errorf("path = %q, want %q", requests[0].Path, readinessToolPath())
	}
}

// TestGetTrainingReadinessSanitizesAGarminFailure proves no raw body reaches the
// caller.
func TestGetTrainingReadinessSanitizesAGarminFailure(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(readinessToolPath(),
		testkit.JSON(http.StatusBadGateway, `{"error":"synthetic","score":83}`))
	h := newToolHarnessWith(t, script, client.Limits{MaxAttempts: 1})

	advice := h.callError(t, ToolGetTrainingReadiness, stressArgs())
	assertNoRawPayload(t, advice)
	if strings.Contains(advice, "83") {
		t.Error("the refusal carries a reading from the scripted body")
	}
}
