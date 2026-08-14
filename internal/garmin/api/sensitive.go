package api

import "log/slog"

// Every model in this package is sensitive: a Garmin response carries health data,
// coordinates and identity at once. The models stay plain, JSON-marshalable structs
// on purpose, because the tool layer returns them to an authorized caller — the
// boundary they must not cross is a log sink.
//
// LogValue is therefore implemented on each of them, reporting shape rather than
// content. It closes the reflective path: slog calls LogValue instead of walking the
// fields. What it cannot close is fmt: %+v on a plain struct prints its fields, so
// printing one of these models is a defect. The material that must survive that too —
// the retained raw payload and the display name — lives behind client.Payload and
// client.DisplayName, which seal it one pointer deeper and carry their own
// alias-stripping leak tests.

// presence renders whether an optional value is present, without revealing it.
func presence(present bool) string {
	if present {
		return "set"
	}
	return "unset"
}

// LogValue reports the shape of the profile, never the identity in it.
func (p SocialProfile) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "socialProfile"),
		slog.String("profileId", presence(p.ProfileID != nil)),
		slog.String("displayName", presence(p.DisplayName != nil)),
		slog.String("fullName", presence(p.FullName != nil)),
		slog.String("location", presence(p.Location != nil)),
		slog.Any("payload", p.raw),
	)
}

// LogValue reports the shape of the settings document.
func (s UserSettings) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "userSettings"),
		slog.String("id", presence(s.ID != nil)),
		slog.String("userData", presence(s.UserData != nil)),
		slog.Any("payload", s.raw),
	)
}

// LogValue reports the shape of the nested settings object.
func (d UserData) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "userData"),
		slog.String("measurementSystem", presence(d.MeasurementSystem.IsSet())),
		slog.String("birthDate", presence(d.BirthDate != nil)),
		slog.String("weight", presence(d.Weight.IsSet())),
	)
}

// LogValue reports the shape of one activity, never its coordinates or heart rate.
func (a Activity) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "activity"),
		slog.String("activityId", presence(a.ActivityID != nil)),
		slog.String("activityName", presence(a.ActivityName != nil)),
		slog.String("startTime", presence(a.StartTimeGMT != nil || a.StartTimeLocal != nil)),
		slog.String("startPosition", presence(a.StartLatitude.IsSet() && a.StartLongitude.IsSet())),
		slog.String("averageHr", presence(a.AverageHR.IsSet())),
	)
}

// LogValue reports the page size, never the activities.
func (p ActivityPage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "activityPage"),
		slog.Int("activities", len(p.Activities)),
		slog.Any("payload", p.raw),
	)
}

// LogValue reports the shape of one day of sleep data.
func (s DailySleep) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "dailySleep"),
		slog.String("summary", presence(s.Summary != nil)),
		slog.Int("sleepLevelsBytes", len(s.SleepLevels)),
		slog.Any("payload", s.raw),
	)
}

// LogValue reports the shape of the nested sleep summary.
func (s SleepSummary) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "sleepSummary"),
		slog.String("calendarDate", presence(s.CalendarDate != nil)),
		slog.String("sleepTimeSeconds", presence(s.SleepTimeSeconds.IsSet())),
	)
}

// LogValue reports the shape of one day of totals.
func (s UserSummary) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "userSummary"),
		slog.String("calendarDate", presence(s.CalendarDate != nil)),
		slog.String("totalSteps", presence(s.TotalSteps.IsSet())),
		slog.String("restingHeartRate", presence(s.RestingHeartRate.IsSet())),
		slog.String("privacyProtected", presence(s.PrivacyProtected != nil)),
		slog.Any("payload", s.raw),
	)
}

// LogValue reports the shape of one device, never its serial number.
func (d Device) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "device"),
		slog.String("deviceId", presence(d.DeviceID.IsSet())),
		slog.String("serialNumber", presence(d.SerialNumber != nil)),
		slog.String("productDisplayName", presence(d.ProductDisplayName != nil)),
	)
}

// LogValue reports the split count, never the splits.
func (s TypedSplits) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "typedSplits"),
		slog.Int("splits", len(s.splits)),
		slog.Any("payload", s.raw),
	)
}

// LogValue reports the shape of one split.
func (s TypedSplit) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "typedSplit"),
		slog.String("type", presence(s.Type.IsSet())),
		slog.String("distance", presence(s.Distance.IsSet())),
		slog.String("averageHr", presence(s.AverageHR.IsSet())),
	)
}

// LogValue reports the set count, never the sets.
func (s ExerciseSets) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "exerciseSets"),
		slog.Int("sets", s.Sets.Len()),
		slog.Any("payload", s.raw),
	)
}

// LogValue reports the shape of one strength set.
func (s ExerciseSet) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "exerciseSet"),
		slog.String("setType", presence(s.SetType.IsSet())),
		slog.Int("exercises", s.Exercises.Len()),
		slog.String("weight", presence(s.Weight.IsSet())),
	)
}

// LogValue reports the shape of one exercise, never the movement it names.
func (e Exercise) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "exercise"),
		slog.String("category", presence(e.Category.IsSet())),
		slog.String("name", presence(e.Name.IsSet())),
	)
}
