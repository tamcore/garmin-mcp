package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetGarminCoachWorkouts is the upstream compatibility name of the Garmin Coach
// read. It is the preferred name for the plan window; get_training_plan_workouts is
// the legacy name for the same data, and upstream keeps both.
const ToolGetGarminCoachWorkouts = "get_garmin_coach_workouts"

func getGarminCoachWorkoutsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetGarminCoachWorkouts,
			Title: "Get the Garmin Coach workouts",
			Description: "read the active Garmin Coach or training-plan workouts around one " +
				"date, with each entry's date, identifiers, sport, planned duration, " +
				"completion status and Garmin's own intent label. Garmin generates the " +
				"window itself and it is typically the current week, so a later date can " +
				"return nothing while a plan is still active. The count includes rest days. " +
				"Adaptive plans identify a workout by UUID and other plan families by " +
				"numeric id; pass whichever one is present to get_workout_by_id. This is " +
				"the preferred tool for Garmin Coach requests, and the legacy " +
				"get_training_plan_workouts returns the same data, so do not call both. " +
				"Garmin's standalone Daily Suggested Workouts are generated on the device " +
				"and no Garmin Connect endpoint serves that schedule, so they are not " +
				"reported here and are never synthesized",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(calendarDateProperty(argNameCalendarDate,
			"the reference day whose Garmin Coach window to read, in YYYY-MM-DD form")),
	}
}

func registerGetGarminCoachWorkouts(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in trainingPlanInput) (
		*mcp.CallToolResult, TrainingPlanWindow, error,
	) {
		window, err := readTrainingPlanWindow(ctx, svc, in.CalendarDate)
		if err != nil {
			return nil, TrainingPlanWindow{}, err
		}
		return nil, window, nil
	}
	return mcpserver.AddTool(registry, getGarminCoachWorkoutsContract().Registration(), handler)
}

// readTrainingPlanWindow validates the date first, so a malformed argument costs no
// Garmin call, then reads the plan window for the request's own principal and returns
// it under this package's entry bound.
//
// It is the whole body of the Garmin Coach read, kept out of the handler so the read
// can be exercised without a live MCP session. get_training_plan_workouts serves the
// identical window and should be collapsed onto this helper the next time
// calendarreads.go is touched.
func readTrainingPlanWindow(
	ctx context.Context, svc *service, calendarDate string,
) (TrainingPlanWindow, error) {
	date, err := parseCalendarDate(argNameCalendarDate, calendarDate)
	if err != nil {
		return TrainingPlanWindow{}, err
	}
	session, err := svc.session(ctx)
	if err != nil {
		return TrainingPlanWindow{}, err
	}

	plans, err := svc.calendar.TrainingPlanWorkouts(ctx, session, date)
	if err != nil {
		return TrainingPlanWindow{}, fail(err)
	}
	return newTrainingPlanWindow(date.String(), plans), nil
}
