//go:build garminlive

package live

import (
	"os"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Result and argument field names, spelled once so a rename shows up in one place.
const (
	keyUnitKey        = "unit_key"
	keyDateTimestamp  = "date_timestamp"
	keyGMTTimestamp   = "gmt_timestamp"
	keyDeleteAll      = "delete_all"
	keyDeletedCount   = "deleted_count"
	weighInTestUnitKg = "kg"
)

// weighInTestKg is the arbitrary, bounded weight this suite records. It names
// nothing about the account: it is a small, plausible figure chosen only so
// Garmin's own validation accepts it, and it is removed again before the test ends.
const weighInTestKg = 61.5

// envWeighInDeleteAck is a fifth gate, narrower than and additional to the four
// AGENTS.md names: it covers only the tests in this file.
//
// delete_weigh_ins' own MCP argument surface (internal/tools/weighindelete.go)
// names a calendar date and a delete_all flag, never a sample identifier — unlike
// every other destructive tool this suite drives, whose one argument is the object
// to remove. weighinguard_test.go's guard makes it impossible for that gap to
// reach a sample this suite did not create: every per-sample DELETE the tool fans
// out to is refused before dispatch unless weighInLedger already owns it. What the
// guard cannot do is make the tool's own contract able to say "delete only mine" —
// it stays a date-and-flag call whose safety depends on a layer beneath it rather
// than on anything a caller passed — and that is worth a separate acknowledgement
// rather than only a comment, the same way nutritionsettingswrite_test.go requires
// one for set_nutrition_daily_settings' own structural gap.
const envWeighInDeleteAck = "GARMIN_LIVE_WEIGHIN_DELETE_ACK"

// weighInDeleteAckValue is the exact value envWeighInDeleteAck must carry, spelled
// out rather than truthy, like every other acknowledgement this suite reads.
const weighInDeleteAckValue = "i-accept-live-weighin-delete"

// requireWeighInDeleteAck skips the calling test unless the fifth gate is open.
func requireWeighInDeleteAck(t *testing.T) {
	t.Helper()

	if os.Getenv(envWeighInDeleteAck) != weighInDeleteAckValue {
		t.Skipf("not run — delete_weigh_ins takes a date and a delete_all flag, never a "+
			"sample identifier, so its own argument contract cannot express \"delete only "+
			"the weigh-in this test created\"; the guard beneath the tool refuses any other "+
			"sample by construction, but exercising the tool at all needs this separate "+
			"acknowledgement: set %s=%s", envWeighInDeleteAck, weighInDeleteAckValue)
	}
}

// TestLiveAddWeighInIsRemovedByDeleteWeighIns drives add_weigh_in from creation to
// removal through delete_weigh_ins.
//
// The day is checked for an existing weigh-in before this suite adds its own: if
// the account already carries one, delete_weigh_ins' confirmAll=false path — the
// only shape this suite ever asks for — would refuse the whole call rather than
// remove anything, so the test skips instead of colliding with a real reading.
func TestLiveAddWeighInIsRemovedByDeleteWeighIns(t *testing.T) {
	w := liveWriteEnv(t)
	requireWeighInDeleteAck(t)

	date := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	if w.dayHasWeighIn(t, date) {
		t.Skip("not run — the account already carries a weigh-in for the chosen day, and " +
			"delete_weigh_ins cannot remove only this suite's own without risking that reading")
	}

	before := w.weighInSamplesOn(t, date)
	created := w.call(t, tools.ToolAddWeighIn, map[string]any{
		keyWeight: weighInTestKg, keyUnitKey: weighInTestUnitKg,
	})
	if got, ok := created[keyWeight].(float64); !ok || got != weighInTestKg {
		t.Errorf("%s did not echo the weight it was given", tools.ToolAddWeighIn)
	}
	if got, _ := created[keyUnitKey].(string); got != weighInTestUnitKg {
		t.Errorf("%s did not echo the unit it was given", tools.ToolAddWeighIn)
	}

	w.deleteAddedWeighIn(t, date, before, tools.ToolAddWeighIn)
}

// TestLiveAddWeighInWithTimestampsIsRemovedByDeleteWeighIns is the timestamped add
// tool's own lifecycle, on a different day so the two tests cannot collide with
// each other.
func TestLiveAddWeighInWithTimestampsIsRemovedByDeleteWeighIns(t *testing.T) {
	w := liveWriteEnv(t)
	requireWeighInDeleteAck(t)

	when := time.Now().UTC().AddDate(0, 0, -2)
	date := when.Format(time.DateOnly)
	if w.dayHasWeighIn(t, date) {
		t.Skip("not run — the account already carries a weigh-in for the chosen day, and " +
			"delete_weigh_ins cannot remove only this suite's own without risking that reading")
	}

	stamp := when.Format(weighInLiveTimestampLayout)
	before := w.weighInSamplesOn(t, date)
	created := w.call(t, tools.ToolAddWeighInWithTimestamps, map[string]any{
		keyWeight: weighInTestKg, keyUnitKey: weighInTestUnitKg,
		keyDateTimestamp: stamp, keyGMTTimestamp: stamp,
	})
	if got, ok := created[keyWeight].(float64); !ok || got != weighInTestKg {
		t.Errorf("%s did not echo the weight it was given", tools.ToolAddWeighInWithTimestamps)
	}

	w.deleteAddedWeighIn(t, date, before, tools.ToolAddWeighInWithTimestamps)
}

// weighInLiveTimestampLayout matches weighinwrites.go's
// weighInTimestampEchoLayout: YYYY-MM-DDThh:mm:ss, no timezone, no fraction.
const weighInLiveTimestampLayout = "2006-01-02T15:04:05"

// deleteAddedWeighIn adopts the sample an add tool just recorded and removes it
// through delete_weigh_ins, asserting the tool actually deleted exactly one and
// that the day is empty afterward.
func (w *writeEnv) deleteAddedWeighIn(t *testing.T, date string, before map[int64]bool, addTool string) {
	t.Helper()

	after := w.weighInSamplesOn(t, date)
	id, ok := w.weighins.own(before, after, date)
	if !ok {
		t.Fatalf("%s reported success and no new, uniquely identifiable weigh-in appeared "+
			"for the day, so nothing can be safely removed", addTool)
	}
	w.keepCleanWeighIn(t, id, date)

	deleted := w.call(t, tools.ToolDeleteWeighIns, map[string]any{
		argDate: date, keyDeleteAll: false,
	})
	count, ok := deleted[keyDeletedCount].(float64)
	if !ok || int(count) != 1 {
		t.Errorf("%s reported %v deletions, want exactly 1",
			tools.ToolDeleteWeighIns, deleted[keyDeletedCount])
	}
	w.weighins.release(id)

	if remaining := w.weighInSamplesOn(t, date); len(remaining) != 0 {
		t.Errorf("%s reported success and the day still carries a weigh-in",
			tools.ToolDeleteWeighIns)
	}
}

// weighInSamplesOn reads the sample identifiers Garmin holds for one calendar day
// through the weight client directly, because the read-only tool surface
// (internal/tools/weighinreads.go's WeighInReading) never exposes the sample
// identifier the delete path needs.
func (w *writeEnv) weighInSamplesOn(t *testing.T, date string) map[int64]bool {
	t.Helper()

	parsed, err := client.ParseDate(date)
	if err != nil {
		t.Fatalf("parsing the weigh-in day: %v", err)
	}
	daily, err := w.weight.GetDailyWeighIns(t.Context(), w.session, parsed)
	if err != nil {
		t.Fatalf("reading the day's weigh-ins through the weight client: %v", err)
	}

	out := make(map[int64]bool, len(daily.DateWeightList))
	for _, sample := range daily.DateWeightList {
		if id, ok := sample.SamplePK.Int64Exact(); ok {
			out[id] = true
		}
	}
	return out
}

// dayHasWeighIn reports whether the account already carries any weigh-in for date.
func (w *writeEnv) dayHasWeighIn(t *testing.T, date string) bool {
	t.Helper()

	return len(w.weighInSamplesOn(t, date)) > 0
}
