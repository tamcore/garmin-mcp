//go:build garminlive

package live

import (
	"context"
	"fmt"
	"os"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// sweepPageSize is how many workouts and activities the start-of-suite sweep
// examines. It is the request layer's own default page size rather than a figure of
// this suite's, so a page it would refuse is never built. Leftovers can only come from
// a killed run, which creates a handful of objects, so a single page of that width is
// enough.
const sweepPageSize = client.DefaultMaxPageSize

// sweep removes what a killed run left behind, and nothing else.
//
// The cut-off is writeEnv.startedAt, the one instant this run is stamped with: only a
// name stamped strictly before it can be a leftover of an earlier run, and because that
// instant is also what every generated name of this run carries, nothing this run
// creates can be swept as though a previous run had left it.
//
// Only an object whose own name is one this suite rendered on an *earlier* run is
// touched. The name is read from Garmin and parsed field by field — prefix, label, run
// stamp, counter — and the run stamp must fall between nameStampFloor and the instant
// this run began; see isPreviousRunObject, which also states the residual risk. A name
// that merely starts with the prefix is not enough and is not admitted, and an object
// that is not admitted to the ledger cannot be removed at all, because the write guard
// would refuse the request.
//
// Calendar entries need no pass of their own: removing a workout template removes
// the entries that point at it, and this suite schedules no other template.
// It reports what it removed to stderr, and stays silent when there was nothing.
// A leftover means a previous run was killed or a delete failed, which is a defect
// worth seeing rather than a quiet repair.
func (w *writeEnv) sweep() error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*requestTimeout)
	defer cancel()

	page, err := client.NewPage(0, sweepPageSize)
	if err != nil {
		return fmt.Errorf("building the sweep page: %w", err)
	}
	workouts, err := w.sweepWorkouts(ctx, page)
	if err != nil {
		return err
	}
	activities, err := w.sweepActivities(ctx, page)
	if err != nil {
		return err
	}

	if workouts+activities > 0 {
		fmt.Fprintf(os.Stderr,
			"live: the sweeper removed %d workout(s) and %d activity(ies) carrying the %s "+
				"prefix, which a previous run left behind\n",
			workouts, activities, objectPrefix)
	}
	return nil
}

// sweepWorkouts removes prefixed leftovers from the workout library and reports how
// many it removed.
func (w *writeEnv) sweepWorkouts(ctx context.Context, page client.Page) (int, error) {
	summaries, err := w.workouts.List(ctx, w.session, page)
	if err != nil {
		return 0, fmt.Errorf("listing the workout library to sweep leftovers: %w", err)
	}

	removed := 0
	for _, summary := range summaries {
		value, present := summary.WorkoutID.Int64()
		if !present {
			continue
		}
		id, err := client.NewID(value)
		if err != nil {
			continue
		}
		if !w.owned.ownSwept(kindWorkout, summary.WorkoutName, value, w.startedAt) {
			continue
		}

		if _, err := w.workouts.Delete(ctx, w.session, id); err != nil {
			return removed, fmt.Errorf("removing a workout a previous run left behind: %w", err)
		}
		w.owned.release(kindWorkout, value)
		removed++
	}
	return removed, nil
}

// sweepActivities removes prefixed leftovers from the activity list and reports how
// many it removed.
func (w *writeEnv) sweepActivities(ctx context.Context, page client.Page) (int, error) {
	read, err := shared()
	if err != nil {
		return 0, err
	}
	listing, err := read.activities.List(ctx, w.session, api.ListQuery{Page: page})
	if err != nil {
		return 0, fmt.Errorf("listing activities to sweep leftovers: %w", err)
	}

	removed := 0
	for _, activity := range listing.Activities {
		if activity.ActivityID == nil {
			continue
		}
		id, err := client.NewID(*activity.ActivityID)
		if err != nil {
			continue
		}
		if !w.owned.ownSwept(kindActivity, activity.ActivityName, *activity.ActivityID, w.startedAt) {
			continue
		}
		if _, err := w.writes.Delete(ctx, w.session, id); err != nil {
			return removed, fmt.Errorf("removing an activity a previous run left behind: %w", err)
		}
		w.owned.release(kindActivity, *activity.ActivityID)
		removed++
	}
	return removed, nil
}
