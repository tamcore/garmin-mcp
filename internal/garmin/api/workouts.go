package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Workouts is the workout library: the templates themselves and their calendar
// schedules.
//
// Source: get_workouts, get_workout_by_id, upload_workout, update_workout,
// delete_workout, download_workout, schedule_workout and unschedule_workout in
// python-garminconnect 0.3.10.
type Workouts struct {
	req requester
}

// NewWorkouts returns a workout client over the request layer.
func NewWorkouts(rc *client.Client) (*Workouts, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Workouts{req: req}, nil
}

// WorkoutDocument is a validated workout body.
//
// It is the strict request model for a write whose content this package cannot
// interpret: Garmin owns the workout schema and it drifts, so the document is
// carried verbatim. What is enforced here is everything that does not depend on
// that schema — it is well-formed JSON, it is an object or an array, and it fits
// inside MaxRequestBodyBytes.
type WorkoutDocument struct {
	body []byte
}

// ParseWorkoutDocument validates a workout body a caller supplied.
func ParseWorkoutDocument(body []byte) (WorkoutDocument, error) {
	trimmed := bytes.TrimSpace(body)
	switch {
	case len(trimmed) == 0:
		return WorkoutDocument{}, fmt.Errorf("%w: a workout document must not be empty",
			client.ErrValidation)
	case len(trimmed) > MaxRequestBodyBytes:
		return WorkoutDocument{}, fmt.Errorf("%w: workout document exceeds its bound",
			client.ErrValidation)
	case trimmed[0] != '{' && trimmed[0] != '[':
		return WorkoutDocument{}, fmt.Errorf(
			"%w: a workout document must be a JSON object or array", client.ErrValidation)
	case !json.Valid(trimmed):
		return WorkoutDocument{}, fmt.Errorf("%w: a workout document must be valid JSON",
			client.ErrValidation)
	}
	return WorkoutDocument{body: bytes.Clone(trimmed)}, nil
}

// IsZero reports whether the document is unset.
func (d WorkoutDocument) IsZero() bool { return len(d.body) == 0 }

// IsObject reports whether the document is a single JSON object, which an
// in-place update requires.
func (d WorkoutDocument) IsObject() bool { return len(d.body) > 0 && d.body[0] == '{' }

// Bytes returns a copy of the validated document.
func (d WorkoutDocument) Bytes() []byte { return bytes.Clone(d.body) }

// withWorkoutID returns a copy of the document whose workoutId is id. Source:
// update_workout, which forces the body's workoutId to match the path so the
// workout keeps its identity and existing schedules stay valid.
func (d WorkoutDocument) withWorkoutID(id client.ID) (WorkoutDocument, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(d.body, &fields); err != nil {
		return WorkoutDocument{}, fmt.Errorf("%w: a workout update must be a JSON object",
			client.ErrValidation)
	}

	merged := make(map[string]json.RawMessage, len(fields)+1)
	maps.Copy(merged, fields)
	merged["workoutId"] = json.RawMessage(strconv.FormatInt(id.Int64(), 10))

	body, err := json.Marshal(merged)
	if err != nil {
		return WorkoutDocument{}, fmt.Errorf("%w: workout update could not be encoded",
			client.ErrValidation)
	}
	return ParseWorkoutDocument(body)
}

// WorkoutSummary is one entry of the workout library.
//
// It is health material: a workout describes a person's training, so it is never
// logged. Every field is optional and an unknown field never fails the response.
type WorkoutSummary struct {
	WorkoutID    client.Number   `json:"workoutId"`
	WorkoutName  *string         `json:"workoutName"`
	Description  *string         `json:"description"`
	UpdatedDate  *string         `json:"updateDate"`
	CreatedDate  *string         `json:"createdDate"`
	SportType    json.RawMessage `json:"sportType"`
	EstimatedSec client.Number   `json:"estimatedDurationInSecs"`
}

// Workout is one workout template. The segment structure keeps its raw shape,
// because Garmin owns that schema and changes it.
type Workout struct {
	WorkoutID   client.Number   `json:"workoutId"`
	WorkoutName *string         `json:"workoutName"`
	SportType   json.RawMessage `json:"sportType"`
	Segments    json.RawMessage `json:"workoutSegments"`

	raw client.Payload
}

// Payload is the retained raw response, which carries the fields this model does
// not name.
func (w Workout) Payload() client.Payload { return w.raw }

// SavedWorkout is what an upload or an in-place update reports.
//
// The identifier and the name are the server's, not the caller's: Garmin
// normalizes a name and assigns an id, and reporting what was sent instead of
// what was saved is how a caller ends up scheduling a workout that does not
// exist.
type SavedWorkout struct {
	WorkoutID   client.Number `json:"workoutId"`
	WorkoutName *string       `json:"workoutName"`

	raw client.Payload
}

// Payload is the retained raw response.
func (w SavedWorkout) Payload() client.Payload { return w.raw }

// ID reports the saved workout's validated identifier.
func (w SavedWorkout) ID() (client.ID, error) {
	value, ok := w.WorkoutID.Int64()
	if !ok {
		return client.ID{}, fmt.Errorf("%w: the workout response carried no workout id",
			client.ErrMalformedPayload)
	}
	return client.NewID(value)
}

