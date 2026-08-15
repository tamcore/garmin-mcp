//go:build garminlive

package live

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
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
				"account; the next run's sweeper removes it: %v", kind, err)
			return
		}
		w.owned.release(kind, id)
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
				fmt.Fprintf(os.Stderr,
					"live: a %s this suite created could not be removed: %v\n", kind, err)
				left++
				continue
			}
			w.owned.release(kind, id)
		}
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

// deleteViaTool drives one destructive tool over the write session and releases the
// object from the ledger once Garmin confirmed the removal.
//
// It also asserts that the call really was confirmed. The destructive tier fails
// closed, so a tool that ran without asking would mean the confirmation middleware
// stopped being reached — exactly the kind of regression a live run should catch and
// a fixture cannot.
func (w *writeEnv) deleteViaTool(
	t *testing.T, tool, field string, kind ownedKind, id int64,
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
	w.owned.release(kind, id)
}
