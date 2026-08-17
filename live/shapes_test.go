//go:build garminlive

package live

import (
	"maps"
	"strconv"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Result keys that repeat across the shapes below.
const (
	keyCount         = "count"
	keyTruncated     = "truncated"
	keyHasData       = "has_data"
	keyActivities    = "activities"
	keyDroppedFields = "dropped_fields"
	keySampleCount   = "sample_count"
	keySamples       = "samples"
	keyWeeklyData    = "weekly_data"
	keyWeeksAsked    = "weeks_requested"
	keyWeeksReturned = "weeks_returned"
	keyDistance      = "distance"
	keyHeartRate     = "heart_rate"
	keyEntries       = "entries"
)

// answersLocally names the swept tools that reach no Garmin endpoint, with the reason.
// Every other tool must dispatch a request, so a handler that answered from nothing is
// a failure rather than a pass.
func answersLocally() map[string]string {
	return map[string]string{
		tools.ToolGetExerciseTypes: "answers from the strength catalog this process " +
			"loaded before the sweep started: the published catalog is read once at " +
			"start-up, by a dedicated anonymous request that never goes through this " +
			"suite's caller, and the compiled-in subset answers when that read failed",
	}
}

// resultShapes names, per swept tool, the result keys that tool's own answer always
// carries. A key that is also an argument must repeat the value that was sent.
func resultShapes() map[string][]string {
	shapes := map[string][]string{}
	for _, group := range []map[string][]string{
		accountShapes(), activityShapes(), healthShapes(), trainingShapes(),
		nutritionShapes(), challengeShapes(),
		deviceShapes(), womensHealthShapes(), weighInReadShapes(),
	} {
		maps.Copy(shapes, group)
	}
	return shapes
}

// accountShapes are the shapes of the account-scoped and derived-argument answers.
func accountShapes() map[string][]string {
	return map[string][]string{
		// Every field of the two profile models is optional: the account decides
		// which exist, so requiring one would pin this suite to that account.
		tools.ToolGetUserProfile:         {},
		tools.ToolGetUserProfileSettings: {},

		tools.ToolGetFullName:          {"full_name"},
		tools.ToolGetUnitSystem:        {"unit_system"},
		tools.ToolGetPersonalRecord:    {"records", keyCount, keyTruncated},
		tools.ToolCountActivities:      {"total_activities", "note"},
		tools.ToolGetActivities:        {keyActivities, keyCount, "start", "limit"},
		tools.ToolGetActivitiesForDate: {argDate, keyActivities, keyCount},
		tools.ToolGetActivityTypes:     {"activity_types", keyCount, keyTruncated},
		tools.ToolGetDevices:           {"devices", keyCount, keyTruncated},
		tools.ToolGetSleepData:         {argDate, keyHasData},
		tools.ToolGetUserSummary:       {argDate},
		tools.ToolGetExerciseTypes:     {"categories", keyCount, "source", "exercise_count"},
		tools.ToolGetWorkouts:          {keyWorkouts, keyCount, keyTruncated},
		tools.ToolGetCourses:           {"courses", keyCount, keyTruncated},
		tools.ToolGetWorkoutByID:       {argWorkoutID},
		tools.ToolDownloadWorkout:      {"id", "format", "media_type", "bytes", "uri"},
		tools.ToolGetTrainingPlanWorkouts: {
			argDate, "plans", keyWorkouts, keyCount, keyTruncated,
		},
		tools.ToolGetGarminCoachWorkouts: {
			argDate, "plans", keyWorkouts, keyCount, keyTruncated,
		},
		tools.ToolGetActivitiesByDate: {
			keyActivities, keyCount, "date_range", "has_more", "page", "page_size",
		},
		tools.ToolGetScheduledWorkouts: {
			"scheduled_workouts", keyCount, argStartDate, argEndDate, keyTruncated,
		},
		tools.ToolGetPowerDurationCurve: {
			"activity_type", "activities_analyzed", "activities_skipped",
			"bytes_downloaded", "budget_exhausted", "ftp_note", "season_bests",
		},
	}
}

// activityShapes are the shapes of the activity-scoped answers.
func activityShapes() map[string][]string {
	return map[string][]string{
		tools.ToolGetActivityGear:           {argActivityID, "gear", keyCount, keyTruncated},
		tools.ToolGetActivityTypedSplits:    {argActivityID, "splits", keyCount, keyTruncated},
		tools.ToolGetActivitySplits:         {argActivityID, "splits", keyCount, keyTruncated},
		tools.ToolGetActivitySplitSummaries: {argActivityID, "summaries", keyCount, keyTruncated},
		tools.ToolGetActivityHRInZones:      {argActivityID, "zones", keyCount},
		tools.ToolGetActivityPowerInZones:   {argActivityID, "zones", keyCount},
		tools.ToolGetActivityWeather:        {argActivityID, "temperature_unit"},
		tools.ToolGetActivityExerciseSets:   {argActivityID, keySets, keyCount, keyTruncated},
		tools.ToolGetActivity: {
			argActivityID, "timing", keyDistance, keyHeartRate, "energy",
			"run_metrics", "power", "training", "elevation", "feedback",
		},
	}
}

// healthShapes are the shapes of the health-and-wellness answers.
func healthShapes() map[string][]string {
	return map[string][]string{
		tools.ToolGetStats:                    {argDate},
		tools.ToolGetStatsAndBody:             {argDate, "stats", keyDroppedFields},
		tools.ToolGetStepsData:                {argDate, "intervals", keyCount, keyTruncated},
		tools.ToolGetFloors:                   {argDate, "buckets", keyCount, keyTruncated},
		tools.ToolGetStressSummary:            {argDate, keyHasData, "data_points_count"},
		tools.ToolGetTrainingReadiness:        {argDate, keyCount, keyTruncated, keyEntries},
		tools.ToolGetMorningTrainingReadiness: {argDate, keyHasData, "from_wakeup_reset"},
		tools.ToolGetHeartRatesSummary:        {argDate, keyHasData, "data_points_count"},
		tools.ToolGetRestingHeartRateDay:      {argDate, keyHasData},
		tools.ToolGetRespirationSummary:       {argDate, keyHasData},
		tools.ToolGetSleepSummary:             {argDate, keyHasData},
		tools.ToolGetHydrationData:            {argDate, keyHasData},
		tools.ToolGetAllDayStress: {
			argDate, keyHasData, keySampleCount, "usable_sample_count",
		},
		tools.ToolGetStressData: {
			argDate, keyHasData, keySampleCount, keyTruncated, keySamples,
		},
		tools.ToolGetHeartRates: {
			argDate, keyHasData, keySamples, keySampleCount, keyTruncated,
		},
		tools.ToolGetSpO2Data: {
			argDate, keyHasData, "hourly_averages", "hourly_average_count", keyTruncated,
		},
		tools.ToolGetBodyBatteryEvents: {
			argDate, keyCount, keyTruncated, keyDroppedFields, "events",
		},
		tools.ToolGetAllDayEvents: {
			argDate, keyCount, keyTruncated, keyDroppedFields, "events",
		},
		tools.ToolGetLifestyleLoggingData: {
			argDate, keyHasData, "document_json", "document_bytes", keyDroppedFields,
		},
		tools.ToolGetRespirationData: {
			argDate, keyHasData, keySamples, keySampleCount, keyTruncated,
			"hourly_averages", "hourly_average_count", "hourly_truncated",
		},
		tools.ToolGetDailySteps:    {argStartDate, argEndDate, "days", keyCount, keyTruncated},
		tools.ToolGetBodyBattery:   {argStartDate, argEndDate, keyCount, keyTruncated, "days"},
		tools.ToolGetBloodPressure: {argStartDate, argEndDate, "readings", keyCount, keyTruncated},
		tools.ToolGetWeeklySteps: {
			argEndDate, keyWeeksAsked, keyWeeksReturned, keyWeeklyData,
		},
		tools.ToolGetWeeklyIntensityMinutes: {
			argEndDate, keyWeeksAsked, keyWeeksReturned, keyWeeklyData,
		},
		tools.ToolGetWeeklyStress: {
			argEndDate, keyWeeksAsked, keyWeeksReturned, keyTruncated, keyWeeklyData,
		},
		tools.ToolGetBodyComposition: {
			argStartDate, argEndDate, "has_entry_list", keyEntries, "entry_count",
			"entries_truncated", keyDroppedFields,
		},
	}
}

// assertResultCarriesItsShape checks that an answer is this tool's own rather than any
// well-formed object, and that a date it echoes is the date that was requested.
func assertResultCarriesItsShape(t *testing.T, call sweepCall, result map[string]any) {
	t.Helper()

	keys, declared := resultShapes()[call.tool]
	if !declared {
		t.Fatalf("%s is swept but declares no result shape: add one to resultShapes", call.tool)
	}

	for _, key := range keys {
		value, present := result[key]
		if !present {
			t.Errorf("%s returned no %q", call.tool, key)
			continue
		}
		sent, comparable := echoText(call.args[key])
		if got, rendered := echoText(value); comparable && (!rendered || got != sent) {
			t.Errorf("%s echoed %q as something other than the value requested", call.tool, key)
		}
	}
}

// echoText renders an argument or a result value as the text an echo is compared by.
//
// An identifier this suite sends as a decimal string comes back as the canonical
// integer — the schema accepts both forms deliberately — and every JSON number arrives
// as a float64, so both sides are spelled out before they are compared.
func echoText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	default:
		return "", false
	}
}
