package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The names of the two activity lifecycle tools.
//
// create_manual_activity is the upstream compatibility name. delete_activity has no
// entry in the pinned manifest — the pinned upstream surface exposes no activity
// delete at all — so it carries the name python-garminconnect uses for the same
// endpoint, and it is an addition rather than a port.
const (
	ToolCreateManualActivity = "create_manual_activity"
	ToolDeleteActivity       = "delete_activity"
)

// createManualActivityInput is the manual-activity argument set.
//
// It takes a calendar date and a clock time rather than a timestamp, because that is
// what the manifest declares and because the pair is unambiguous once the timezone
// is validated.
type createManualActivityInput struct {
	TypeKey         string   `json:"type_key" jsonschema:"the Garmin activity-type key"`
	Date            string   `json:"date" jsonschema:"the activity date in YYYY-MM-DD form"`
	DurationMinutes int      `json:"duration_minutes" jsonschema:"the activity duration in minutes"`
	StartTime       string   `json:"start_time" jsonschema:"the local start time in HH:MM form"`
	ActivityName    string   `json:"activity_name" jsonschema:"the activity title"`
	DistanceKM      *float64 `json:"distance_km" jsonschema:"the distance covered in kilometres"`
	TimeZone        string   `json:"time_zone" jsonschema:"the IANA timezone of the activity"`
}

func createManualActivityContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCreateManualActivity,
			Title: "Log a manual activity",
			Description: "log one activity that was done without a watch. It creates a new " +
				"record every time it is called, so a repeat after a transport failure " +
				"creates a second activity: read the activity list before calling again",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(manualActivityProperties()...),
	}
}

// manualActivityProperties declares the manual-activity arguments, with the defaults
// the manifest states.
func manualActivityProperties() []Property {
	return []Property{
		{
			Name:        "type_key",
			Types:       []string{typeString},
			Description: `the Garmin activity-type key, for example "yoga"`,
			MaxLength:   new(maxActivityTypeArgumentLen),
			Required:    true,
		},
		calendarDateProperty("date", "the activity date in YYYY-MM-DD form"),
		{
			Name:        "duration_minutes",
			Types:       []string{typeInteger},
			Description: "the activity duration in minutes",
			Minimum:     bound(1),
			Maximum:     bound(maxManualDurationMinutes),
			Required:    true,
		},
		{
			Name:        argNameStartTime,
			Types:       []string{typeString},
			Description: "the local start time on the 24-hour clock, HH:MM",
			Pattern:     `^\d{2}:\d{2}$`,
			MaxLength:   new(maxTimeOfDayLen),
			Default:     defaultManualStartTime,
		},
		{
			Name:        argNameActivityName,
			Types:       []string{typeString},
			Description: "the activity title; empty falls back to the activity-type key",
			MaxLength:   new(maxNameArgumentLen),
			Default:     "",
		},
		{
			Name:        "distance_km",
			Types:       []string{typeNumber},
			Description: "the distance covered in kilometres; zero for a non-distance activity",
			Minimum:     bound(0),
			Maximum:     bound(maxDistanceKM),
			Default:     0.0,
		},
		{
			Name:        "time_zone",
			Types:       []string{typeString},
			Description: `the IANA timezone of the activity, for example "Europe/Paris"`,
			MaxLength:   new(maxTimeZoneArgumentLen),
			Default:     defaultManualTimeZone,
		},
	}
}

// A CreatedActivityResult reports the activity a create produced.
type CreatedActivityResult struct {
	ActivityID int64 `json:"activity_id" jsonschema:"the identifier Garmin assigned"`
}

// LogValue reports that an activity was created, never what was created.
func (r CreatedActivityResult) LogValue() slog.Value {
	return shape("createdActivity")
}

func registerCreateManualActivity(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in createManualActivityInput) (
		*mcp.CallToolResult, CreatedActivityResult, error,
	) {
		activity, err := buildManualActivity(in)
		if err != nil {
			return nil, CreatedActivityResult{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, CreatedActivityResult{}, err
		}

		created, err := svc.writes.CreateManual(ctx, session, activity)
		if err != nil {
			return nil, CreatedActivityResult{}, fail(err)
		}
		id, err := created.ID()
		if err != nil {
			return nil, CreatedActivityResult{}, fail(err)
		}
		return nil, CreatedActivityResult{ActivityID: id.Int64()}, nil
	}
	return mcpserver.AddTool(registry, createManualActivityContract().Registration(), handler)
}

