//go:build garminlive

package live

import (
	"strconv"
	"testing"
	"time"
)

// The two tests here are pure: they build a ledger and call it directly, and they
// reach neither Garmin nor the shared session. They exist because the ledger and the
// sweeper are the two pieces that decide what this suite may delete on a real account,
// and neither should be provable only by running against that account.

// TestSweeperAdoptsOnlyANameAnEarlierRunGenerated pins the sweeper's licence.
//
// A prefix is not ownership: it is a string a person could type. The sweeper therefore
// requires the whole shape suiteName renders and a run stamp older than this run, and
// this test walks the near misses one by one.
func TestSweeperAdoptsOnlyANameAnEarlierRunGenerated(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	past := strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)
	future := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	ancient := strconv.FormatInt(nameStampFloor.Add(-time.Hour).Unix(), 10)

	cases := map[string]struct {
		name string
		want bool
	}{
		"a name an earlier run generated":  {objectPrefix + "workout-" + past + "-1", true},
		"the prefix and nothing else":      {objectPrefix, false},
		"the prefix and a typed suffix":    {objectPrefix + "my own workout", false},
		"a stamp from this run or later":   {objectPrefix + "workout-" + future + "-1", false},
		"a stamp before the floor":         {objectPrefix + "workout-" + ancient + "-1", false},
		"a non-numeric stamp":              {objectPrefix + "workout-june-1", false},
		"a non-numeric counter":            {objectPrefix + "workout-" + past + "-x", false},
		"too few fields":                   {objectPrefix + past + "-1", false},
		"a name without the prefix at all": {"morning ride", false},
	}
	for label, want := range cases {
		if got := isPreviousRunObject(&want.name, now); got != want.want {
			t.Errorf("isPreviousRunObject(%s) = %v, want %v", label, got, want.want)
		}
	}
	if isPreviousRunObject(nil, now) {
		t.Error("isPreviousRunObject(no name) = true, want an unnamed object never adopted")
	}
}

// TestLedgerRefusesAnIdentifierNothingProves pins the other half: every path into the
// ledger verifies, so an identifier nothing Garmin said backs never becomes deletable.
func TestLedgerRefusesAnIdentifierNothingProves(t *testing.T) {
	t.Parallel()

	const probe int64 = 7
	owned := newOwnedObjects()

	if owned.ownCreated(kindWorkout, []byte(`{"someOtherField":7}`), "") {
		t.Error("a create response naming no workout was accepted as proof of ownership")
	}
	if owned.ownCreated(kindSchedule, []byte(`{"scheduleId":7}`), "") {
		t.Error("a calendar create was accepted, and a calendar create reports no identifier")
	}
	if owned.ownScheduled(probe, probe) {
		t.Error("a calendar entry was adopted for a workout this suite does not own")
	}
	stale := objectPrefix + "workout-1-1"
	if owned.ownSwept(kindWorkout, &stale, probe, time.Now().UTC()) {
		t.Error("a leftover was adopted from a name whose run stamp precedes the floor")
	}

	for _, kind := range deletionOrder() {
		if owned.owns(kind, probe) {
			t.Errorf("a refused %s reached the ledger anyway", kind)
		}
	}

	// The admitted path must still work, or every refusal above would be vacuous.
	if !owned.ownCreated(kindWorkout, []byte(`{"workoutId":"7"}`), "") {
		t.Fatal("a create response naming the workout was refused")
	}
	if !owned.owns(kindWorkout, probe) || !owned.ownScheduled(probe, probe) {
		t.Error("the proven workout did not reach the ledger")
	}
}
