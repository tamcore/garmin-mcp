package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// StartTimeLayout is the local timestamp form Garmin's activity endpoints use.
// Source: the "2023-12-02T10:00:00.000" pattern documented on
// create_manual_activity.
const StartTimeLayout = "2006-01-02T15:04:05.000"

// MaxManualDurationSeconds bounds a manually created activity at seven days, so
// a mistyped duration cannot create a record Garmin folds into a week of
// statistics.
const MaxManualDurationSeconds = 7 * 24 * 60 * 60

// MaxTimeZoneLen bounds the timezone name a caller may supply.
const MaxTimeZoneLen = 64

// metersPerKilometer converts the kilometers a caller thinks in into the meters
// Garmin stores. Source: the distance_km * 1000 of create_manual_activity.
const metersPerKilometer = 1000

// ManualActivity is the strict request model for a manually created activity.
//
// Source: create_manual_activity, which posts a private activity carrying an
// activity type, a local start timestamp, a timezone, a distance and a duration.
type ManualActivity struct {
	// TypeKey is the Garmin activity-type key, for example "resort_skiing".
	TypeKey string
	// StartLocal is the local start timestamp in StartTimeLayout form.
	StartLocal string
	// TimeZone is the IANA timezone of the activity, for example "Europe/Paris".
	TimeZone string
	// Name is the activity title. It may be empty.
	Name string
	// DistanceMeters is the distance covered. Zero omits it.
	DistanceMeters float64
	// DurationSeconds is the elapsed duration. It must be positive.
	DurationSeconds int
}

// NewManualActivity builds a manual activity from the kilometers and minutes a
// caller thinks in, so the unit conversion happens once, here, rather than in
// every caller.
func NewManualActivity(
	typeKey, startLocal, timeZone, name string, distanceKM float64, durationMinutes int,
) ManualActivity {
	return ManualActivity{
		TypeKey:         typeKey,
		StartLocal:      startLocal,
		TimeZone:        timeZone,
		Name:            name,
		DistanceMeters:  distanceKM * metersPerKilometer,
		DurationSeconds: durationMinutes * 60,
	}
}

// validate reports whether the manual activity may be dispatched.
func (m ManualActivity) validate(req client.Request) (ManualActivity, error) {
	key, present, err := parseLowerToken(m.TypeKey, "activity type key")
	if err != nil || !present {
		return ManualActivity{}, invalid(req, fmt.Errorf(
			"%w: a manual activity needs an activity type key", client.ErrValidation))
	}
	if _, err := time.Parse(StartTimeLayout, m.StartLocal); err != nil {
		return ManualActivity{}, invalid(req, fmt.Errorf(
			"%w: start time must be in 2006-01-02T15:04:05.000 form", client.ErrValidation))
	}
	if err := validateManualBounds(req, m); err != nil {
		return ManualActivity{}, err
	}
	return ManualActivity{
		TypeKey:         key,
		StartLocal:      m.StartLocal,
		TimeZone:        strings.TrimSpace(m.TimeZone),
		Name:            m.Name,
		DistanceMeters:  m.DistanceMeters,
		DurationSeconds: m.DurationSeconds,
	}, nil
}

// validateManualBounds checks the numeric and textual bounds of a manual
// activity.
func validateManualBounds(req client.Request, m ManualActivity) error {
	zone := strings.TrimSpace(m.TimeZone)
	switch {
	case zone == "" || len(zone) > MaxTimeZoneLen || hasControlRune(zone):
		return invalid(req, fmt.Errorf("%w: a manual activity needs a valid timezone name",
			client.ErrValidation))
	case m.DurationSeconds <= 0 || m.DurationSeconds > MaxManualDurationSeconds:
		return invalid(req, fmt.Errorf("%w: duration must be positive and under seven days",
			client.ErrValidation))
	case m.DistanceMeters < 0:
		return invalid(req, fmt.Errorf("%w: distance must not be negative",
			client.ErrValidation))
	case len(m.Name) > MaxTextLen || hasControlRune(m.Name):
		return invalid(req, fmt.Errorf("%w: activity name is too long or has control characters",
			client.ErrValidation))
	}
	return nil
}

