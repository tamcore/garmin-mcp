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

// LogValue reports the outcome of a write, never the object it returned.
func (w WriteResult) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "writeResult"),
		slog.Int("status", w.Status),
		slog.Any("payload", w.raw),
	)
}

// LogValue reports whether a create returned an identifier, never which one.
func (c CreatedActivity) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "createdActivity"),
		slog.String("activityId", presence(c.ActivityID.IsSet())),
		slog.Any("payload", c.raw),
	)
}

// LogValue reports the shape of one activity record, never its measurements.
func (a ActivitySummary) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "activitySummary"),
		slog.String("activityId", presence(a.ActivityID.IsSet())),
		slog.String("activityName", presence(a.ActivityName != nil)),
		slog.String("summary", presence(len(a.Summary) > 0)),
		slog.Any("payload", a.raw),
	)
}

// LogValue reports the summary count, never the summaries.
func (s SplitSummaries) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "splitSummaries"),
		slog.Int("summaries", s.Summaries.Len()),
		slog.Any("payload", s.raw),
	)
}

// LogValue reports the shape of one split summary.
func (s SplitSummary) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "splitSummary"),
		slog.String("splitType", presence(s.SplitType.IsSet())),
		slog.String("distance", presence(s.Distance.IsSet())),
	)
}

// LogValue reports the shape of a weather record, never its coordinates.
func (w Weather) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "weather"),
		slog.String("temp", presence(w.Temp.IsSet())),
		slog.String("position", presence(w.Latitude.IsSet() && w.Longitude.IsSet())),
		slog.Any("payload", w.raw),
	)
}

// LogValue reports the shape of one zone bucket, never the time in it.
func (z ZoneBucket) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "zoneBucket"),
		slog.String("zoneNumber", presence(z.ZoneNumber.IsSet())),
		slog.String("secsInZone", presence(z.SecsInZone.IsSet())),
	)
}

// LogValue reports the shape of one catalog row. A type key is not sensitive,
// but the model is logged through the same discipline as every other.
func (c CatalogEntry) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "catalogEntry"),
		slog.String("typeId", presence(c.TypeID.IsSet())),
		slog.String("typeKey", presence(c.TypeKey.IsSet())),
	)
}

// LogValue reports the shape of one gear item, never the equipment it names.
func (g GearItem) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "gearItem"),
		slog.String("uuid", presence(g.UUID != nil)),
		slog.String("displayName", presence(g.DisplayName != nil)),
		slog.String("gearType", presence(g.GearTypeName != nil)),
	)
}

// LogValue reports the shape of one workout entry, never the training in it.
func (w WorkoutSummary) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "workoutSummary"),
		slog.String("workoutId", presence(w.WorkoutID.IsSet())),
		slog.String("workoutName", presence(w.WorkoutName != nil)),
	)
}

// LogValue reports the shape of one workout, never its steps.
func (w Workout) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "workout"),
		slog.String("workoutId", presence(w.WorkoutID.IsSet())),
		slog.String("workoutName", presence(w.WorkoutName != nil)),
		slog.Int("segmentBytes", len(w.Segments)),
		slog.Any("payload", w.raw),
	)
}

// LogValue reports that a workout was saved, never what was saved.
func (w SavedWorkout) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "savedWorkout"),
		slog.String("workoutId", presence(w.WorkoutID.IsSet())),
		slog.String("workoutName", presence(w.WorkoutName != nil)),
		slog.Any("payload", w.raw),
	)
}

// LogValue reports the shape of the profile settings, never the identity in it.
func (p ProfileSettings) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "profileSettings"),
		slog.String("displayName", presence(p.DisplayName != nil)),
		slog.String("fullName", presence(p.FullName != nil)),
		slog.String("location", presence(p.Location != nil)),
		slog.Any("payload", p.raw),
	)
}

// LogValue reports the shape of one personal record, never the performance.
func (p PersonalRecord) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "personalRecord"),
		slog.String("typeId", presence(p.TypeID.IsSet())),
		slog.String("value", presence(p.Value.IsSet())),
	)
}

// LogValue reports the shape of one strength set, never the repetitions or the
// weight, which describe a person's body.
func (s StrengthSet) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "strengthSet"),
		slog.String("kind", string(s.Kind)),
		slog.String("start", presence(!s.Start.IsZero())),
		slog.String("repetitions", presence(s.Repetitions > 0)),
		slog.String("weight", presence(s.WeightGrams > 0)),
		slog.String("exercise", presence(s.ExerciseName != "")),
	)
}

// LogValue reports that a strength activity was created and verified, never its
// content.
func (c CreatedStrengthActivity) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "createdStrengthActivity"),
		slog.String("activityId", presence(!c.Activity.IsZero())),
		slog.Int("sets", c.Sets.Sets.Len()),
	)
}
