package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// ActivityWrites updates and removes activities.
//
// Source: set_activity_name, set_activity_type, set_activity_description and
// delete_activity in python-garminconnect 0.3.10, plus the direct PUTs the
// pinned Taxuspt surface performs against "/activity-service/activity/{id}" for
// the event type, the feel rating and the perceived effort.
//
// Every method here is a write, so each one names its effect: an absolute-value
// PUT is EffectIdempotentWrite and may be repeated, a create is
// EffectUnsafeWrite and a removal is EffectDelete, and neither of those two is
// ever retried by the request layer.
type ActivityWrites struct {
	req requester
}

// NewActivityWrites returns an activity write client over the request layer.
func NewActivityWrites(rc *client.Client) (*ActivityWrites, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &ActivityWrites{req: req}, nil
}

// activityPath is the per-activity path every activity write targets.
func activityPath(id client.ID) string {
	return client.PathActivityPrefix + "/" + id.String()
}

// SetName renames one activity.
//
// Source: set_activity_name, which PUTs {"activityId", "activityName"}.
func (a *ActivityWrites) SetName(
	ctx context.Context, session client.Session, id client.ID, name string,
) (WriteResult, error) {
	req := writeRequest(client.OpSetActivityName, client.EndpointActivity,
		http.MethodPut, activityPath(id), client.EffectIdempotentWrite)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	title, err := requireText(req, name, "activity name")
	if err != nil {
		return WriteResult{}, err
	}

	body, err := jsonBody(req, struct {
		ActivityID   string `json:"activityId"`
		ActivityName string `json:"activityName"`
	}{ActivityID: id.String(), ActivityName: title})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body
	return a.apply(ctx, session, req)
}

// TypeChange is the strict request model for an activity-type change. Garmin
// needs the whole triple, because the type key alone does not identify the row
// in its own catalog. Source: set_activity_type.
type TypeChange struct {
	// TypeID is Garmin's numeric activity-type id.
	TypeID int64
	// TypeKey is the lowercase activity-type key, for example "running".
	TypeKey string
	// ParentTypeID is the numeric id of the parent type.
	ParentTypeID int64
}

// validate reports whether the change may be dispatched.
func (c TypeChange) validate(req client.Request) (TypeChange, error) {
	key, present, err := parseLowerToken(c.TypeKey, "activity type key")
	switch {
	case err != nil:
		return TypeChange{}, invalid(req, err)
	case !present:
		return TypeChange{}, invalid(req, fmt.Errorf("%w: an activity type key is required",
			client.ErrValidation))
	case c.TypeID <= 0, c.ParentTypeID <= 0:
		return TypeChange{}, invalid(req, fmt.Errorf(
			"%w: an activity type change needs a positive type id and parent type id",
			client.ErrValidation))
	}
	return TypeChange{TypeID: c.TypeID, TypeKey: key, ParentTypeID: c.ParentTypeID}, nil
}

// SetType changes the activity type of one activity.
func (a *ActivityWrites) SetType(
	ctx context.Context, session client.Session, id client.ID, change TypeChange,
) (WriteResult, error) {
	req := writeRequest(client.OpSetActivityType, client.EndpointActivity,
		http.MethodPut, activityPath(id), client.EffectIdempotentWrite)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	valid, err := change.validate(req)
	if err != nil {
		return WriteResult{}, err
	}

	type typeDTO struct {
		TypeID       int64  `json:"typeId"`
		TypeKey      string `json:"typeKey"`
		ParentTypeID int64  `json:"parentTypeId"`
	}
	body, err := jsonBody(req, struct {
		ActivityID string  `json:"activityId"`
		Type       typeDTO `json:"activityTypeDTO"`
	}{ActivityID: id.String(), Type: typeDTO(valid)})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body
	return a.apply(ctx, session, req)
}

// EventTypeChange is the strict request model for an event-type change. The key
// is what a caller names; the numeric id is optional because Garmin resolves the
// key, and a caller that already read the event-type catalog can supply both.
type EventTypeChange struct {
	// TypeKey is the event-type key, for example "race" or "training".
	TypeKey string
	// TypeID is Garmin's numeric event-type id. Zero omits it.
	TypeID int64
}

// eventTypeKeys are the keys Garmin's event-type catalog answers with. Source:
// the documented key list of the pinned set_activity_event_type tool. The set is
// closed here so an unknown key is refused before dispatch rather than stored.
var eventTypeKeys = [...]string{
	"race", "recreation", "specialEvent", "training", "transportation",
	"touring", "geocaching", "fitness", "uncategorized",
}

// ParseEventTypeKey validates an event-type key against the closed set.
func ParseEventTypeKey(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	for _, known := range eventTypeKeys {
		if trimmed == known {
			return known, nil
		}
	}
	return "", fmt.Errorf("%w: event type key is not one Garmin recognizes",
		client.ErrValidation)
}

// EventTypeKeys returns a copy of the recognized event-type keys.
func EventTypeKeys() []string {
	out := make([]string, len(eventTypeKeys))
	copy(out, eventTypeKeys[:])
	return out
}

