//go:build garminlive

package live

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The two identifiers the guard probes use.
//
// Both are literals rather than anything read from the account, and the guard
// refuses the unowned one before dispatch, so no probe in this file can reach Garmin
// whatever the account holds. No identifier of the account under test appears here.
const (
	unownedID int64 = 1
	ownedID   int64 = 2
)

// probeGearUUID is a synthetic canonical UUID. It names no gear of the account's:
// the guard decides on the activity segment of a gear path, and every probe below is
// refused or absorbed before it is dispatched.
const probeGearUUID = "00000000-0000-0000-0000-000000000000"

// labelGearLink is the class label a gear probe failure names.
const labelGearLink = "gear link"

// TestLiveWriteCallerRefusesAMutationOfAnObjectThisSuiteDidNotCreate pins the
// ownership guard, which is what makes "a write test only ever mutates an object it
// created itself" a property of the wiring rather than a promise about the tests.
//
// The maintainer's own pre-existing activity and workout are protected by exactly
// this check, so the check itself is tested: a guard that silently stopped refusing
// would leave every later slice free to overwrite real data. Nothing below is
// dispatched — every probe is refused before the inner caller is reached — so this
// test performs no live traffic of its own.
func TestLiveWriteCallerRefusesAMutationOfAnObjectThisSuiteDidNotCreate(t *testing.T) {
	w := liveWriteEnv(t)

	inner := &countingCaller{}
	guard := writeCaller{inner: inner, owned: newOwnedObjects()}

	for _, probe := range probesFor(unownedID) {
		err := writeProbe(t, guard, probe.method, probe.path)
		switch {
		case err == nil:
			t.Errorf("the write guard accepted a %s against a %s this suite did not create",
				probe.method, probe.label)
		case !strings.Contains(err.Error(), "did not create"):
			t.Errorf("the %s refusal for a %s does not name the ownership rule",
				probe.method, probe.label)
		}
	}
	if inner.reached != 0 {
		t.Errorf("%d mutations of non-owned objects passed the guard and reached the transport",
			inner.reached)
	}

	// The live write session must be usable, or the tests that follow would be
	// asserting nothing.
	if w.session.IsZero() {
		t.Fatal("the live write session is unusable, so no write reached Garmin through the guard")
	}
}

// TestLiveWriteCallerAdmitsAMutationOfAnOwnedObject is the other half of the guard.
//
// A guard that refused everything would pass the test above and make every write
// test vacuous, so the admitted path is pinned too: once an identifier is in the
// ledger, the same request reaches the transport.
func TestLiveWriteCallerAdmitsAMutationOfAnOwnedObject(t *testing.T) {
	liveWriteEnv(t)

	inner := &countingCaller{}
	owned := seededLedger(t)
	guard := writeCaller{inner: inner, owned: owned}

	probes := probesFor(ownedID)
	for _, probe := range probes {
		if err := writeProbe(t, guard, probe.method, probe.path); err != errProbeReachedTransport {
			t.Errorf("the write guard refused a %s to a %s this suite created: %v",
				probe.method, probe.label, err)
		}
	}
	if inner.reached != len(probes) {
		t.Errorf("%d of %d mutations of owned objects reached the transport",
			inner.reached, len(probes))
	}
}

// TestLiveWriteCallerRefusesAnUnrecognisedMutation pins the allowlist.
//
// The guard recognises the endpoints this suite writes to and refuses everything
// else, so a mutating endpoint a later slice adds is refused until the guard is
// taught how to own its objects, rather than being waved through unowned.
func TestLiveWriteCallerRefusesAnUnrecognisedMutation(t *testing.T) {
	liveWriteEnv(t)

	inner := &countingCaller{}
	guard := writeCaller{inner: inner, owned: newOwnedObjects()}

	unrecognised := []pathProbe{
		{http.MethodPut, client.PathGearPrefix + "/link/1", labelGearLink},
		{http.MethodPut, client.PathUserSettings, "account settings"},
		{http.MethodPost, client.PathActivityTypes, "activity-type catalog"},
		{http.MethodPatch, client.PathActivityPrefix + "/1", labelActivity},
	}
	for _, probe := range unrecognised {
		err := writeProbe(t, guard, probe.method, probe.path)
		switch {
		case err == nil:
			t.Errorf("the write guard accepted an unrecognised %s to a %s",
				probe.method, probe.label)
		case !strings.Contains(err.Error(), "recognises"):
			t.Errorf("the refusal for an unrecognised %s does not name the allowlist rule",
				probe.method)
		}
	}
	if inner.reached != 0 {
		t.Errorf("%d unrecognised mutations reached the transport", inner.reached)
	}
}

