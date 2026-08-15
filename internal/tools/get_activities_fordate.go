package tools

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetActivitiesForDate is the upstream compatibility name of the single-day read.
const ToolGetActivitiesForDate = "get_activities_fordate"

// defaultMaxDailyActivities bounds how many activities one day may return. A day
// holds a handful of sessions, so a longer answer is drift rather than a busy day,
// and it is refused rather than truncated: a partial day would read as a whole one.
const defaultMaxDailyActivities = 200

// DailyActivity is one activity of a single day.
//
// It carries the measurements the day document holds and no coordinate, which is the
// same rule every activity result on this surface follows.
type DailyActivity struct {
	ActivityID               *int64   `json:"activity_id,omitempty" jsonschema:"the Garmin activity identifier"`
	Name                     *string  `json:"activity_name,omitempty" jsonschema:"the activity name"`
	ActivityType             *string  `json:"activity_type,omitempty" jsonschema:"the activity type key"`
	EventType                *string  `json:"event_type,omitempty" jsonschema:"the event type key, for example race"`
	StartTimeLocal           *string  `json:"start_time_local,omitempty" jsonschema:"the local start time"`
	DistanceMeters           *float64 `json:"distance_meters,omitempty" jsonschema:"the distance in meters"`
	DurationSecs             *float64 `json:"duration_seconds,omitempty" jsonschema:"the timed duration in seconds"`
	Calories                 *float64 `json:"calories,omitempty" jsonschema:"the kilocalories burned"`
	AverageHR                *float64 `json:"average_heart_rate,omitempty" jsonschema:"the average heart rate in bpm"`
	Steps                    *float64 `json:"steps,omitempty" jsonschema:"the steps recorded"`
	LapCount                 *int     `json:"lap_count,omitempty" jsonschema:"how many laps the activity holds"`
	ModerateIntensityMinutes *float64 `json:"moderate_intensity_minutes,omitempty" jsonschema:"the moderate minutes"`
	VigorousIntensityMinutes *float64 `json:"vigorous_intensity_minutes,omitempty" jsonschema:"the vigorous minutes"`
}

// DailyActivityList is every activity recorded on one calendar day.
type DailyActivityList struct {
	Date       string          `json:"date" jsonschema:"the day that was read, YYYY-MM-DD"`
	Activities []DailyActivity `json:"activities" jsonschema:"the activities recorded on that day"`
	Count      int             `json:"count" jsonschema:"how many activities the day holds"`
}

// LogValue reports the day's activity count, never an activity.
func (l DailyActivityList) LogValue() slog.Value {
	return shape("dailyActivityList", slog.Int("activities", len(l.Activities)))
}

// getActivitiesForDateInput is the strict argument set.
type getActivitiesForDateInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getActivitiesForDateContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivitiesForDate,
			Title: "Get activities for a date",
			Description: "read every activity recorded on one calendar day. Garmin answers " +
				"this endpoint with the day's heart-rate series as well; that series is " +
				"dropped and only the activities are returned. A day with no activity " +
				"returns an empty list, which is a normal state. Start coordinates are not " +
				"returned",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetActivitiesForDate registers the tool.
func registerGetActivitiesForDate(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getActivitiesForDateInput) (
		*mcp.CallToolResult, DailyActivityList, error,
	) {
		day, err := parseCalendarDate("date", in.Date)
		if err != nil {
			return nil, DailyActivityList{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DailyActivityList{}, err
		}

		activities, err := svc.activities.ForDate(ctx, session, day)
		if err != nil {
			return nil, DailyActivityList{}, fail(err)
		}
		if len(activities) > defaultMaxDailyActivities {
			return nil, DailyActivityList{}, tooLarge("the day holds more than " +
				strconv.Itoa(defaultMaxDailyActivities) + " activities")
		}
		return nil, newDailyActivityList(day.String(), activities), nil
	}
	return mcpserver.AddTool(registry, getActivitiesForDateContract().Registration(), handler)
}

// newDailyActivityList maps the domain models onto the bounded result.
func newDailyActivityList(date string, activities []api.Activity) DailyActivityList {
	out := make([]DailyActivity, 0, len(activities))
	for _, activity := range activities {
		out = append(out, DailyActivity{
			ActivityID:               activity.ActivityID,
			Name:                     activity.ActivityName,
			ActivityType:             typeKeyOf(activity.ActivityType),
			EventType:                typeKeyOf(activity.EventType),
			StartTimeLocal:           activity.StartTimeLocal,
			DistanceMeters:           optionalFloat(activity.Distance),
			DurationSecs:             optionalFloat(activity.Duration),
			Calories:                 optionalFloat(activity.Calories),
			AverageHR:                optionalFloat(activity.AverageHR),
			Steps:                    optionalFloat(activity.Steps),
			LapCount:                 optionalInt(activity.LapCount),
			ModerateIntensityMinutes: optionalFloat(activity.ModerateIntensityMinutes),
			VigorousIntensityMinutes: optionalFloat(activity.VigorousIntensityMinutes),
		})
	}
	return DailyActivityList{Date: date, Activities: out, Count: len(out)}
}