// manualBody is the create payload. Source: the dict create_manual_activity
// posts — a private access rule, an auto-calculated calorie flag and a summary
// carrying the local start, the distance in meters and the duration in seconds.
type manualBody struct {
	ActivityType  keyDTO      `json:"activityTypeDTO"`
	AccessControl accessDTO   `json:"accessControlRuleDTO"`
	TimeZoneUnit  zoneDTO     `json:"timeZoneUnitDTO"`
	ActivityName  string      `json:"activityName"`
	Metadata      metadataDTO `json:"metadataDTO"`
	Summary       summaryDTO  `json:"summaryDTO"`
}

type keyDTO struct {
	TypeKey string `json:"typeKey"`
}

type accessDTO struct {
	TypeID  int    `json:"typeId"`
	TypeKey string `json:"typeKey"`
}

type zoneDTO struct {
	UnitKey string `json:"unitKey"`
}

type metadataDTO struct {
	AutoCalcCalories bool `json:"autoCalcCalories"`
}

type summaryDTO struct {
	StartTimeLocal string  `json:"startTimeLocal"`
	Distance       float64 `json:"distance"`
	Duration       int     `json:"duration"`
}

// privateAccessRule is Garmin's private access-control rule. Source: the
// {"typeId": 2, "typeKey": "private"} of create_manual_activity. A created
// activity is private, deliberately and unconditionally: a tool call must not
// publish a person's outing.
var privateAccessRule = accessDTO{TypeID: 2, TypeKey: "private"}

// manualActivityBody renders the validated model as the create payload.
func manualActivityBody(m ManualActivity) manualBody {
	return manualBody{
		ActivityType:  keyDTO{TypeKey: m.TypeKey},
		AccessControl: privateAccessRule,
		TimeZoneUnit:  zoneDTO{UnitKey: m.TimeZone},
		ActivityName:  m.Name,
		Metadata:      metadataDTO{AutoCalcCalories: true},
		Summary: summaryDTO{
			StartTimeLocal: m.StartLocal,
			Distance:       m.DistanceMeters,
			Duration:       m.DurationSeconds,
		},
	}
}

// CreatedActivity is what Garmin answers a create with. The identifier is a
// union decoder because Garmin sends it as a number and as a string depending on
// the deployment, and an unknown field never fails the response.
type CreatedActivity struct {
	ActivityID client.Number `json:"activityId"`

	raw client.Payload
}

// Payload is the retained raw response.
func (c CreatedActivity) Payload() client.Payload { return c.raw }

// ID reports the created activity's validated identifier.
func (c CreatedActivity) ID() (client.ID, error) {
	value, ok := c.ActivityID.Int64()
	if !ok {
		return client.ID{}, fmt.Errorf("%w: the create response carried no activity id",
			client.ErrMalformedPayload)
	}
	return client.NewID(value)
}

// CreateManual creates one private activity from a few basic parameters.
//
// It is EffectUnsafeWrite: a repeat creates a second activity, so the request
// layer never retries it, and a caller that saw a transport failure must look
// before it writes again.
func (a *ActivityWrites) CreateManual(
	ctx context.Context, session client.Session, activity ManualActivity,
) (CreatedActivity, error) {
	req := writeRequest(client.OpCreateManualActivity, client.EndpointActivity,
		http.MethodPost, client.PathActivityPrefix, client.EffectUnsafeWrite)

	valid, err := activity.validate(req)
	if err != nil {
		return CreatedActivity{}, err
	}
	body, err := jsonBody(req, manualActivityBody(valid))
	if err != nil {
		return CreatedActivity{}, err
	}
	req.Body = body

	var created CreatedActivity
	payload, err := a.req.write(ctx, session, req, &created)
	if err != nil {
		return CreatedActivity{}, err
	}
	created.raw = payload
	return created, nil
}
