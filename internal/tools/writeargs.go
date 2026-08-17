package tools

import (
	"strconv"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Argument bounds the write surface enforces before anything is dispatched. Each one
// is checked in the handler from the same constant the schema declares, so the
// published bound and the enforced bound cannot drift.
const (
	// maxNameArgumentLen bounds a free-text name a caller may write.
	maxNameArgumentLen = 256

	// maxDescriptionArgumentLen bounds a free-text description.
	maxDescriptionArgumentLen = api.MaxTextLen

	// maxEventTypeArgumentLen bounds an event-type key.
	maxEventTypeArgumentLen = 32

	// maxGearUUIDLen is the length of a canonical hyphenated UUID.
	maxGearUUIDLen = 36

	// maxTimeZoneArgumentLen bounds an IANA timezone name.
	maxTimeZoneArgumentLen = api.MaxTimeZoneLen

	// maxTimeOfDayLen is the length of an HH:MM clock time.
	maxTimeOfDayLen = 5

	// maxInstantLen bounds an RFC 3339 timestamp argument.
	maxInstantLen = 35

	// maxExerciseKeyLen bounds an exercise or category key.
	maxExerciseKeyLen = api.MaxExerciseKeyLen
)

// Manual-activity, builder and strength bounds.
const (
	// maxManualDurationMinutes bounds a manually logged activity at seven days,
	// which is the same ceiling the API layer enforces in seconds.
	maxManualDurationMinutes = api.MaxManualDurationSeconds / 60

	// maxDistanceKM bounds a manually logged distance.
	maxDistanceKM = 1000.0

	// maxIntervalSeconds bounds one workout interval at four hours.
	maxIntervalSeconds = 4 * 60 * 60

	// maxBlockMinutes bounds a warmup, cooldown or steady block.
	maxBlockMinutes = 300

	// maxRepeats bounds the iterations of a built repeat group.
	maxRepeats = 100

	// minHeartRate and maxHeartRate bound a heart-rate argument in bpm.
	minHeartRate = 30
	maxHeartRate = 250

	// maxRepetitions bounds the repetition count of one strength step or set.
	maxRepetitions = api.MaxRepetitions

	// maxWeightGrams bounds an external weight.
	maxWeightGrams = api.MaxWeightGrams

	// maxRestSeconds bounds a rest between sets.
	maxRestSeconds = 3600

	// maxStrengthExercises bounds how many exercises one built workout carries.
	maxStrengthExercises = 50

	// maxStrengthSets bounds a caller-supplied strength set list.
	maxStrengthSets = api.MaxStrengthSets
)

// Argument names shared by more than one tool's declaration or handler. Naming them
// once is what keeps a declared property and the handler that validates it from
// drifting to different spellings.
const (
	argNameName            = "name"
	argNameSets            = "sets"
	argNameStartTime       = "start_time"
	argNameCategory        = "category"
	argNameReps            = "reps"
	argNameRestSeconds     = "rest_seconds"
	argNameDurationSeconds = "duration_seconds"
	argNameRepetitions     = "repetitions"
	argNameWeightGrams     = "weight_grams"
	argNameExerciseName    = "exercise_name"
	argNameKind            = "kind"
	argNameFormat          = "format"
	argNameWorkoutID       = "workout_id"
	argNameCalendarDate    = "calendar_date"
	argNameActivityName    = "activity_name"
	argNameRunSeconds      = "run_seconds"
	argNameWarmupMin       = "warmup_min"
	argNameCooldownMin     = "cooldown_min"
	argNameHRMin           = "hr_min"
	argNameHRMax           = "hr_max"
)

// descExerciseCategory is the one description every category argument carries.
const descExerciseCategory = "Garmin's exercise category, from get_exercise_types"

// jsonNull is the encoded form of an absent JSON value.
const jsonNull = "null"

// Argument defaults the manifest states.
const (
	// defaultManualStartTime is the default start clock time of a manual activity.
	defaultManualStartTime = "09:00"

	// defaultManualTimeZone is the default timezone of a manual activity.
	defaultManualTimeZone = "UTC"

	// defaultHeartRateZone is the default target zone of the run builders.
	defaultHeartRateZone = "Z3"

	// defaultDownloadFormat is the default activity download format.
	defaultDownloadFormat = "fit"
)

// parseText validates a free-text write argument. It refuses an over-long value and
// a value carrying a control character, because either would reach Garmin, a log
// sink or a client verbatim.
func parseText(field, value string, limit int) (string, error) {
	if len(value) > limit {
		return "", invalidArgument(
			field + " must not exceed " + strconv.Itoa(limit) + " characters")
	}
	for _, r := range value {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			return "", invalidArgument(field + " must not contain control characters")
		}
	}
	return value, nil
}

