package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestGetMorningTrainingReadinessSelectsTheWakeUpSnapshot pins the selection rule of
// the pinned upstream source: the entry whose inputContext is AFTER_WAKEUP_RESET wins,
// whatever its position in the day.
func TestGetMorningTrainingReadinessSelectsTheWakeUpSnapshot(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(readinessToolPath(),
		testkit.JSON(http.StatusOK, readinessDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetMorningTrainingReadiness, stressArgs())

	if got, _ := result[argDate].(string); got != stressDate {
		t.Errorf("date = %q, want %q", got, stressDate)
	}
	if has, _ := result["has_data"].(bool); !has {
		t.Error("has_data = false, want true")
	}
	if from, _ := result["from_wakeup_reset"].(bool); !from {
		t.Error("from_wakeup_reset = false, want true: the fixture carries that context")
	}
	readiness := object(t, result, "readiness")
	if got := number(t, readiness, "score"); got != 83 {
		t.Errorf("score = %v, want the after-waking snapshot's", got)
	}
	if got, _ := readiness["context"].(string); got != "AFTER_WAKEUP_RESET" {
		t.Errorf("context = %q, want AFTER_WAKEUP_RESET", got)
	}
}

// TestGetMorningTrainingReadinessCarriesTheMetricsARealAccountReturns pins the three
// metrics a sampled account actually gets back: recovery time, sleep score and acute
// load. Upstream's morning tool omits null keys, so those three are what survives, and
// its remaining keys — readinessScore, readinessLevel, hrvStatus, bodyBattery,
// chronicLoad — name nothing in the document this URL returns.
//
// Source, verified at the pinned commit: get_morning_training_readiness reads
// recoveryTime, sleepScore and acuteLoad, the same wire names get_training_readiness
// reads, and renders recoveryTime as hours rounded to one decimal.
func TestGetMorningTrainingReadinessCarriesTheMetricsARealAccountReturns(t *testing.T) {
	t.Parallel()

	body := `[{"calendarDate":"` + stressDate + `","inputContext":"AFTER_WAKEUP_RESET",` +
		`"recoveryTime":165,"sleepScore":74,"acuteLoad":312}]`
	script := testkit.NewScript().With(readinessToolPath(), testkit.JSON(http.StatusOK, body))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetMorningTrainingReadiness, stressArgs())

	readiness := object(t, result, "readiness")
	// 165 minutes is 2.75 hours, which upstream rounds to one decimal.
	if got := number(t, readiness, "recovery_time_hours"); got != 2.8 {
		t.Errorf("recovery_time_hours = %v, want 2.8", got)
	}
	if got := number(t, readiness, "sleep_score"); got != 74 {
		t.Errorf("sleep_score = %v, want 74", got)
	}
	if got := number(t, readiness, "acute_load"); got != 312 {
		t.Errorf("acute_load = %v, want 312", got)
	}
	if got, _ := result[argDate].(string); got != stressDate {
		t.Errorf("date = %q, want the day that was asked for", got)
	}
	// Every other curated key is absent for this document, which is the normal case.
	for _, key := range []string{"score", "level", keyHRVWeeklyAverage, "sleep_factor_percent"} {
		if _, present := readiness[key]; present {
			t.Errorf("%s is present, want it omitted for a document without it", key)
		}
	}
}

// TestGetMorningTrainingReadinessFallsBackAndSaysSo pins the other half of upstream's
// rule: a device that records no trigger yields the first snapshot, and the result
// admits that it is not the after-waking one.
func TestGetMorningTrainingReadinessFallsBackAndSaysSo(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(readinessToolPath(),
		testkit.JSON(http.StatusOK, `[{"score":41},{"score":42}]`))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetMorningTrainingReadiness, stressArgs())

	if has, _ := result["has_data"].(bool); !has {
		t.Error("has_data = false, want true: a snapshot was returned")
	}
	if from, _ := result["from_wakeup_reset"].(bool); from {
		t.Error("from_wakeup_reset = true, want false for the fallback")
	}
	if got := number(t, object(t, result, "readiness"), "score"); got != 41 {
		t.Errorf("score = %v, want the first snapshot's", got)
	}
}

// TestGetMorningTrainingReadinessReportsNoSnapshotAsEmpty proves a day with nothing to
// select is an answer rather than a failure.
func TestGetMorningTrainingReadinessReportsNoSnapshotAsEmpty(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{caseEmptyArray: `[]`, jsonNull: jsonNull} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(readinessToolPath(),
				testkit.JSON(http.StatusOK, body))
			h := newToolHarness(t, script)

			result := h.call(t, ToolGetMorningTrainingReadiness, stressArgs())
			if has, _ := result["has_data"].(bool); has {
				t.Error("has_data = true, want false for a day with no snapshot")
			}
			if _, present := result["readiness"]; present {
				t.Error("readiness is present, want it omitted when nothing was selected")
			}
		})
	}
}

// TestGetMorningTrainingReadinessSharesTheReadinessURL proves the two readiness tools
// are two views of one read rather than two different requests.
func TestGetMorningTrainingReadinessSharesTheReadinessURL(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(readinessToolPath(),
		testkit.JSON(http.StatusOK, readinessDocument))
	h := newToolHarness(t, script)

	h.call(t, ToolGetTrainingReadiness, stressArgs())
	h.call(t, ToolGetMorningTrainingReadiness, stressArgs())

	requests := h.fake.Requests()
	if len(requests) != 2 {
		t.Fatalf("the fake received %d requests, want one per call", len(requests))
	}
	for _, request := range requests {
		if request.Path != readinessToolPath() {
			t.Errorf("path = %q, want the one readiness URL %q",
				request.Path, readinessToolPath())
		}
	}
}

// TestGetMorningTrainingReadinessSanitizesAGarminFailure proves no raw body reaches the
// caller.
func TestGetMorningTrainingReadinessSanitizesAGarminFailure(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(readinessToolPath(),
		testkit.JSON(http.StatusForbidden, `{"error":"synthetic","sleepScore":88}`))
	h := newToolHarness(t, script)

	advice := h.callError(t, ToolGetMorningTrainingReadiness, stressArgs())
	assertNoRawPayload(t, advice)
}

// TestMorningReadinessLogValueOmitsTheProvenanceFlag is the provenance decision.
// FromWakeupReset looks like shape — it names which branch the selection took — but
// the branch is chosen by a predicate over the payload: it answers whether the day's
// snapshots carried a wake-up trigger at all. Across days that is a record of when the
// account wore a device and slept, which is the same coverage leak as a count of
// readings that passed a value test. The caller still gets the flag; the log does not.
func TestMorningReadinessLogValueOmitsTheProvenanceFlag(t *testing.T) {
	t.Parallel()

	rendered := MorningReadinessResult{
		HasData: true, FromWakeupReset: true,
	}.LogValue().String()

	if strings.Contains(rendered, "fromWakeupReset") {
		t.Errorf("LogValue = %s, want no payload-derived provenance", rendered)
	}
	if !strings.Contains(rendered, "hasData=true") {
		t.Errorf("LogValue = %s, want the shape of the answer", rendered)
	}
}
