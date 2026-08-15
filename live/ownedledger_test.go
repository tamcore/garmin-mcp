//go:build garminlive

package live

import (
	"strconv"
	"testing"
	"time"
)

// The three tests here are pure: they build a ledger and call it directly, and they
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

	now := nameStampFloor().AddDate(0, 1, 0)
	past := strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)
	future := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	ancient := strconv.FormatInt(nameStampFloor().Add(-time.Hour).Unix(), 10)
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

		// The integer forms strconv.ParseInt accepts and strconv.FormatInt cannot
		// produce. None of them is a string this suite's generator can write, so none
		// of them is evidence that this suite wrote the name, however plausible the
		// value behind it is.
		"a signed counter":       {objectPrefix + label + "-" + past + "-+1", false},
		"a zero-padded counter":  {objectPrefix + label + "-" + past + "-01", false},
		"a signed stamp":         {objectPrefix + label + "-+" + past + "-1", false},
		"a zero-padded stamp":    {objectPrefix + label + "-0" + past + "-1", false},
		"a counter with a space": {objectPrefix + label + "-" + past + "- 1", false},
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

// TestSweeperTreatsARunInTheSameSecondAsConcurrent is the precision half, and it is a
// different question from "is this stamp older than this run".
//
// A generated name stamps whole seconds; the instant a run starts carries nanoseconds.
// Two runs that start inside the same second therefore write identical stamps, and the
// one that starts later compares the other's stamp — read back as that second's zero
// nanosecond — as strictly earlier than its own start. That is a live run's objects
// classified as a dead run's leftovers and swept out from under it, mid-test. The
// cut-off is truncated to the resolution the name carries so the two compare equal
// instead, and equal is not earlier.
func TestSweeperTreatsARunInTheSameSecondAsConcurrent(t *testing.T) {
	t.Parallel()

	// A run that started 400 ms into a second, and a concurrent run whose names carry
	// that same second.
	started := nameStampFloor().AddDate(0, 1, 0).Add(400 * time.Millisecond)
	concurrent := objectPrefix + string(labelNameWorkout) + "-" +
		strconv.FormatInt(started.Unix(), 10) + "-1"
	if isPreviousRunObject(&concurrent, started) {
		t.Error("a concurrent run's object was classified as an earlier run's leftover, " +
			"so this run would delete objects a live run is still using")
	}

	// The second before it is still a leftover, or the sweeper would stop working.
	earlier := objectPrefix + string(labelNameWorkout) + "-" +
		strconv.FormatInt(started.Add(-time.Second).Unix(), 10) + "-1"
	if !isPreviousRunObject(&earlier, started) {
		t.Error("an object stamped a second before this run was not adopted, so the " +
			"sweeper no longer removes what a killed run left behind")
	}
}

// suiteBirth is the instant this write layer first existed: the author date of commit
// 9e82609, which introduced it. Every run of the write half is later than it, and no run
// of it is earlier.
//
// It is a function rather than a variable because a package-level variable is mutable
// state, which AGENTS.md forbids, and time.Date cannot be a constant.
func suiteBirth() time.Time {
	return time.Date(2026, time.August, 15, 14, 44, 33, 0, time.UTC)
}

// TestTheStampFloorIsExactlyThisSuitesBirth pins the figure rather than the mechanism.
//
// A floor before the suite existed admits stamps no run of it could have written, which
// is precisely the population of accidental matches the floor exists to exclude: it makes
// every name carrying a plausible unix second from a time this suite did not run in a
// candidate for deletion. The mechanism is tested above and passes at any floor, so the
// figure is asserted here — as equality, not as a range, because a range is what let the
// floor sit at the midnight before the suite was written and admit almost fifteen hours
// of seconds no run of it ever stamped.
func TestTheStampFloorIsExactlyThisSuitesBirth(t *testing.T) {
	t.Parallel()

	if !nameStampFloor().Equal(suiteBirth()) {
		t.Errorf("the stamp floor is %s, want exactly %s: a floor before this suite existed "+
			"admits stamps no run of it wrote, and one after it stops the sweeper removing "+
			"this suite's own leftovers",
			nameStampFloor().Format(time.RFC3339), suiteBirth().Format(time.RFC3339))
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
		"an identifier with no name behind it at all": {id: probe, storedID: probe},
		"a name Garmin serves for a different object": {
			id: probe, sent: sent, stored: "someone else's workout", storedID: probe,
		},
		"an object Garmin serves under no name": {id: probe, sent: sent, storedID: probe},
		"a name this suite did not generate": {
			id: probe, sent: "my own workout", stored: "my own workout", storedID: probe,
		},
		"an identifier that is not positive": {id: 0, sent: sent, stored: sent, storedID: 0},

		// The two the name comparison alone cannot refuse. A generated name carries a
		// one-second run stamp and a counter, so a concurrent run renders the same name
		// for the same object class: a create identifier that named *that* run's object
		// would match on the name and on nothing else.
		"a read-back that reports a different object's identifier": {
			id: probe, sent: sent, stored: sent, storedID: probe + 1,
		},
		"a read-back that reports no identifier at all": {
			id: probe, sent: sent, stored: sent,
		},
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
	if !owned.ownCreated(kindWorkout, createdObject{
		id: probe, sent: sent, stored: sent, storedID: probe,
	}) {
		t.Fatal("a create whose read-back reports the identifier addressed and the name that " +
			"was sent was refused")
	}
	if !owned.owns(kindWorkout, probe) || !owned.ownScheduled(probe, probe) {
		t.Error("the proven workout did not reach the ledger")
	}
}