// parseXMLDocument validates a caller-supplied XML document argument, such as
// upload_course's gpx_content. It is parseText plus one allowance: \r is
// accepted alongside \n and \t. A GPX file exported on Windows is
// CRLF-terminated, and parseText's own control-character refusal — correct
// for a free-text name or note, which has no reason to carry a line ending
// at all — would otherwise refuse the majority of GPX files a caller is
// likely to have on hand before a single byte reaches Garmin.
func parseXMLDocument(field, value string, limit int) (string, error) {
	if len(value) > limit {
		return "", invalidArgument(
			field + " must not exceed " + strconv.Itoa(limit) + " characters")
	}
	for _, r := range value {
		if (r < 0x20 && r != '\n' && r != '\t' && r != '\r') || r == 0x7f {
			return "", invalidArgument(field + " must not contain control characters")
		}
	}
	return value, nil
}

// parseRequiredText is parseText for an argument that may not be empty.
func parseRequiredText(field, value string, limit int) (string, error) {
	text, err := parseText(field, value, limit)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", invalidArgument(field + " must not be empty")
	}
	return text, nil
}

// parseGearIdentifier validates a gear UUID. Only a parsed value reaches a URL path.
func parseGearIdentifier(value string) (api.GearUUID, error) {
	if len(value) > maxGearUUIDLen {
		return api.GearUUID{}, invalidArgument("gear_uuid is not a canonical UUID")
	}
	gear, err := api.ParseGearUUID(value)
	if err != nil {
		return api.GearUUID{}, invalidArgument(
			"gear_uuid must be a canonical hyphenated UUID from get_gear")
	}
	return gear, nil
}

// parseTimeZone validates an IANA timezone name and returns both the name and the
// loaded location, so a caller's timezone is proven to exist before any write.
func parseTimeZone(field, value string) (string, *time.Location, error) {
	if len(value) > maxTimeZoneArgumentLen {
		return "", nil, invalidArgument(field + " is too long to be an IANA timezone")
	}
	location, err := time.LoadLocation(value)
	if err != nil {
		return "", nil, invalidArgument(
			field + ` must be an IANA timezone name such as "Europe/Paris"`)
	}
	return value, location, nil
}

// parseTimeOfDay validates an HH:MM clock time on the 24-hour clock.
func parseTimeOfDay(field, value string) (int, int, error) {
	if len(value) != maxTimeOfDayLen {
		return 0, 0, invalidArgument(field + " must be exactly five characters, HH:MM")
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, 0, invalidArgument(field + " must be a 24-hour clock time in HH:MM form")
	}
	return parsed.Hour(), parsed.Minute(), nil
}

// parseInstant validates an absolute RFC 3339 timestamp. An absolute instant is the
// only form a strength set may be placed at, because a local time without an offset
// is ambiguous twice a year.
func parseInstant(field, value string) (time.Time, error) {
	if len(value) > maxInstantLen {
		return time.Time{}, invalidArgument(field + " is too long to be an RFC 3339 timestamp")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, invalidArgument(
			field + ` must be an RFC 3339 timestamp such as "2026-01-31T06:12:00Z"`)
	}
	return parsed.UTC(), nil
}

// localStart renders a calendar date and a clock time in the local timestamp form
// Garmin's activity endpoints store, and reports the same instant absolutely.
func localStart(date client.Date, hour, minute int, location *time.Location) (string, time.Time) {
	day := date.Time()
	local := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, location)
	return local.Format(api.StartTimeLayout), local.UTC()
}

// boundedCount refuses a batch or list argument that is empty or over its bound. It
// is the one place a batch tool's fan-out is limited, and every batch tool uses it.
func boundedCount(field string, count, limit int) error {
	switch {
	case count == 0:
		return invalidArgument(field + " must name at least one item")
	case count > limit:
		return invalidArgument(
			field + " must not name more than " + strconv.Itoa(limit) + " items")
	}
	return nil
}

// inRange refuses a numeric argument outside its declared bounds.
func inRange(field string, value, low, high float64) error {
	if value < low || value > high {
		return invalidArgument(field + " must be between " +
			formatBound(low) + " and " + formatBound(high))
	}
	return nil
}

func formatBound(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// optionalTextArg applies a default to an absent or empty optional text argument.
func optionalTextArg(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