// TestLiveDestructiveToolIsRefusedForAnObjectThisSuiteDidNotCreate proves the guard
// bites through the whole real stack rather than only in isolation.
//
// The call goes through the registry, the policy — which here enables both higher
// tiers and holds both scopes — and the confirmation middleware, which this client
// accepts. It is still refused, because the guard sits below all of them on the
// transport and the identifier it names is not one this suite created.
//
// The confirmation counter is what makes the refusal attributable. Every earlier gate
// is passed — the client was asked and it consented — so the only thing left to
// refuse the call is the guard on the transport. The refusal text is deliberately not
// asserted: the tool layer sanitises every error it returns, which is a property this
// server must keep, so the ownership wording is pinned at the caller instead.
func TestLiveDestructiveToolIsRefusedForAnObjectThisSuiteDidNotCreate(t *testing.T) {
	w := liveWriteEnv(t)

	asked := w.confirmations.Load()
	result := w.rawCall(t, tools.ToolDeleteActivity, map[string]any{argActivityID: unownedID})
	if !result.IsError {
		t.Fatalf("%s removed an activity this suite did not create", tools.ToolDeleteActivity)
	}
	if w.confirmations.Load() == asked {
		t.Fatalf("%s was refused before it was confirmed, so this proves the confirmation "+
			"gate rather than the ownership guard", tools.ToolDeleteActivity)
	}
	if text := resultText(result); text == "" {
		t.Errorf("the %s refusal names no reason at all", tools.ToolDeleteActivity)
	}
}

// seededLedger builds a ledger holding ownedID in all three classes.
//
// It reaches the ledger the way everything else does, through the verifying entry
// points: there is no way to declare ownership, so even a guard test has to present
// the evidence. The create bodies below are synthetic and name the literal ownedID,
// which is no identifier of the account's.
func seededLedger(t *testing.T) *ownedObjects {
	t.Helper()

	owned := newOwnedObjects()
	text := strconv.FormatInt(ownedID, 10)
	for kind, body := range map[ownedKind]string{
		kindActivity: `{"activityId":` + text + `}`,
		kindWorkout:  `{"workoutId":` + text + `}`,
	} {
		if !owned.ownCreated(kind, []byte(body), "") {
			t.Fatalf("the ledger refused a synthetic %s create response", kind)
		}
	}
	if !owned.ownScheduled(ownedID, ownedID) {
		t.Fatal("the ledger refused a calendar entry for the workout it was just given")
	}
	return owned
}

// pathProbe is one guard probe: a method, a path and the label a failure names.
type pathProbe struct {
	method string
	path   string
	label  string
}

// probesFor builds one probe per recognised targeted mutation.
func probesFor(id int64) []pathProbe {
	text := strconv.FormatInt(id, 10)
	activity := client.PathActivityPrefix + "/" + text
	workout := client.PathWorkoutPrefix + "/" + text
	schedule := client.PathWorkoutSchedule + "/" + text

	gear := client.PathGearPrefix + "/%s/" + probeGearUUID + "/activity/" + text

	return []pathProbe{
		{http.MethodPut, fmt.Sprintf(gear, "link"), labelGearLink},
		{http.MethodPut, fmt.Sprintf(gear, "unlink"), labelGearLink},
		{http.MethodPut, activity, labelActivity},
		{http.MethodPut, activity + "/" + client.SegmentExerciseSets, "activity set list"},
		{http.MethodDelete, activity, labelActivity},
		{http.MethodPut, workout, labelWorkout},
		{http.MethodDelete, workout, labelWorkout},
		{http.MethodPost, schedule, "workout schedule"},
		{http.MethodDelete, schedule, labelSchedule},
	}
}

// writeProbe pushes one request at the write guard and reports what came back.
// Nothing is dispatched: the inner caller never performs a request.
func writeProbe(t *testing.T, guard writeCaller, method, path string) error {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(), method, "https://connectapi.garmin.com"+path, nil)
	if err != nil {
		t.Fatalf("building the %s probe: %v", method, err)
	}
	_, err = guard.Do(t.Context(), livePrincipal, req)
	return err
}
