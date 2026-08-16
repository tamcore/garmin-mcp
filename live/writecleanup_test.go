//go:build garminlive

package live

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// keepClean registers the removal that must happen whatever the test does.
//
// It is registered immediately after a create, so a failing assertion, a t.Fatal or
// a panic still removes the object: t.Cleanup runs on every one of those paths. It
// is a no-op when the test already removed the object through the destructive tool,
// because a confirmed removal releases the identifier from the ledger.
//
// A removal that fails is reported as a test failure and never swallowed. An object
// left on a real account is a defect, and the run must say so.
func (w *writeEnv) keepClean(t *testing.T, kind ownedKind, id int64) {
	t.Helper()

	t.Cleanup(func() {
		if !w.owned.owns(kind, id) {
			return
		}
		if err := w.remove(kind, id); err != nil {
			t.Errorf("live: the %s this test created could not be removed and is left on the "+
				"account; the next run's sweeper removes it: %s", kind, safeError(err))
			return
		}
		w.owned.release(kind, id)
	})
}

// keepCleanFood is keepClean for a custom food: the identifier space the food ledger
// tracks rather than ownedObjects', so it needs its own removal path.
func (w *writeEnv) keepCleanFood(t *testing.T, id string) {
	t.Helper()

	t.Cleanup(func() {
		if !w.foods.ownsFood(id) {
			return
		}
		if err := w.removeCustomFood(id); err != nil {
			t.Errorf("live: the custom food this test created could not be removed and is left "+
				"on the account; it carries the %s prefix for a person to find by hand: %s",
				objectPrefix, safeError(err))
			return
		}
		w.foods.releaseFood(id)
	})
}

// keepCleanLog is keepClean for a food-log entry.
func (w *writeEnv) keepCleanLog(t *testing.T, id, mealDate string) {
	t.Helper()

	t.Cleanup(func() {
		if !w.foods.ownsLog(id) {
			return
		}
		if err := w.removeFoodLog(id, mealDate); err != nil {
			t.Errorf("live: the food-log entry this test created could not be removed and is "+
				"left on the account: %s", safeError(err))
			return
		}
		w.foods.releaseLog(id)
	})
}

// removeOutstanding deletes everything still in the ledger when the suite ends.
//
// It closes the one hole t.Cleanup cannot: a tool that creates an object and then
// fails on its own second step — create_strength_training_activity attaches its sets
// inside one call — leaves an object the guard owns and no test ever registered.
// Ownership is learned from Garmin's create response, so the ledger knows about it
// even though the test does not.
//
// It reports what it could not remove and returns how many are left.
func (w *writeEnv) removeOutstanding() int {
	left := 0
	for _, kind := range deletionOrder() {
		for _, id := range w.owned.identifiers(kind) {
			if err := w.remove(kind, id); err != nil {
				suiteLogger().Error(
					"live: an object this suite created could not be removed",
					slog.String("kind", kind.String()),
					slog.String("reason", safeError(err)))
				left++
				continue
			}
			w.owned.release(kind, id)
		}
	}
	left += w.removeOutstandingFoodLogs()
	left += w.removeOutstandingFoods()
	return left
}

// removeOutstandingFoodLogs deletes every food-log entry still in the food ledger.
// A leftover here is otherwise unreachable on a later run: Garmin's food-log surface
// carries no name for isPreviousRunObject to recognise the way a workout or an
// activity does.
func (w *writeEnv) removeOutstandingFoodLogs() int {
	left := 0
	for id, mealDate := range w.foods.logEntries() {
		if err := w.removeFoodLog(id, mealDate); err != nil {
			suiteLogger().Error(
				"live: a food-log entry this suite created could not be removed",
				slog.String("reason", safeError(err)))
			left++
			continue
		}
		w.foods.releaseLog(id)
	}
	return left
}

// removeOutstandingFoods deletes every custom food still in the food ledger.
func (w *writeEnv) removeOutstandingFoods() int {
	left := 0
	for _, id := range w.foods.foodIdentifiers() {
		if err := w.removeCustomFood(id); err != nil {
			suiteLogger().Error(
				"live: a custom food this suite created could not be removed",
				slog.String("reason", safeError(err)))
			left++
			continue
		}
		w.foods.releaseFood(id)
	}
	return left
}

