package client

import (
	"slices"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// API-tier paths. Source: the URL table built in GarminConnect.__init__ and the
// per-method URLs in python-garminconnect 0.3.10, file garminconnect/__init__.py.
//
// A path is a template with no query string: query parameters are supplied
// separately, so a credential or a date can never be concatenated into a path.
const (
	// PathSocialProfile is the flat profile object that also carries the display
	// name every date-keyed wellness path needs.
	// Source: connectapi("/userprofile-service/socialProfile").
	PathSocialProfile = protocol.PathSocialProfile
	// PathUserSettings is the account settings document, whose userData holds the
	// measurement system. Source: garmin_connect_user_settings_url.
	PathUserSettings = "/userprofile-service/userprofile/user-settings"
	// PathUserProfileSettings is the profile settings document.
	// Source: garmin_connect_userprofile_settings_url.
	PathUserProfileSettings = "/userprofile-service/userprofile/settings"
	// PathActivitySearch is the paginated activity list, filtered by start,
	// limit, activityType, startDate, endDate and sortOrder.
	// Source: garmin_connect_activities.
	PathActivitySearch = "/activitylist-service/activities/search/activities"
	// PathActivitiesCount is the account's total activity count, answered as an
	// object carrying totalCount. Source: garmin_connect_activities_count.
	PathActivitiesCount = "/activitylist-service/activities/count"
	// PathActivitiesForDatePrefix precedes a calendar date in the single-day
	// activity path. The path belongs to the mobile gateway's heart-rate tier and
	// the useful part of its answer is the ActivitiesForDay document, which is why
	// the surrounding heart-rate series is dropped on decode.
	// Source: garmin_connect_activity_fordate.
	PathActivitiesForDatePrefix = "/mobile-gateway/heartRate/forDate"
	// PathDailySleepPrefix is the date-keyed daily sleep summary. The display name
	// is appended as a single escaped path segment.
	// Source: garmin_connect_daily_sleep_url.
	PathDailySleepPrefix = "/wellness-service/wellness/dailySleepData"
	// PathUserSummaryPrefix is the date-keyed daily user summary. The display name
	// is appended as a single escaped path segment.
	// Source: garmin_connect_daily_summary_url.
	PathUserSummaryPrefix = "/usersummary-service/usersummary/daily"
	// PathDevices is the registered-device array.
	// Source: garmin_connect_devices_url.
	PathDevices = "/device-service/deviceregistration/devices"
	// PathActivityPrefix precedes an activity id in every per-activity path.
	// Source: garmin_connect_activity.
	PathActivityPrefix = "/activity-service/activity"
	// SegmentTypedSplits is the per-activity typed-splits segment, whose payload
	// shape varies by activity type. Source: get_activity_typed_splits.
	SegmentTypedSplits = "typedsplits"
	// SegmentExerciseSets is the per-activity strength-set segment. It is read by
	// get_activity_exercise_sets and replaced by set_activity_exercise_sets.
	SegmentExerciseSets = "exerciseSets"
	// SegmentSplits is the per-activity split collection.
	// Source: get_activity_splits.
	SegmentSplits = "splits"
	// SegmentSplitSummaries is the per-activity split summary collection.
	// Source: get_activity_split_summaries.
	SegmentSplitSummaries = "split_summaries"
	// SegmentWeather is the per-activity weather record.
	// Source: get_activity_weather.
	SegmentWeather = "weather"
	// SegmentHRInZones is the per-activity heart-rate time-in-zones record.
	// Source: get_activity_hr_in_timezones.
	SegmentHRInZones = "hrTimeInZones"
	// SegmentPowerInZones is the per-activity power time-in-zones record.
	// Source: get_activity_power_in_timezones.
	SegmentPowerInZones = "powerTimeInZones"

	// PathActivityTypes is the activity-type catalog.
	// Source: garmin_connect_activity_types.
	PathActivityTypes = "/activity-service/activity/activityTypes"
	// PathActivityEventTypes is the event-type catalog. Source: the
	// connectapi("/activity-service/activity/eventTypes") read of
	// set_activity_event_type.
	PathActivityEventTypes = "/activity-service/activity/eventTypes"
	// PathPersonalRecords precedes the display name in the personal-record path.
	// Source: garmin_connect_personal_record_url.
	PathPersonalRecords = "/personalrecord-service/personalrecord/prs"
	// PathGearFilter is the gear list, filtered by activityId or userProfilePk.
	// Source: garmin_connect_gear.
	PathGearFilter = "/gear-service/gear/filterGear"
	// PathGearPrefix precedes the link and unlink segments of a gear write.
	// Source: garmin_connect_gear_baseurl.
	PathGearPrefix = "/gear-service/gear"
	// PathWorkouts is the paginated workout library.
	// Source: f"{garmin_workouts}/workouts".
	PathWorkouts = "/workout-service/workouts"
	// PathWorkoutPrefix precedes a workout id in the per-workout paths.
	// Source: f"{garmin_workouts}/workout".
	PathWorkoutPrefix = "/workout-service/workout"
	// PathWorkoutFITPrefix precedes a workout id in the FIT download path.
	// Source: download_workout.
	PathWorkoutFITPrefix = "/workout-service/workout/FIT"
	// PathWorkoutSchedule precedes a workout id when scheduling and a scheduled
	// workout id when unscheduling. Source: garmin_workouts_schedule_url.
	PathWorkoutSchedule = "/workout-service/schedule"

	// PathActivityOriginalDownload precedes an activity id in the original
	// (zipped FIT) download path. Source: garmin_connect_fit_download.
	PathActivityOriginalDownload = "/download-service/files/activity"
	// PathActivityTCXDownload precedes an activity id in the TCX export path.
	// Source: garmin_connect_tcx_download.
	PathActivityTCXDownload = "/download-service/export/tcx/activity"
	// PathActivityGPXDownload precedes an activity id in the GPX export path.
	// Source: garmin_connect_gpx_download.
	PathActivityGPXDownload = "/download-service/export/gpx/activity"
	// PathActivityKMLDownload precedes an activity id in the KML export path.
	// Source: garmin_connect_kml_download.
	PathActivityKMLDownload = "/download-service/export/kml/activity"
	// PathActivityCSVDownload precedes an activity id in the CSV export path.
	// Source: garmin_connect_csv_download.
	PathActivityCSVDownload = "/download-service/export/csv/activity"
)

// Query parameter names Garmin's API tier expects. Source: the params dicts of
// the corresponding methods in garminconnect/__init__.py.
const (
	QueryStart                 = "start"
	QueryLimit                 = "limit"
	QueryActivityType          = "activityType"
	QueryStartDate             = "startDate"
	QueryEndDate               = "endDate"
	QuerySortOrder             = "sortOrder"
	QueryCalendarDate          = "calendarDate"
	QueryDate                  = "date"
	QueryNonSleepBufferMinutes = "nonSleepBufferMinutes"
	QueryActivityID            = "activityId"
)

// Endpoint is a sanitized endpoint label for logs, metrics and errors.
//
// It mirrors protocol.Endpoint, which is a closed set covering the login and
// token surface only. This package needs labels for the API tier, and the
// protocol package cannot be extended from here, so the same discipline is
// reproduced: only the Endpoint* constants below are labels, and a value built
// from any other string — a URL, a query string, a header — renders as "unknown".
// The one label both packages need is taken from protocol rather than restated.
type Endpoint string

// Sanitized endpoint labels. They never contain a host, a credential or a query
// string.
const (
	// EndpointSocialProfile is protocol's own label for the shared profile call.
	EndpointSocialProfile       = Endpoint(protocol.EndpointSocialProfile)
	EndpointUserSettings        = Endpoint("connectapi.userprofile.user_settings")
	EndpointUserProfileSettings = Endpoint("connectapi.userprofile.settings")
	EndpointActivitySearch      = Endpoint("connectapi.activitylist.search")
	EndpointActivitiesCount     = Endpoint("connectapi.activitylist.count")
	EndpointActivitiesForDate   = Endpoint("connectapi.mobilegateway.activities_for_date")
	EndpointDailySleep          = Endpoint("connectapi.wellness.daily_sleep")
	EndpointUserSummary         = Endpoint("connectapi.usersummary.daily")
	EndpointDevices             = Endpoint("connectapi.device.registered_devices")
	EndpointActivityTypedSplits = Endpoint("connectapi.activity.typed_splits")
	EndpointActivityExerciseSet = Endpoint("connectapi.activity.exercise_sets")

	EndpointActivity               = Endpoint("connectapi.activity.summary")
	EndpointActivityTypes          = Endpoint("connectapi.activity.types")
	EndpointActivityEventTypes     = Endpoint("connectapi.activity.event_types")
	EndpointActivitySplits         = Endpoint("connectapi.activity.splits")
	EndpointActivitySplitSummaries = Endpoint("connectapi.activity.split_summaries")
	EndpointActivityWeather        = Endpoint("connectapi.activity.weather")
	EndpointActivityHRInZones      = Endpoint("connectapi.activity.hr_in_zones")
	EndpointActivityPowerInZones   = Endpoint("connectapi.activity.power_in_zones")
	EndpointActivityDownload       = Endpoint("connectapi.download.activity_file")
	EndpointGearFilter             = Endpoint("connectapi.gear.filter")
	EndpointGearLink               = Endpoint("connectapi.gear.link")
	EndpointGearUnlink             = Endpoint("connectapi.gear.unlink")
	EndpointPersonalRecords        = Endpoint("connectapi.personalrecord.prs")
	EndpointWorkoutList            = Endpoint("connectapi.workout.list")
	EndpointWorkout                = Endpoint("connectapi.workout.item")
	EndpointWorkoutDownload        = Endpoint("connectapi.workout.download")
	EndpointWorkoutSchedule        = Endpoint("connectapi.workout.schedule")
	// EndpointGraphQL is Garmin's GraphQL gateway. One label covers the whole tier
	// because one path does: the root field being queried is a separate sanitized
	// label, GraphQLField, and the Op says which read it serves.
	EndpointGraphQL = Endpoint("connectapi.graphql.gateway")
)

// coreEndpoints are the labels declared here; endpoints_health.go declares the rest and joins both into knownEndpoints.
var coreEndpoints = [...]Endpoint{
	EndpointSocialProfile,
	EndpointUserSettings,
	EndpointUserProfileSettings,
	EndpointActivitySearch,
	EndpointActivitiesCount,
	EndpointActivitiesForDate,
	EndpointDailySleep,
	EndpointUserSummary,
	EndpointDevices,
	EndpointActivityTypedSplits,
	EndpointActivityExerciseSet,
	EndpointActivity,
	EndpointActivityTypes,
	EndpointActivityEventTypes,
	EndpointActivitySplits,
	EndpointActivitySplitSummaries,
	EndpointActivityWeather,
	EndpointActivityHRInZones,
	EndpointActivityPowerInZones,
	EndpointActivityDownload,
	EndpointGearFilter,
	EndpointGearLink,
	EndpointGearUnlink,
	EndpointPersonalRecords,
	EndpointWorkoutList,
	EndpointWorkout,
	EndpointWorkoutDownload,
	EndpointWorkoutSchedule,
	EndpointGraphQL,
}

// labelUnknown is what an unrecognized label renders as, matching protocol.
const labelUnknown = "unknown"

// KnownEndpoints returns a copy of the endpoint labels this package can render.
func KnownEndpoints() []Endpoint {
	out := make([]Endpoint, len(knownEndpoints))
	copy(out, knownEndpoints[:])
	return out
}

// IsKnown reports whether e is one of the package's Endpoint constants.
func (e Endpoint) IsKnown() bool {
	return slices.Contains(knownEndpoints, e)
}

// String returns the label, or "unknown" for a value that is not a package
// constant.
func (e Endpoint) String() string {
	if !e.IsKnown() {
		return labelUnknown
	}
	return string(e)
}

// Op is a sanitized label for the logical operation that failed. Like Endpoint,
// only a recognized constant is rendered.
//
// A protocol.Op is also recognized, so a caller can label an operation this
// package performs on behalf of the login layer without restating its labels.
type Op string

// Sanitized operation labels, one per read this package can perform.
const (
	OpGetSocialProfile        = Op("get_social_profile")
	OpGetUserSettings         = Op("get_user_settings")
	OpGetUserProfileSettings  = Op("get_userprofile_settings")
	OpListActivities          = Op("list_activities")
	OpListActivitiesByDate    = Op("list_activities_by_date")
	OpCountActivities         = Op("count_activities")
	OpListActivitiesForDate   = Op("get_activities_fordate")
	OpGetDailySleep           = Op("get_daily_sleep")
	OpGetUserSummary          = Op("get_user_summary")
	OpListDevices             = Op("list_devices")
	OpGetActivityTypedSplits  = Op("get_activity_typed_splits")
	OpGetActivityExerciseSets = Op("get_activity_exercise_sets")

	OpGetActivity               = Op("get_activity")
	OpGetActivityTypes          = Op("get_activity_types")
	OpGetActivityEventTypes     = Op("get_activity_event_types")
	OpGetActivitySplits         = Op("get_activity_splits")
	OpGetActivitySplitSummaries = Op("get_activity_split_summaries")
	OpGetActivityWeather        = Op("get_activity_weather")
	OpGetActivityHRInZones      = Op("get_activity_hr_in_zones")
	OpGetActivityPowerInZones   = Op("get_activity_power_in_zones")
	OpGetActivityGear           = Op("get_activity_gear")
	OpGetPersonalRecords        = Op("get_personal_records")
	OpDownloadActivityFile      = Op("download_activity_file")
	OpSetActivityName           = Op("set_activity_name")
	OpSetActivityType           = Op("set_activity_type")
	OpSetActivityEventType      = Op("set_activity_event_type")
	OpSetActivityDescription    = Op("set_activity_description")
	OpSetActivityFeel           = Op("set_activity_feel")
	OpSetPerceivedEffort        = Op("set_perceived_effort")
	OpSetActivityExerciseSets   = Op("set_activity_exercise_sets")
	OpAddGearToActivity         = Op("add_gear_to_activity")
	OpRemoveGearFromActivity    = Op("remove_gear_from_activity")
	OpDeleteActivity            = Op("delete_activity")
	OpCreateManualActivity      = Op("create_manual_activity")
	OpCreateStrengthActivity    = Op("create_strength_training_activity")
	OpListWorkouts              = Op("list_workouts")
	OpGetWorkout                = Op("get_workout_by_id")
	OpUploadWorkout             = Op("upload_workout")
	OpUpdateWorkout             = Op("update_workout")
	OpDeleteWorkout             = Op("delete_workout")
	OpScheduleWorkout           = Op("schedule_workout")
	OpUnscheduleWorkout         = Op("unschedule_workout")
	OpDownloadWorkout           = Op("download_workout")
	OpGetScheduledWorkouts      = Op("get_scheduled_workouts")
	OpGetTrainingPlanWorkouts   = Op("get_training_plan_workouts")
)

// coreOps are the operations declared here; endpoints_health.go declares the rest and joins both into knownOps.
var coreOps = [...]Op{
	OpGetSocialProfile,
	OpGetUserSettings,
	OpGetUserProfileSettings,
	OpListActivities,
	OpListActivitiesByDate,
	OpCountActivities,
	OpListActivitiesForDate,
	OpGetDailySleep,
	OpGetUserSummary,
	OpListDevices,
	OpGetActivityTypedSplits,
	OpGetActivityExerciseSets,
	OpGetActivity,
	OpGetActivityTypes,
	OpGetActivityEventTypes,
	OpGetActivitySplits,
	OpGetActivitySplitSummaries,
	OpGetActivityWeather,
	OpGetActivityHRInZones,
	OpGetActivityPowerInZones,
	OpGetActivityGear,
	OpGetPersonalRecords,
	OpDownloadActivityFile,
	OpSetActivityName,
	OpSetActivityType,
	OpSetActivityEventType,
	OpSetActivityDescription,
	OpSetActivityFeel,
	OpSetPerceivedEffort,
	OpSetActivityExerciseSets,
	OpAddGearToActivity,
	OpRemoveGearFromActivity,
	OpDeleteActivity,
	OpCreateManualActivity,
	OpCreateStrengthActivity,
	OpListWorkouts,
	OpGetWorkout,
	OpUploadWorkout,
	OpUpdateWorkout,
	OpDeleteWorkout,
	OpScheduleWorkout,
	OpUnscheduleWorkout,
	OpDownloadWorkout,
	OpGetScheduledWorkouts,
	OpGetTrainingPlanWorkouts,
}

// credentialOps are the protocol operations that carry a password or a one-time
// code. They belong to internal/garmin/auth, not here, but the retry predicate
// names them explicitly so no future caller can make this package replay a
// credential or MFA submission. See Op.IsCredentialSubmission.
var credentialOps = [...]protocol.Op{
	protocol.OpMobileLogin,
	protocol.OpPortalLogin,
	protocol.OpWidgetLogin,
	protocol.OpVerifyMFA,
	protocol.OpRequestMFACode,
}

// KnownOps returns a copy of the operation labels this package defines. The
// protocol operations Op also accepts are deliberately not included: they label
// work another package performs.
func KnownOps() []Op {
	out := make([]Op, len(knownOps))
	copy(out, knownOps[:])
	return out
}

// IsKnown reports whether o is one of this package's Op constants or one of
// protocol's.
func (o Op) IsKnown() bool {
	if slices.Contains(knownOps, o) {
		return true
	}
	return protocol.Op(o).IsKnown()
}

// String returns the label, or "unknown" for a value that is neither this
// package's constant nor protocol's.
func (o Op) String() string {
	if !o.IsKnown() {
		return labelUnknown
	}
	return string(o)
}

// IsCredentialSubmission reports whether o submits a password or a one-time
// code. Such an operation is never retried, whatever else the retry predicate
// concludes.
func (o Op) IsCredentialSubmission() bool {
	for _, credential := range credentialOps {
		if string(o) == string(credential) {
			return true
		}
	}
	return false
}
