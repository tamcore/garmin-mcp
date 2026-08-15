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
// requires the whole shape a generated name has and a run stamp older than this run, and
// this test walks the near misses one by one.
func TestSweeperAdoptsOnlyANameAnEarlierRunGenerated(t *testing.T) {
	t.Parallel()

	now := nameStampFloor.AddDate(0, 1, 0)
	past := strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)
	future := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	ancient := strconv.FormatInt(nameStampFloor.Add(-time.Hour).Unix(), 10)
	label := string(labelNameWorkout)

	cases := map[string]struct {
		name string
		want bool
	}{
		"a name an earlier run generated": {objectPrefix + label + "-" + past + "-1", true},
		"a two-word label an earlier run generated": {
			objectPrefix + string(labelNameWorkoutUpdated) + "-" + past + "-2", true,
		},
		"the prefix and nothing else":      {objectPrefix, false},
		"the prefix and a typed suffix":    {objectPrefix + "my own workout", false},
		"a stamp from this run or later":   {objectPrefix + label + "-" + future + "-1", false},
		"a stamp before the floor":         {objectPrefix + label + "-" + ancient + "-1", false},
		"a non-numeric stamp":              {objectPrefix + label + "-june-1", false},
		"a non-numeric counter":            {objectPrefix + label + "-" + past + "-x", false},
		"a counter of zero":                {objectPrefix + label + "-" + past + "-0", false},
		"a negative counter":               {objectPrefix + label + "-" + past + "--1", false},
		"a label this suite never renders": {objectPrefix + "ride-" + past + "-1", false},
		"an empty label":                   {objectPrefix + "-" + past + "-1", false},
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

// suiteBirth is the earliest date this write layer could have created anything. It is
// the month the write half was written, and every run of it is later.
var suiteBirth = time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)

// TestTheStampFloorIsNoEarlierThanThisSuite pins the figure rather than the mechanism.
//
// A floor before the suite existed admits stamps no run of it could have written, which
// is precisely the population of accidental matches the floor exists to exclude: it
// makes every name carrying a plausible unix second from a year this suite did not run
// in a candidate for deletion. The mechanism is tested above and passes at any floor,
// so the figure is asserted here.
func TestTheStampFloorIsNoEarlierThanThisSuite(t *testing.T) {
	t.Parallel()

	if nameStampFloor.Before(suiteBirth) {
		t.Errorf("the stamp floor is %s, want no earlier than %s: a floor before this suite "+
			"existed admits stamps no run of it wrote",
			nameStampFloor.Format(time.DateOnly), suiteBirth.Format(time.DateOnly))
	}
}

// TestLedgerRefusesAnIdentifierNothingProves pins the other half: every path into the
// ledger verifies, so an identifier nothing Garmin said backs never becomes deletable.
func TestLedgerRefusesAnIdentifierNothingProves(t *testing.T) {
	t.Parallel()

	const probe int64 = 7
	owned := newOwnedObjects()
	sent := objectPrefix + "workout-1780000000-1"

	refused := map[string]createdObject{
		"an identifier with no name behind it at all": {id: probe},
		"a name Garmin serves for a different object": {
			id: probe, sent: sent, stored: "someone else's workout",
		},
		"an object Garmin serves under no name": {id: probe, sent: sent},
		"a name this suite did not generate": {
			id: probe, sent: "my own workout", stored: "my own workout",
		},
		"an identifier that is not positive": {id: 0, sent: sent, stored: sent},
	}
	for label, created := range refused {
		if owned.ownCreated(kindWorkout, created) {
			t.Errorf("the ledger accepted %s as proof of ownership", label)
		}
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
	if !owned.ownCreated(kindWorkout, createdObject{id: probe, sent: sent, stored: sent}) {
		t.Fatal("a create whose read-back carries the name that was sent was refused")
	}
	if !owned.owns(kindWorkout, probe) || !owned.ownScheduled(probe, probe) {
		t.Error("the proven workout did not reach the ledger")
	}
}
