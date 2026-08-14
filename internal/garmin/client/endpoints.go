package client

import "github.com/tamcore/garmin-mcp/internal/garmin/protocol"

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
	// SegmentExerciseSets is the per-activity strength-set segment.
	// Source: get_activity_exercise_sets.
	SegmentExerciseSets = "exerciseSets"
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
	EndpointDailySleep          = Endpoint("connectapi.wellness.daily_sleep")
	EndpointUserSummary         = Endpoint("connectapi.usersummary.daily")
	EndpointDevices             = Endpoint("connectapi.device.registered_devices")
	EndpointActivityTypedSplits = Endpoint("connectapi.activity.typed_splits")
	EndpointActivityExerciseSet = Endpoint("connectapi.activity.exercise_sets")
)

var knownEndpoints = [...]Endpoint{
	EndpointSocialProfile,
	EndpointUserSettings,
	EndpointUserProfileSettings,
	EndpointActivitySearch,
	EndpointDailySleep,
	EndpointUserSummary,
	EndpointDevices,
	EndpointActivityTypedSplits,
	EndpointActivityExerciseSet,
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
	for _, known := range knownEndpoints {
		if e == known {
			return true
		}
	}
	return false
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
	OpGetDailySleep           = Op("get_daily_sleep")
	OpGetUserSummary          = Op("get_user_summary")
	OpListDevices             = Op("list_devices")
	OpGetActivityTypedSplits  = Op("get_activity_typed_splits")
	OpGetActivityExerciseSets = Op("get_activity_exercise_sets")
)

var knownOps = [...]Op{
	OpGetSocialProfile,
	OpGetUserSettings,
	OpGetUserProfileSettings,
	OpListActivities,
	OpListActivitiesByDate,
	OpGetDailySleep,
	OpGetUserSummary,
	OpListDevices,
	OpGetActivityTypedSplits,
	OpGetActivityExerciseSets,
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
	for _, known := range knownOps {
		if o == known {
			return true
		}
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
