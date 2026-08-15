package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// This file holds the workout writes: creating one, replacing one in place, and the
// shared dispatch both use. They sit apart from the reads because between them they
// carry the rule that the stored identifier and name are the server's, never the
// caller's, and the checks that keep Garmin to it.

// Upload creates a new workout from a caller-supplied document.
//
// It is EffectUnsafeWrite: repeating it creates a duplicate workout, so the
// request layer never retries it.
func (w *Workouts) Upload(
	ctx context.Context, session client.Session, document WorkoutDocument,
) (SavedWorkout, error) {
	req := writeRequest(client.OpUploadWorkout, client.EndpointWorkout,
		http.MethodPost, client.PathWorkoutPrefix, client.EffectUnsafeWrite)
	if document.IsZero() {
		return SavedWorkout{}, invalid(req, fmt.Errorf("%w: a workout document is required",
			client.ErrValidation))
	}
	req.Body = document.Bytes()
	return w.save(ctx, session, req)
}

// Update replaces an existing workout in place.
//
// Garmin replaces the whole workout through a PUT, so the document must be the
// complete structure. The workout keeps its id — that is the point of the
// method: a calendar schedule pointing at it stays valid, which it would not if
// the workout were deleted and re-uploaded. The body's workoutId is forced to
// match the path, and the document validators are the ones Upload uses.
//
// It is EffectIdempotentWrite: the same document PUT twice converges on the same
// workout.
func (w *Workouts) Update(
	ctx context.Context, session client.Session, id client.ID, document WorkoutDocument,
) (SavedWorkout, error) {
	req := writeRequest(client.OpUpdateWorkout, client.EndpointWorkout,
		http.MethodPut, workoutPath(id), client.EffectIdempotentWrite)
	if err := requireID(req, id); err != nil {
		return SavedWorkout{}, err
	}
	if document.IsZero() || !document.IsObject() {
		return SavedWorkout{}, invalid(req, fmt.Errorf(
			"%w: a workout update needs a complete JSON object", client.ErrValidation))
	}

	merged, err := document.withWorkoutID(id)
	if err != nil {
		return SavedWorkout{}, invalid(req, err)
	}
	req.Body = merged.Bytes()

	saved, err := w.save(ctx, session, req)
	if err != nil {
		return SavedWorkout{}, err
	}
	// An identifier that is present and not an exact integer is a mismatch rather than
	// an absence: the answer named something, and what it named is not the workout that
	// was addressed. Only an answer naming no identifier at all is read back.
	if saved.WorkoutID.IsSet() {
		if reported, exact := saved.WorkoutID.Int64Exact(); !exact || reported != id.Int64() {
			return SavedWorkout{}, mismatch(req, wrongWorkoutError())
		}
		return saved, nil
	}
	return w.readSaved(ctx, session, req, id)
}

// wrongWorkoutError is what an answer naming a workout other than the one addressed
// is reported as.
//
// It is a failure and never a result. An update reports the identifier a caller then
// schedules, deletes or updates again, so an identifier that is not the one the
// request addressed would send every one of those at a workout the caller never asked
// for — the maintainer's own, if Garmin ever answered a write with a cached or drifted
// object. The identifier itself is not named: it is account data.
//
// Both comparisons that raise it read the answer through Number.Int64Exact rather than
// Number.Int64, and the difference is the whole check. Int64 truncates a float64 the
// payload was parsed into: an answer naming 123.9 would compare equal to a requested
// 123, and above 2^53 two identifiers one apart compare equal in either direction. An
// identifier is compared on the digits the payload carried or it is not compared at all.
func wrongWorkoutError() error {
	return fmt.Errorf("%w: the workout answer names a different workout than the request",
		client.ErrMalformedPayload)
}

// readSaved reports what Garmin stored, for an update it answered with no content.
//
// Garmin answers an in-place workout update with 204 and an empty body, so the
// answer names neither the workout nor the name it stored. Treating that as a
// malformed payload reported failure for an update that had succeeded. The rule the
// type documents still holds — the identifier and the name are the server's, not the
// caller's — so they are read back rather than echoed from the request. Confirmed
// against the live service on 2026-08-15.
//
// The read-back is still checked against the request. A GET that answers with some
// other workout — a cache, a redirect, upstream drift — would otherwise be copied into
// the result verbatim and reported as the workout that was updated.
func (w *Workouts) readSaved(
	ctx context.Context, session client.Session, req client.Request, id client.ID,
) (SavedWorkout, error) {
	stored, err := w.Get(ctx, session, id)
	if err != nil {
		return SavedWorkout{}, err
	}
	if reported, exact := stored.WorkoutID.Int64Exact(); !exact || reported != id.Int64() {
		return SavedWorkout{}, mismatch(req, wrongWorkoutError())
	}
	return SavedWorkout{
		WorkoutID:   stored.WorkoutID,
		WorkoutName: stored.WorkoutName,
		raw:         stored.raw,
	}, nil
}

// save dispatches an upload or an update and reports what Garmin saved.
func (w *Workouts) save(
	ctx context.Context, session client.Session, req client.Request,
) (SavedWorkout, error) {
	var saved SavedWorkout
	payload, err := w.req.write(ctx, session, req, &saved)
	if err != nil {
		return SavedWorkout{}, err
	}
	saved.raw = payload
	return saved, nil
}