// SetEventType changes the event type of one activity.
func (a *ActivityWrites) SetEventType(
	ctx context.Context, session client.Session, id client.ID, change EventTypeChange,
) (WriteResult, error) {
	req := writeRequest(client.OpSetActivityEventType, client.EndpointActivity,
		http.MethodPut, activityPath(id), client.EffectIdempotentWrite)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	key, parseErr := ParseEventTypeKey(change.TypeKey)
	if parseErr != nil {
		return WriteResult{}, invalid(req, parseErr)
	}

	type eventDTO struct {
		TypeKey string `json:"typeKey"`
		TypeID  int64  `json:"typeId,omitempty"`
	}
	body, err := jsonBody(req, struct {
		ActivityID string   `json:"activityId"`
		EventType  eventDTO `json:"eventTypeDTO"`
	}{ActivityID: id.String(), EventType: eventDTO{TypeKey: key, TypeID: change.TypeID}})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body
	return a.apply(ctx, session, req)
}

// SetDescription sets the description of one activity.
func (a *ActivityWrites) SetDescription(
	ctx context.Context, session client.Session, id client.ID, description string,
) (WriteResult, error) {
	req := writeRequest(client.OpSetActivityDescription, client.EndpointActivity,
		http.MethodPut, activityPath(id), client.EffectIdempotentWrite)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	text, err := requireText(req, description, "description")
	if err != nil {
		return WriteResult{}, err
	}

	body, err := jsonBody(req, struct {
		ActivityID  string `json:"activityId"`
		Description string `json:"description"`
	}{ActivityID: id.String(), Description: text})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body
	return a.apply(ctx, session, req)
}

// Feel is Garmin's five-point "How did you feel?" rating. Source: the pinned
// set_activity_feel tool, which stores 0, 25, 50, 75 or 100.
type Feel int

// The five feel values. Higher is better.
const (
	FeelVeryTired Feel = 0
	FeelTired     Feel = 25
	FeelNormal    Feel = 50
	FeelGood      Feel = 75
	FeelStrong    Feel = 100
)

// ParseFeel validates a feel rating against the five values Garmin stores.
func ParseFeel(value int) (Feel, error) {
	switch Feel(value) {
	case FeelVeryTired, FeelTired, FeelNormal, FeelGood, FeelStrong:
		return Feel(value), nil
	default:
		return 0, fmt.Errorf("%w: feel must be 0, 25, 50, 75 or 100", client.ErrValidation)
	}
}

// SetFeel sets the feel rating of one activity. It is health data, so neither
// the value nor the response is ever logged.
func (a *ActivityWrites) SetFeel(
	ctx context.Context, session client.Session, id client.ID, feel Feel,
) (WriteResult, error) {
	req := writeRequest(client.OpSetActivityFeel, client.EndpointActivity,
		http.MethodPut, activityPath(id), client.EffectIdempotentWrite)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	valid, parseErr := ParseFeel(int(feel))
	if parseErr != nil {
		return WriteResult{}, invalid(req, parseErr)
	}

	value := float64(valid)
	body, err := jsonBody(req, ratingBody(id, summaryUpdate{Feel: &value}))
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body
	return a.apply(ctx, session, req)
}

// MaxPerceivedEffort is the highest RPE Garmin accepts on its 0-10 scale.
const MaxPerceivedEffort = 10

// perceivedEffortScale is the factor Garmin stores an RPE with: RPE 7 is stored
// as 70. Source: the pinned set_perceived_effort tool.
const perceivedEffortScale = 10

// SetPerceivedEffort sets the RPE of one activity on Garmin's 0-10 scale, where
// 0 clears the rating. It is health data and is never logged.
func (a *ActivityWrites) SetPerceivedEffort(
	ctx context.Context, session client.Session, id client.ID, rpe float64,
) (WriteResult, error) {
	req := writeRequest(client.OpSetPerceivedEffort, client.EndpointActivity,
		http.MethodPut, activityPath(id), client.EffectIdempotentWrite)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	if rpe < 0 || rpe > MaxPerceivedEffort {
		return WriteResult{}, invalid(req, fmt.Errorf(
			"%w: perceived effort must be between 0 and 10", client.ErrValidation))
	}

	stored := rpe * perceivedEffortScale
	body, err := jsonBody(req, ratingBody(id, summaryUpdate{RPE: &stored}))
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body
	return a.apply(ctx, session, req)
}

// summaryUpdate is the summaryDTO patch both subjective ratings are written
// through. Exactly one field is ever set, so writing one rating never clears the
// other.
type summaryUpdate struct {
	Feel *float64 `json:"directWorkoutFeel,omitempty"`
	RPE  *float64 `json:"directWorkoutRpe,omitempty"`
}

// ratingBody builds the PUT body for a subjective rating.
func ratingBody(id client.ID, update summaryUpdate) any {
	return struct {
		ActivityID string        `json:"activityId"`
		Summary    summaryUpdate `json:"summaryDTO"`
	}{ActivityID: id.String(), Summary: update}
}

// Delete removes one activity.
//
// It is EffectDelete, so the request layer never repeats it: a caller sees the
// real outcome of the single attempt that was made.
func (a *ActivityWrites) Delete(
	ctx context.Context, session client.Session, id client.ID,
) (WriteResult, error) {
	req := writeRequest(client.OpDeleteActivity, client.EndpointActivity,
		http.MethodDelete, activityPath(id), client.EffectDelete)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	return a.apply(ctx, session, req)
}

// apply dispatches a write that reports only its outcome.
func (a *ActivityWrites) apply(
	ctx context.Context, session client.Session, req client.Request,
) (WriteResult, error) {
	payload, err := a.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}