// remove deletes one owned object through the domain client.
//
// Cleanup runs after the test context is cancelled, so it carries a context of its
// own. The guard still applies: the request only leaves this process because the
// object is in the ledger.
func (w *writeEnv) remove(kind ownedKind, id int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()

	target, err := client.NewID(id)
	if err != nil {
		return fmt.Errorf("the recorded identifier is unusable: %w", err)
	}

	switch kind {
	case kindActivity:
		_, err = w.writes.Delete(ctx, w.session, target)
	case kindWorkout:
		_, err = w.workouts.Delete(ctx, w.session, target)
	case kindSchedule:
		_, err = w.workouts.Unschedule(ctx, w.session, target)
	default:
		return fmt.Errorf("no removal is defined for a %s", kind)
	}
	return err
}

// removeCustomFood deletes one owned custom food through the nutrition client.
func (w *writeEnv) removeCustomFood(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()

	foodID, err := api.ParseFoodID(id)
	if err != nil {
		return fmt.Errorf("the recorded food identifier is unusable: %w", err)
	}
	_, err = w.nutrition.DeleteCustomFood(ctx, w.session, foodID)
	return err
}

// removeFoodLog deletes one owned food-log entry through the nutrition client.
func (w *writeEnv) removeFoodLog(id, mealDate string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()

	logID, err := api.ParseLogID(id)
	if err != nil {
		return fmt.Errorf("the recorded log identifier is unusable: %w", err)
	}
	date, err := client.ParseDate(mealDate)
	if err != nil {
		return fmt.Errorf("the recorded meal date is unusable: %w", err)
	}
	_, err = w.nutrition.DeleteFoodLog(ctx, w.session, date, logID)
	return err
}

// deleteViaTool drives one destructive tool over the write session and releases the
// object from the ledger once the object is provably gone.
//
// gone is that proof, and the reported success is not. A tool reports what Garmin
// answered, and a stale success, a no-op removal or a delete of something else all
// answer the same way — after which releasing the identifier would leave a real object
// on the account with nothing tracking it, invisible to the leak report and impossible
// for any cleanup to retry. An object whose absence cannot be established therefore
// stays in the ledger: the test fails, and the removal is attempted again by this
// test's own cleanup and by the end of the suite.
//
// It also asserts that the call really was confirmed. The destructive tier fails
// closed, so a tool that ran without asking would mean the confirmation middleware
// stopped being reached — exactly the kind of regression a live run should catch and
// a fixture cannot.
func (w *writeEnv) deleteViaTool(
	t *testing.T, tool, field string, kind ownedKind, id int64, gone absenceProof,
) {
	t.Helper()

	asked := w.confirmations.Load()
	result := w.call(t, tool, map[string]any{field: id})

	if deleted, _ := result["deleted"].(bool); !deleted {
		t.Fatalf("%s did not report the %s as deleted", tool, kind)
	}
	if w.confirmations.Load() == asked {
		t.Errorf("%s ran without asking for confirmation, so the destructive gate was not reached",
			tool)
	}
	if !awaitAbsent(gone) {
		t.Errorf("%s reported the %s as deleted and it is still there; it stays in the ledger "+
			"so the cleanup removes it", tool, kind)
		return
	}
	w.owned.release(kind, id)
}

// The absence proof. proofs consecutive reads must agree the object is gone, within
// reads attempts.
//
// One read is not enough and repetition alone is not either. Garmin serves some of these
// objects through a gateway that lags in both directions, so a single absent answer can
// precede a present one — a removal that never happened would then be certified by the
// first read that had not caught up yet. Requiring consecutive agreement is what turns
// "it did not answer this time" into "it is gone", and the attempt bound is what keeps a
// wait from becoming a pass. Every read is separated from the last by rawCall's own
// pause, so consecutive agreement is agreement over time rather than inside one instant.
//
// The two classes below are priced differently on purpose, and the difference is what
// each read is evidence of rather than how careful the test feels. See absenceProof.
const (
	recordAbsenceProofs = 2
	recordAbsenceReads  = 5

	calendarAbsenceProofs = 3
	calendarAbsenceReads  = 8
)