// buildManualActivity validates every argument and converts the units once, here.
func buildManualActivity(in createManualActivityInput) (api.ManualActivity, error) {
	typeKey, err := parseActivityTypeFilter(in.TypeKey)
	if err != nil {
		return api.ManualActivity{}, err
	}
	if typeKey.IsZero() {
		return api.ManualActivity{}, invalidArgument("type_key must not be empty")
	}
	date, err := parseCalendarDate("date", in.Date)
	if err != nil {
		return api.ManualActivity{}, err
	}
	if err := inRange("duration_minutes",
		float64(in.DurationMinutes), 1, maxManualDurationMinutes); err != nil {
		return api.ManualActivity{}, err
	}
	name, err := parseText(argNameActivityName, in.ActivityName, maxNameArgumentLen)
	if err != nil {
		return api.ManualActivity{}, err
	}
	distance, err := manualDistance(in.DistanceKM)
	if err != nil {
		return api.ManualActivity{}, err
	}
	start, zone, err := manualStart(in, date)
	if err != nil {
		return api.ManualActivity{}, err
	}
	return api.NewManualActivity(
		typeKey.String(), start, zone, name, distance, in.DurationMinutes), nil
}

// manualStart resolves the timezone and the clock time into the local timestamp form
// Garmin's activity endpoints store.
func manualStart(in createManualActivityInput, date client.Date) (string, string, error) {
	zone, location, err := parseTimeZone(
		"time_zone", optionalTextArg(in.TimeZone, defaultManualTimeZone))
	if err != nil {
		return "", "", err
	}
	hour, minute, err := parseTimeOfDay(
		argNameStartTime, optionalTextArg(in.StartTime, defaultManualStartTime))
	if err != nil {
		return "", "", err
	}
	start, _ := localStart(date, hour, minute, location)
	return start, zone, nil
}

// manualDistance applies the manifest default and the declared bound.
func manualDistance(value *float64) (float64, error) {
	if value == nil {
		return 0, nil
	}
	if err := inRange("distance_km", *value, 0, maxDistanceKM); err != nil {
		return 0, err
	}
	return *value, nil
}

// deleteActivityInput is the delete argument set.
type deleteActivityInput struct {
	ActivityID any `json:"activity_id" jsonschema:"the Garmin activity identifier to delete"`
}

// A DeletionResult reports one removal.
type DeletionResult struct {
	ID      int64 `json:"id" jsonschema:"the record that was removed"`
	Deleted bool  `json:"deleted" jsonschema:"whether Garmin accepted the removal"`
	Status  int   `json:"status" jsonschema:"the HTTP status Garmin answered with"`
}

// LogValue reports that a removal happened, never which record it named.
func (r DeletionResult) LogValue() slog.Value {
	return shape("deletion", slog.Bool("deleted", r.Deleted), slog.Int("status", r.Status))
}

func deleteActivityContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDeleteActivity,
			Title: "Delete an activity",
			Description: "permanently remove one activity from Garmin Connect, along with " +
				"everything recorded against it. It cannot be undone and it requires " +
				"confirmation",
			Tier:        policy.TierDestructive,
			Category:    categoryHealth,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(activityIDProperty()),
	}
}

func registerDeleteActivity(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deleteActivityInput) (
		*mcp.CallToolResult, DeletionResult, error,
	) {
		write, err := svc.resolveActivityWrite(ctx, in.ActivityID)
		if err != nil {
			return nil, DeletionResult{}, err
		}

		result, err := svc.writes.Delete(ctx, write.session, write.id)
		if err != nil {
			return nil, DeletionResult{}, fail(err)
		}
		return nil, DeletionResult{
			ID: write.id.Int64(), Deleted: true, Status: result.Status,
		}, nil
	}
	return mcpserver.AddTool(registry, deleteActivityContract().Registration(), handler)
}
