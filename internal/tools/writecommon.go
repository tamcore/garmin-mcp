package tools

import (
	"context"
	"log/slog"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// activityIDIntegerProperty declares an activity identifier the manifest types as a
// plain integer rather than as a number-or-string union.
func activityIDIntegerProperty() Property {
	return Property{
		Name:        "activity_id",
		Types:       []string{typeInteger},
		Description: "the Garmin activity identifier, as a positive whole number",
		Minimum:     bound(1),
		Required:    true,
	}
}

// workoutIDProperty declares a workout identifier as the number-or-string union the
// manifest states for get_workout_by_id.
func workoutIDProperty() Property {
	return Property{
		Name:        argNameWorkoutID,
		Types:       []string{typeInteger, typeString},
		Description: "the Garmin workout identifier, as a positive number or decimal string",
		Minimum:     bound(1),
		MaxLength:   new(maxIdentifierArgumentLen),
		Required:    true,
	}
}

// workoutIDIntegerProperty declares a workout identifier the manifest types as a
// plain integer.
func workoutIDIntegerProperty() Property {
	return Property{
		Name:        argNameWorkoutID,
		Types:       []string{typeInteger},
		Description: "the Garmin workout identifier, a positive whole number from get_workouts",
		Minimum:     bound(1),
		Required:    true,
	}
}

// scheduledWorkoutIDProperty declares a calendar-entry identifier, which is not the
// workout's own identifier: unscheduling by workout id removes nothing.
func scheduledWorkoutIDProperty() Property {
	return Property{
		Name:  "scheduled_workout_id",
		Types: []string{typeInteger},
		Description: "the Garmin calendar-entry identifier, which is not the workout's own " +
			"identifier",
		Minimum:  bound(1),
		Required: true,
	}
}

// calendarDateProperty declares a calendar-date argument in Garmin's YYYY-MM-DD form.
func calendarDateProperty(name, description string) Property {
	return Property{
		Name:        name,
		Types:       []string{typeString},
		Description: description,
		Format:      formatDate,
		Pattern:     patternCalendarDate,
		MaxLength:   new(maxDateArgumentLen),
		Required:    true,
	}
}

// An ActivityUpdate reports one applied change to one activity.
//
// It reports what was changed rather than the changed value: the value is the
// caller's own argument, and for a feel or an effort rating it is health data.
type ActivityUpdate struct {
	ActivityID int64  `json:"activity_id" jsonschema:"the activity that was updated"`
	Updated    string `json:"updated" jsonschema:"which field of the activity was written"`
	Status     int    `json:"status" jsonschema:"the HTTP status Garmin answered the write with"`
}

// LogValue reports that an activity was updated, never what it was updated to.
func (u ActivityUpdate) LogValue() slog.Value {
	return shape("activityUpdate", slog.Int("status", u.Status))
}

// newActivityUpdate maps a write result onto the bounded acknowledgement.
func newActivityUpdate(id client.ID, field string, result api.WriteResult) ActivityUpdate {
	return ActivityUpdate{ActivityID: id.Int64(), Updated: field, Status: result.Status}
}

// activityWrite is the shape every single-activity write shares: the validated
// identifier and the principal's session.
type activityWrite struct {
	id      client.ID
	session client.Session
}

// resolveActivityWrite validates the identifier first, so a malformed identifier
// costs no Garmin call at all, and only then resolves the session.
func (s *service) resolveActivityWrite(ctx context.Context, raw any) (activityWrite, error) {
	id, err := parseActivityIdentifier(raw)
	if err != nil {
		return activityWrite{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return activityWrite{}, err
	}
	return activityWrite{id: id, session: session}, nil
}

// A BatchOutcome is one item's result inside a batch tool.
//
// A batch loops the single-item API call, so one item's failure does not abandon the
// rest: each item reports its own outcome and the batch reports how many succeeded.
// The advice is the same authored, sanitized text a single call would have returned.
type BatchOutcome struct {
	ID      int64  `json:"id" jsonschema:"the identifier this item named"`
	Applied bool   `json:"applied" jsonschema:"whether Garmin accepted this item"`
	Status  int    `json:"status,omitempty" jsonschema:"the HTTP status Garmin answered with"`
	Advice  string `json:"advice,omitempty" jsonschema:"why this item was not applied"`
}

// LogValue reports whether an item applied, never which record it named.
func (o BatchOutcome) LogValue() slog.Value {
	return shape("batchOutcome", slog.Bool("applied", o.Applied))
}

// A BatchResult is what a batch tool reports.
type BatchResult struct {
	Outcomes  []BatchOutcome `json:"outcomes" jsonschema:"one outcome per requested item, in order"`
	Requested int            `json:"requested" jsonschema:"how many items were requested"`
	Applied   int            `json:"applied" jsonschema:"how many items Garmin accepted"`
}

// LogValue reports the batch counts, never the records.
func (r BatchResult) LogValue() slog.Value {
	return shape("batchResult",
		slog.Int("requested", r.Requested),
		slog.Int("applied", r.Applied),
	)
}

// newBatchResult tallies the outcomes into the reported result.
func newBatchResult(outcomes []BatchOutcome) BatchResult {
	applied := 0
	for _, outcome := range outcomes {
		if outcome.Applied {
			applied++
		}
	}
	return BatchResult{Outcomes: outcomes, Requested: len(outcomes), Applied: applied}
}

// failedOutcome renders one item's failure with authored advice only.
func failedOutcome(id int64, err error) BatchOutcome {
	return BatchOutcome{ID: id, Applied: false, Advice: fail(err).Error()}
}