// An absenceProof is a repeated read that decides whether one object is gone, together
// with the agreement it demands.
//
// The two are one value because they are not independent: how much agreement is enough
// depends on what a single absent answer proves, and that differs per class.
//
//   - A record read — a workout template, an activity — has an authoritative negative.
//     The REST tier answers a removed record with the one refusal the tool layer renders
//     as AdviceNoSuchRecord, and no other outcome counts, so an absent answer is Garmin
//     stating the record is not there. Two agreeing reads then establish it.
//
//   - A calendar read has **no** authoritative negative, and this is the honest limit of
//     this suite. The workout calendar is served by a GraphQL gateway that answers a day
//     with the entries it holds; an entry that was never replicated to the replica
//     serving the read is indistinguishable from an entry that was deleted, because both
//     are simply not in the list. There is no per-entry fetch that can answer "no such
//     entry". Repeating the read raises the number of replicas that must all have missed
//     the entry, and it cannot rule out a single lagging replica answering every read.
//     That residual is accepted and not papered over: what actually guarantees the
//     calendar is clean is the removal of the workout template the entry points at,
//     which *is* proven authoritatively — Garmin removes an entry with its template, and
//     every test that schedules also deletes the template and proves that removal by
//     AdviceNoSuchRecord. The entry check is a check on unschedule_workout's own effect,
//     not the guarantee that nothing is left behind.
type absenceProof struct {
	gone   func() bool
	proofs int
	reads  int
}

// recordAbsence is the proof for a class with an authoritative not-found.
func recordAbsence(gone func() bool) absenceProof {
	return absenceProof{gone: gone, proofs: recordAbsenceProofs, reads: recordAbsenceReads}
}

// calendarAbsence is the proof for the one class that has none.
func calendarAbsence(gone func() bool) absenceProof {
	return absenceProof{gone: gone, proofs: calendarAbsenceProofs, reads: calendarAbsenceReads}
}

// awaitAbsent reports whether the object was consistently absent.
func awaitAbsent(proof absenceProof) bool {
	agreed := 0
	for range proof.reads {
		if !proof.gone() {
			agreed = 0
			continue
		}
		agreed++
		if agreed >= proof.proofs {
			return true
		}
	}
	return false
}

// workoutGone reports whether the workout library no longer holds one template.
//
// The evidence is the one refusal that means "no such record", not any refusal. A rate
// limit, an expired session, a gateway error and a response this server could not
// decode are all failures of the read, and reading a delete into them is how a run
// certifies a removal that never happened. Every other outcome is reported as unknown,
// which leaves the object in the ledger.
func (w *writeEnv) workoutGone(t *testing.T, id int64) absenceProof {
	t.Helper()

	return recordAbsence(func() bool {
		return noSuchRecord(w.rawCall(t, tools.ToolGetWorkoutByID,
			map[string]any{argWorkoutID: id}))
	})
}

// activityGone reports the same for one activity record.
func (w *writeEnv) activityGone(t *testing.T, id int64) absenceProof {
	t.Helper()

	return recordAbsence(func() bool {
		return noSuchRecord(w.rawCall(t, tools.ToolGetActivity,
			map[string]any{argActivityID: id}))
	})
}

// entryGone reports whether the calendar no longer carries one created workout's entry
// on one date.
//
// It is the weakest of the three proofs and it says so: the GraphQL calendar gateway
// offers no authoritative not-found for a single entry, so absence here is an entry
// missing from a list rather than Garmin stating the entry does not exist. See
// absenceProof for what that leaves open and for what closes it.
func (w *writeEnv) entryGone(t *testing.T, workout int64, date string) absenceProof {
	t.Helper()

	return calendarAbsence(func() bool {
		_, present := w.scheduledEntry(t, workout, date)
		return !present
	})
}

// noSuchRecord reports whether a tool result is the authored refusal for a record
// Garmin does not hold.
//
// It compares against the tool layer's own exported constant rather than a phrase
// spelled here, so the two cannot drift apart: every tool error is authored advice by
// design and carries no class, status or identifier, which leaves this sentence as the
// only signal that distinguishes a removed object from a failed read.
func noSuchRecord(result *mcp.CallToolResult) bool {
	return result.IsError && strings.Contains(resultText(result), tools.AdviceNoSuchRecord)
}
