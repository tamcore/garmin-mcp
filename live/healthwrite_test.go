//go:build garminlive

package live

import (
	"math"
	"os"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// This file is the write half of the three data-management tools:
// add_body_composition, set_blood_pressure and add_hydration_data
// (internal/garmin/api/datamanagement.go). None of the three is an owned-object
// lifecycle the way a workout, an activity or a course is: each POST or PUT appends
// a health record with no identifier in its response, and this codebase registers no
// delete tool for any of the three (internal/tools/register.go's readOnlyTools and
// destructiveTools carry get_body_composition and get_blood_pressure but no
// delete_body_composition or delete_blood_pressure, and get_hydration_data answers a
// daily total rather than a per-entry list).
//
// The three are not equally undoable, and they are not treated alike here:
//
//   - add_body_composition (bodycomposition.go) uploads a FIT file through the
//     shared /upload endpoint. Garmin's response carries no identifier
//     (AddBodyCompositionResult echoes only the date and weight this call sent,
//     never anything Garmin assigned), and no tool in this codebase or upstream
//     python-garminconnect 0.3.10 reads or removes a single body-composition
//     reading. A reading this test adds is on the account permanently.
//   - set_blood_pressure (bloodpressure.go) POSTs one reading with the same shape
//     of response — an echo of what was sent plus an HTTP status, never an
//     identifier — and again no delete tool exists for it anywhere in this
//     codebase. A reading this test adds is on the account permanently too.
//   - add_hydration_data (hydration.go) is different in kind: Garmin's hydration
//     document is a daily *total* rather than a list of entries
//     (get_hydration_data's own Hydration.ValueML is one figure per day, see
//     get_hydration_data.go), and the tool's own value_in_ml argument is signed,
//     "positive) or subtract (negative)" by its own manifest description. A write
//     of +N therefore has an exact, in-contract undo: a second write of -N on the
//     same day, which this test issues from a t.Cleanup registered only after the
//     forward +N call has returned, so it runs whatever the test's own assertions
//     do but never for a forward write that itself never landed. The account's one
//     observable figure, the day's total, is restored to what it was before this
//     test ran.
//
// The first two are gated behind a sixth, narrower acknowledgement, the same shape
// nutritionsettingswrite_test.go and weighinwrite_test.go each already use for their
// own irreversible or structurally-gapped write. The third needs no such gate: its
// effect is undone by this suite itself, and
// TestLiveHydrationRoundTripIsUndoneByACompensatingWrite proves the total actually
// returns to what it was.

// envHealthWriteAck is a sixth gate, narrower than and additional to the four
// AGENTS.md names: it covers only add_body_composition and set_blood_pressure, the
// two writes in this file this suite cannot undo.
const envHealthWriteAck = "GARMIN_LIVE_HEALTH_WRITE_ACK"

// healthWriteAckValue is the exact value envHealthWriteAck must carry, spelled out
// rather than truthy, like every other acknowledgement this suite reads.
const healthWriteAckValue = "i-accept-live-irreversible-health-writes"

// requireHealthWriteAck skips the calling test unless the sixth gate is open.
func requireHealthWriteAck(t *testing.T) {
	t.Helper()

	if os.Getenv(envHealthWriteAck) != healthWriteAckValue {
		t.Skipf("not run — %s and %s each append a health record Garmin assigns no "+
			"identifier to, and no tool in this codebase or upstream removes a single "+
			"reading of either kind; driving them adds data to the real account that "+
			"cannot be taken back. set %s=%s to accept that",
			tools.ToolAddBodyComposition, tools.ToolSetBloodPressure,
			envHealthWriteAck, healthWriteAckValue)
	}
}

// The arbitrary, bounded readings this suite records when the sixth gate is open.
// Every figure is a plausible human value chosen only so Garmin's own validation
// accepts it, and none of them describes anything about the account.
const (
	bodyCompositionTestKg  = 70.0
	bloodPressureSystolic  = 118
	bloodPressureDiastolic = 76
	bloodPressurePulse     = 64
)

// TestLiveAddBodyCompositionRecordsAReading drives add_body_composition once.
//
// There is nothing to clean up: see this file's own doc comment for why no delete
// exists for a body-composition reading.
func TestLiveAddBodyCompositionRecordsAReading(t *testing.T) {
	w := liveWriteEnv(t)
	requireHealthWriteAck(t)

	date := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	result := w.call(t, tools.ToolAddBodyComposition, map[string]any{
		argDate: date, keyWeight: bodyCompositionTestKg,
	})
	if got, ok := result[argDate].(string); !ok || got != date {
		t.Errorf("%s did not echo the date it was given", tools.ToolAddBodyComposition)
	}
	if got, ok := result[keyWeight].(float64); !ok || got != bodyCompositionTestKg {
		t.Errorf("%s did not echo the weight it was given", tools.ToolAddBodyComposition)
	}
}

// TestLiveSetBloodPressureRecordsAReading drives set_blood_pressure once.
//
// There is nothing to clean up: see this file's own doc comment for why no delete
// exists for a blood-pressure reading.
func TestLiveSetBloodPressureRecordsAReading(t *testing.T) {
	w := liveWriteEnv(t)
	requireHealthWriteAck(t)

	result := w.call(t, tools.ToolSetBloodPressure, map[string]any{
		"systolic": bloodPressureSystolic, "diastolic": bloodPressureDiastolic,
		"pulse": bloodPressurePulse,
	})
	if got, ok := result["systolic"].(float64); !ok || int(got) != bloodPressureSystolic {
		t.Errorf("%s did not echo the systolic reading it was given", tools.ToolSetBloodPressure)
	}
	if got, ok := result["diastolic"].(float64); !ok || int(got) != bloodPressureDiastolic {
		t.Errorf("%s did not echo the diastolic reading it was given", tools.ToolSetBloodPressure)
	}
	if got, ok := result["pulse"].(float64); !ok || int(got) != bloodPressurePulse {
		t.Errorf("%s did not echo the pulse it was given", tools.ToolSetBloodPressure)
	}
}

// hydrationTestDeltaML is the arbitrary, bounded volume this test adds and then
// subtracts again. It is well inside add_hydration_data's own +/-10000 ml bound
// (hydration.go's maxHydrationMLArgument) with room either side for whatever the
// account's own total already is.
const hydrationTestDeltaML = 250

// hydrationTolerance absorbs float round-tripping through JSON only; Garmin's own
// total is otherwise expected to move by exactly what was written.
const hydrationTolerance = 0.01

// TestLiveHydrationRoundTripIsUndoneByACompensatingWrite drives add_hydration_data
// forward and then back on the same calendar day, and proves the account's one
// observable figure — the day's total, from get_hydration_data — returns to what it
// was before this test ran.
//
// The compensating write happens in a t.Cleanup registered only after the forward
// write has returned, so it runs whatever the test's own assertions do but never
// for a forward write that itself never landed, the same shape
// nutritionsettingswrite_test.go's cleanup restores the account's calorie goal with.
func TestLiveHydrationRoundTripIsUndoneByACompensatingWrite(t *testing.T) {
	w := liveWriteEnv(t)

	date := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	before := w.call(t, tools.ToolGetHydrationData, map[string]any{argDate: date})
	originalML, _ := before["value_ml"].(float64)

	added := w.addHydrationUndoably(t, date, hydrationTestDeltaML, originalML)
	if got, ok := added["value_in_ml"].(float64); !ok || got != hydrationTestDeltaML {
		t.Errorf("%s did not echo the volume it was given", tools.ToolAddHydrationData)
	}

	updated := w.call(t, tools.ToolGetHydrationData, map[string]any{argDate: date})
	updatedML, _ := updated["value_ml"].(float64)
	if math.Abs(updatedML-(originalML+hydrationTestDeltaML)) > hydrationTolerance {
		t.Errorf("%s did not add its volume to the day's total", tools.ToolAddHydrationData)
	}
}

// addHydrationUndoably writes deltaML to the day and only then registers the
// compensating write that takes it back.
//
// The ordering is the whole point, and it is enforced here by construction rather
// than by a caller remembering it: there is no way to register the undo without
// having made the forward write first, because the registration happens after
// w.call has returned inside this helper. w.call fails the test with t.Fatalf, and
// Go runs every already-registered t.Cleanup after a Fatal — so an undo registered
// before the forward write would fire for a write that never landed and take the
// account's total below where it started, which is damage the suite inflicted
// rather than found.
func (w *writeEnv) addHydrationUndoably(
	t *testing.T, date string, deltaML, originalML float64,
) map[string]any {
	t.Helper()

	added := w.call(t, tools.ToolAddHydrationData, map[string]any{
		"value_in_ml": deltaML, "cdate": date, "timestamp": date + "T12:00:00.000",
	})

	t.Cleanup(func() {
		compensated := w.call(t, tools.ToolAddHydrationData, map[string]any{
			"value_in_ml": -deltaML, "cdate": date,
			"timestamp": date + "T12:00:01.000",
		})
		if got, ok := compensated["value_in_ml"].(float64); !ok || got != -deltaML {
			t.Errorf("%s did not echo the compensating volume", tools.ToolAddHydrationData)
		}
		after := w.call(t, tools.ToolGetHydrationData, map[string]any{argDate: date})
		restoredML, _ := after["value_ml"].(float64)
		if math.Abs(restoredML-originalML) > hydrationTolerance {
			t.Errorf("live: the compensating write did not restore the account's hydration "+
				"total for %s: it stays changed on the real account", date)
		}
	})
	return added
}
