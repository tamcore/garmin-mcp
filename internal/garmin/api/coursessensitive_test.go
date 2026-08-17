package api_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// testSyntheticCourseName is the distinctive fixture name reused across
// this file's models, so it can be a single package constant rather than a
// repeated literal.
const testSyntheticCourseName = "Loop Trail Synthetic"

// courseLogNeedles reports the fixture values a course log line must never
// contain: a distinctive name, coordinates and a share URL, each at least 5
// characters.
func courseLogNeedles() []string {
	return []string{
		testSyntheticCourseName, "47.556677", "8.556677", "modern/course/778899",
	}
}

func collectCourseModels(t *testing.T) map[string]any {
	t.Helper()

	// c.get("courseId"), c.get("courseName"), c.get("distanceInMeters"),
	// c.get("elevationGainInMeters"), c.get("elevationLossInMeters"),
	// (c.get("activityType") or {}).get("typeKey"), c.get("hasPaceBand"),
	// c.get("createdDateFormatted") (courses.py:185-196).
	course := mustDecodeModel[api.Course](t,
		`{"courseId":778899,"courseName":"`+testSyntheticCourseName+`","distanceInMeters":5230.5,`+
			`"elevationGainInMeters":120,"elevationLossInMeters":118,`+
			`"activityType":{"typeKey":"`+seriesRunning+`"},"hasPaceBand":true,`+
			`"createdDateFormatted":"2026-02-01"}`)

	id, err := client.NewID(778899)
	if err != nil {
		t.Fatalf("NewID() = %v", err)
	}

	return map[string]any{
		"Course":             course,
		"CourseActivityType": *course.ActivityType,
		"CourseUpload": api.CourseUpload{
			GPX:          []byte("<gpx>" + testSyntheticCourseName + " 47.556677 8.556677</gpx>"),
			Name:         testSyntheticCourseName,
			ActivityType: seriesRunning,
			Description:  testSyntheticCourseName,
		},
		"UploadedCourse": api.UploadedCourse{
			CourseID: id, Name: testSyntheticCourseName,
			URL: "https://connect.garmin.com/modern/course/778899",
		},
	}
}

// TestCourseModelsAreNotLoggable proves that handing a course-management
// model to slog reports its shape only, never a name, a coordinate or a
// share URL.
func TestCourseModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	for name, value := range collectCourseModels(t) {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, leakLogOptions())).Info("garmin read", "model", value)

		rendered := logged.String()
		for _, needle := range courseLogNeedles() {
			if strings.Contains(rendered, needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
			}
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}
