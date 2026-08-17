package api

import "log/slog"

// The course-management domain's LogValue implementations. Every model here
// carries a route — a sequence of latitude/longitude points — or a value
// derived from one, so each reports its shape only, following the same
// discipline sensitive.go documents for the rest of this package. Kept in a
// file of its own, the same reason weightsensitive.go is.

// LogValue reports which fields one course listing entry carries, never its
// name, its distance or its activity type.
func (c Course) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "course"),
		slog.String("courseId", presence(c.CourseID.IsSet())),
		slog.String("name", presence(c.Name.IsSet())),
		slog.String("distanceMeters", presence(c.DistanceMeters.IsSet())),
		slog.String("elevationGainMeters", presence(c.ElevationGainMeters.IsSet())),
		slog.String("elevationLossMeters", presence(c.ElevationLossMeters.IsSet())),
		slog.String("activityType", presence(c.ActivityType != nil)),
		slog.String("hasPaceBand", presence(c.HasPaceBand != nil)),
		slog.String("created", presence(c.Created.IsSet())),
	)
}

// LogValue reports whether the nested activity type arrived, never its key.
func (a CourseActivityType) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "courseActivityType"),
		slog.String("typeKey", presence(a.TypeKey.IsSet())),
	)
}

// LogValue reports the shape of an upload request, never the GPX content, a
// caller-supplied name or a description — and never a geo point, which is
// exactly the location data this whole domain exists to keep out of a log
// line.
func (u CourseUpload) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "courseUpload"),
		slog.Int("gpxBytes", len(u.GPX)),
		slog.String("name", presence(u.Name != "")),
		slog.String("activityType", presence(u.ActivityType != "")),
		slog.String("description", presence(u.Description != "")),
	)
}

// LogValue reports that a course was saved, never its name, its route or its
// share URL.
func (u UploadedCourse) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "uploadedCourse"),
		slog.String("courseId", presence(!u.CourseID.IsZero())),
		slog.String("name", presence(u.Name != "")),
	)
}
