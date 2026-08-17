//go:build garminlive

package live

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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
// the entries that point at it, and this suite schedules no other template. A
// custom food has no calendar analogue and no int64 identifier either, so its own
// pass, sweepFoods, matches leftovers by name the same way foodguard_test.go's
// storedCustomFood binds a create, rather than by fetching a single item — Garmin
// exposes no per-item GET for a custom food. A course carries an int64 identifier
// but the same missing per-item GET, so sweepCourses matches its leftovers by name
// too, over the one whole listing course-service's GetCourses gives rather than a
// page.
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
	foods, err := w.sweepFoods(ctx, page)
	if err != nil {
		return err
	}
	courses, err := w.sweepCourses(ctx)
	if err != nil {
		return err
	}

	if workouts+activities+foods+courses > 0 {
		suiteLogger().Info(
			"live: the sweeper removed leftovers a previous run left behind",
			slog.Int("workouts", workouts), slog.Int("activities", activities),
			slog.Int("foods", foods), slog.Int("courses", courses),
			slog.String("prefix", objectPrefix))
	}
	return nil
}

// sweepCourses removes prefixed leftovers from the course listing and reports how
// many it removed.
//
// Unlike sweepWorkouts and sweepActivities, this walks no page: course-service's
// GetCourses (courses.go) takes no page parameter of its own, only the whole listing.
func (w *writeEnv) sweepCourses(ctx context.Context) (int, error) {
	list, err := w.courses.GetCourses(ctx, w.session)
	if err != nil {
		return 0, fmt.Errorf("listing courses to sweep leftovers: %w", err)
	}

	removed := 0
	for _, course := range list {
		value, present := course.CourseID.Int64Exact()
		if !present {
			continue
		}
		id, err := client.NewID(value)
		if err != nil {
			continue
		}
		var namePtr *string
		if name, ok := course.Name.Value(); ok {
			namePtr = &name
		}
		if !w.owned.ownSwept(kindCourse, namePtr, value, w.startedAt) {
			continue
		}

		if _, err := w.courses.DeleteCourse(ctx, w.session, id); err != nil {
			return removed, fmt.Errorf("removing a course a previous run left behind: %w", err)
		}
		w.owned.release(kindCourse, value)
		removed++
	}
	return removed, nil
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
		// Int64Exact, not Int64: this identifier addresses the DELETE below, and Int64
		// truncates the float64 the payload was parsed into. A listing that named
		// 18446744.9, or two identifiers one apart above 2^53, would otherwise resolve
		// to an identifier the library never listed and the sweeper would delete that.
		value, present := summary.WorkoutID.Int64Exact()
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

// sweepFoods removes prefixed leftovers from the custom-food library and reports
// how many it removed.
//
// A custom food has no per-item GET and no int64 identifier, so this walks the
// whole library — an empty search lists everything — the same way
// foodguard_test.go's storedCustomFood finds a created food by name, and adopts a
// leftover through foodLedger.ownSweptFood, the string-keyed analogue of
// ownedObjects.ownSwept guarded by the same isPreviousRunObject licence.
func (w *writeEnv) sweepFoods(ctx context.Context, page client.Page) (int, error) {
	library, err := w.nutrition.CustomFoods(ctx, w.session, "", page)
	if err != nil {
		return 0, fmt.Errorf("listing the custom-food library to sweep leftovers: %w", err)
	}

	removed := 0
	for _, item := range library.CustomFoods.Items() {
		if item.Meta == nil {
			continue
		}
		id, present := item.Meta.FoodID.Value()
		if !present || id == "" {
			continue
		}
		if !w.foods.ownSweptFood(item.Meta.FoodName, id, w.startedAt) {
			continue
		}

		parsed, err := api.ParseFoodID(id)
		if err != nil {
			continue
		}
		if _, err := w.nutrition.DeleteCustomFood(ctx, w.session, parsed); err != nil {
			return removed, fmt.Errorf("removing a custom food a previous run left behind: %w", err)
		}
		w.foods.releaseFood(id)
		removed++
	}
	return removed, nil
}

// ownSweptFood adopts a leftover custom food from an earlier run, the string-keyed
// analogue of ownedObjects.ownSwept: it requires the same name shape and run-stamp
// licence isPreviousRunObject enforces for a workout or activity, because a custom
// food carries no int64 identifier for that function's own signature to take.
func (f *foodLedger) ownSweptFood(name *string, id string, before time.Time) bool {
	if id == "" || !isPreviousRunObject(name, before) {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.foods[id] = struct{}{}
	return true
}