// Name reports the saved workout's server-side name, and whether Garmin
// returned one.
func (w SavedWorkout) Name() (string, bool) {
	if w.WorkoutName == nil {
		return "", false
	}
	return *w.WorkoutName, true
}

// workoutPath is the per-workout path, built from a validated identifier.
func workoutPath(id client.ID) string {
	return client.PathWorkoutPrefix + "/" + id.String()
}

// List reads one page of the workout library.
func (w *Workouts) List(
	ctx context.Context, session client.Session, page client.Page,
) ([]WorkoutSummary, error) {
	query := url.Values{}
	query.Set(client.QueryStart, strconv.Itoa(page.Start()))
	query.Set(client.QueryLimit, strconv.Itoa(page.Limit()))

	req := readRequest(client.OpListWorkouts, client.EndpointWorkoutList,
		client.PathWorkouts, query)
	if err := w.req.limits().ValidatePage(page); err != nil {
		return nil, invalid(req, err)
	}

	var items client.List[WorkoutSummary]
	if _, err := w.req.read(ctx, session, req, &items); err != nil {
		return nil, err
	}
	return items.Items(), nil
}

// Get reads one workout template.
func (w *Workouts) Get(
	ctx context.Context, session client.Session, id client.ID,
) (Workout, error) {
	req := readRequest(client.OpGetWorkout, client.EndpointWorkout, workoutPath(id), nil)
	if err := requireID(req, id); err != nil {
		return Workout{}, err
	}

	var workout Workout
	payload, err := w.req.read(ctx, session, req, &workout)
	if err != nil {
		return Workout{}, err
	}
	workout.raw = payload
	return workout, nil
}

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
	if _, reported := saved.WorkoutID.Int64(); reported {
		return saved, nil
	}
	return w.readSaved(ctx, session, id)
}

// readSaved reports what Garmin stored, for an update it answered with no content.
//
// Garmin answers an in-place workout update with 204 and an empty body, so the
// answer names neither the workout nor the name it stored. Treating that as a
// malformed payload reported failure for an update that had succeeded. The rule the
// type documents still holds — the identifier and the name are the server's, not the
// caller's — so they are read back rather than echoed from the request. Confirmed
// against the live service on 2026-08-15.
func (w *Workouts) readSaved(
	ctx context.Context, session client.Session, id client.ID,
) (SavedWorkout, error) {
	stored, err := w.Get(ctx, session, id)
	if err != nil {
		return SavedWorkout{}, err
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

// Delete removes one workout template. It is EffectDelete and is never retried.
func (w *Workouts) Delete(
	ctx context.Context, session client.Session, id client.ID,
) (WriteResult, error) {
	req := writeRequest(client.OpDeleteWorkout, client.EndpointWorkout,
		http.MethodDelete, workoutPath(id), client.EffectDelete)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	return w.apply(ctx, session, req)
}

// Schedule puts one workout on a calendar date.
//
// It is EffectUnsafeWrite, not idempotent: Garmin accepts the same workout on
// the same date more than once and creates a second calendar entry, so the
// request layer never repeats it. Upstream's MCP layer avoids the duplicate with
// a pre-check that fails open; a pre-check is not idempotency and this package
// does not claim it.
func (w *Workouts) Schedule(
	ctx context.Context, session client.Session, id client.ID, date client.Date,
) (WriteResult, error) {
	req := writeRequest(client.OpScheduleWorkout, client.EndpointWorkoutSchedule,
		http.MethodPost, client.PathWorkoutSchedule+"/"+id.String(),
		client.EffectUnsafeWrite)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	if err := requireDate(req, date); err != nil {
		return WriteResult{}, err
	}

	body, err := jsonBody(req, struct {
		Date string `json:"date"`
	}{Date: date.String()})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body
	return w.apply(ctx, session, req)
}

// Unschedule removes one calendar entry without deleting the template. The
// identifier is the scheduled-workout id, not the workout id.
func (w *Workouts) Unschedule(
	ctx context.Context, session client.Session, scheduled client.ID,
) (WriteResult, error) {
	req := writeRequest(client.OpUnscheduleWorkout, client.EndpointWorkoutSchedule,
		http.MethodDelete, client.PathWorkoutSchedule+"/"+scheduled.String(),
		client.EffectDelete)
	if err := requireID(req, scheduled); err != nil {
		return WriteResult{}, err
	}
	return w.apply(ctx, session, req)
}

// Download streams the FIT encoding of one workout into dst.
//
// The sink is the caller's. This package never opens, creates or names a file,
// and both the wire and the decompressed bound of the request layer apply.
func (w *Workouts) Download(
	ctx context.Context, session client.Session, id client.ID, dst io.Writer,
) (client.Download, error) {
	req := client.Request{
		Op:           client.OpDownloadWorkout,
		Endpoint:     client.EndpointWorkoutDownload,
		Path:         client.PathWorkoutFITPrefix + "/" + id.String(),
		Effect:       client.EffectRead,
		FileTransfer: true,
	}
	if err := requireID(req, id); err != nil {
		return client.Download{}, err
	}
	return w.req.download(ctx, session, req, dst)
}

// apply dispatches a write that reports only its outcome.
func (w *Workouts) apply(
	ctx context.Context, session client.Session, req client.Request,
) (WriteResult, error) {
	payload, err := w.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}
