package api

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// ActivityDetails reads the per-activity collections whose payload shape varies by
// activity type.
//
// Source: get_activity_typed_splits ("/activity-service/activity/{id}/typedsplits")
// and get_activity_exercise_sets (".../exerciseSets"). Upstream types both as
// dict[str, Any] and hands the raw shape to the caller, because a bouldering session,
// an interval run and a strength workout do not answer alike.
type ActivityDetails struct {
	req requester
}

// NewActivityDetails returns an activity-detail client over the request layer.
func NewActivityDetails(rc *client.Client) (*ActivityDetails, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &ActivityDetails{req: req}, nil
}

// TypedSplit is one split, lap or interval.
//
// It is sensitive: it carries per-interval health and elevation detail. Every
// measurement is a union decoder, because the same field arrives as a number on one
// device and as a numeric string on another.
type TypedSplit struct {
	Type            client.Text   `json:"type"`
	MessageIndex    client.Number `json:"messageIndex"`
	Distance        client.Number `json:"distance"`
	Duration        client.Number `json:"duration"`
	ElapsedDuration client.Number `json:"elapsedDuration"`
	MovingDuration  client.Number `json:"movingDuration"`
	AverageHR       client.Number `json:"averageHR"`
	MaxHR           client.Number `json:"maxHR"`
	Calories        client.Number `json:"calories"`
	MaxElevation    client.Number `json:"maxElevation"`
	StartTimeGMT    *string       `json:"startTimeGMT"`
}

// TypedSplits is the typed-split collection.
//
// It is the union-decoded endpoint of this slice. Garmin answers with an object keyed
// "splits", an object keyed "lapDTOs", a bare array, or — for an activity with a
// single interval — one bare object. All four decode to the same list, and an
// unrecognized object decodes to no splits rather than failing, so a shape change
// cannot break an otherwise useful read.
type TypedSplits struct {
	splits []TypedSplit
	raw    client.Payload
}

// splitEnvelope names the keys Garmin has been observed to nest the collection under.
type splitEnvelope struct {
	Splits  client.List[TypedSplit] `json:"splits"`
	LapDTOs client.List[TypedSplit] `json:"lapDTOs"`
}

// UnmarshalJSON accepts every shape described on TypedSplits.
func (s *TypedSplits) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*s = TypedSplits{}
		return nil
	}

	if trimmed[0] == '[' {
		var items []TypedSplit
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return err
		}
		*s = TypedSplits{splits: items}
		return nil
	}

	var envelope splitEnvelope
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return err
	}
	if envelope.Splits.Len() > 0 || envelope.LapDTOs.Len() > 0 {
		*s = TypedSplits{splits: pickSplits(envelope)}
		return nil
	}
	return s.decodeSingle(trimmed)
}

// decodeSingle treats the object as one split, which is how an activity with a single
// interval answers.
func (s *TypedSplits) decodeSingle(data []byte) error {
	var single TypedSplit
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	if !single.Type.IsSet() && !single.Distance.IsSet() && !single.Duration.IsSet() {
		// An object with none of the split fields is a wrapper this package does not
		// know. Reporting no splits keeps the rest of the response usable.
		*s = TypedSplits{}
		return nil
	}
	*s = TypedSplits{splits: []TypedSplit{single}}
	return nil
}

// pickSplits prefers the "splits" key and falls back to "lapDTOs".
func pickSplits(envelope splitEnvelope) []TypedSplit {
	if envelope.Splits.Len() > 0 {
		return envelope.Splits.Items()
	}
	return envelope.LapDTOs.Items()
}

// Splits returns a copy of the decoded splits, so no caller can mutate the value
// another caller holds.
func (s TypedSplits) Splits() []TypedSplit {
	out := make([]TypedSplit, len(s.splits))
	copy(out, s.splits)
	return out
}

// Len is the split count.
func (s TypedSplits) Len() int { return len(s.splits) }

// MarshalJSON normalizes the volatile shape to an array for every consumer
// downstream.
func (s TypedSplits) MarshalJSON() ([]byte, error) {
	if s.splits == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(s.splits)
}

// Payload is the retained raw response.
func (s TypedSplits) Payload() client.Payload { return s.raw }

// Exercise is one recognized exercise inside a strength set. Garmin sends name as
// null under a known category, which is a valid state rather than an error.
type Exercise struct {
	Category    client.Text   `json:"category"`
	Name        client.Text   `json:"name"`
	Probability client.Number `json:"probability"`
}

// ExerciseSet is one strength set.
//
// It is sensitive: repetitions and weights are health data.
type ExerciseSet struct {
	SetType         client.Text           `json:"setType"`
	StartTime       *string               `json:"startTime"`
	Duration        client.Number         `json:"duration"`
	RepetitionCount client.Number         `json:"repetitionCount"`
	Weight          client.Number         `json:"weight"`
	MessageIndex    client.Number         `json:"messageIndex"`
	WktStepIndex    client.Number         `json:"wktStepIndex"`
	Exercises       client.List[Exercise] `json:"exercises"`
}

// ExerciseSets is the strength-set collection. The sets themselves arrive as an array
// or as a single object, which the List union decoder handles.
type ExerciseSets struct {
	Sets client.List[ExerciseSet] `json:"exerciseSets"`

	raw client.Payload
}

// Payload is the retained raw response.
func (s ExerciseSets) Payload() client.Payload { return s.raw }

// TypedSplits reads the typed splits of one activity.
func (a *ActivityDetails) TypedSplits(
	ctx context.Context, session client.Session, id client.ID,
) (TypedSplits, error) {
	req := readRequest(client.OpGetActivityTypedSplits, client.EndpointActivityTypedSplits,
		activitySegmentPath(id, client.SegmentTypedSplits), nil)
	if err := requireID(req, id); err != nil {
		return TypedSplits{}, err
	}

	var splits TypedSplits
	payload, err := a.req.read(ctx, session, req, &splits)
	if err != nil {
		return TypedSplits{}, err
	}
	splits.raw = payload
	return splits, nil
}

// ExerciseSets reads the strength sets of one activity.
func (a *ActivityDetails) ExerciseSets(
	ctx context.Context, session client.Session, id client.ID,
) (ExerciseSets, error) {
	req := readRequest(client.OpGetActivityExerciseSets, client.EndpointActivityExerciseSet,
		activitySegmentPath(id, client.SegmentExerciseSets), nil)
	if err := requireID(req, id); err != nil {
		return ExerciseSets{}, err
	}

	var sets ExerciseSets
	payload, err := a.req.read(ctx, session, req, &sets)
	if err != nil {
		return ExerciseSets{}, err
	}
	sets.raw = payload
	return sets, nil
}
