package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// activityCycling is the one course activity_type key this file's sibling
// tests reference that has no existing shared constant (seriesRunning,
// defined in trainingthresholds_test.go, covers "running").
const activityCycling = "cycling"

func newCourses(t *testing.T, h harness) *api.Courses {
	t.Helper()

	c, err := api.NewCourses(h.rc)
	if err != nil {
		t.Fatalf("NewCourses() = %v", err)
	}
	return c
}

func TestNewCoursesRefusesANilRequestLayer(t *testing.T) {
	t.Parallel()

	if _, err := api.NewCourses(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Fatalf("NewCourses(nil) = %v, want %v", err, client.ErrNotConfigured)
	}
}

// TestGetCoursesDecodesTheCuratedFields pins every field courses.py:185-196
// reads off one course, plus the nested activityType.typeKey.
func TestGetCoursesDecodesTheCuratedFields(t *testing.T) {
	t.Parallel()

	body := `[{"courseId":9001,"courseName":"Loop Trail","distanceInMeters":5230.5,` +
		`"elevationGainInMeters":120,"elevationLossInMeters":118,` +
		`"activityType":{"typeKey":"` + seriesRunning + `"},"hasPaceBand":true,` +
		`"createdDateFormatted":"2026-02-01"}]`
	script := testkit.NewScript().With(client.PathCourseBase, testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	courses, err := newCourses(t, h).GetCourses(t.Context(), h.session)
	if err != nil {
		t.Fatalf("GetCourses() = %v", err)
	}
	if len(courses) != 1 {
		t.Fatalf("len(courses) = %d, want 1", len(courses))
	}
	got := courses[0]
	if id, ok := got.CourseID.Int64Exact(); !ok || id != 9001 {
		t.Errorf("CourseID = %v, %v, want 9001, true", id, ok)
	}
	if name, _ := got.Name.Value(); name != "Loop Trail" {
		t.Errorf("Name = %q, want %q", name, "Loop Trail")
	}
	if distance, _ := got.DistanceMeters.Float64(); distance != 5230.5 {
		t.Errorf("DistanceMeters = %v, want 5230.5", distance)
	}
	if got.ActivityType == nil {
		t.Fatalf("ActivityType = nil, want a value")
	}
	if key, _ := got.ActivityType.TypeKey.Value(); key != seriesRunning {
		t.Errorf("ActivityType.TypeKey = %q, want %q", key, seriesRunning)
	}
	if got.HasPaceBand == nil || !*got.HasPaceBand {
		t.Errorf("HasPaceBand = %v, want a pointer to true", got.HasPaceBand)
	}
	if requests := h.server.Requests(); len(requests) != 1 || requests[0].Method != http.MethodGet {
		t.Errorf("requests = %+v, want one GET", requests)
	}
}

// TestGetCoursesPreservesAMissingHasPaceBandAsAbsence proves a course
// listing entry that omits hasPaceBand reports that as absence (a nil
// pointer), never coerced to false: courses.py's own `c.get("hasPaceBand")`
// reports upstream's real None the same way.
func TestGetCoursesPreservesAMissingHasPaceBandAsAbsence(t *testing.T) {
	t.Parallel()

	body := `[{"courseId":9002,"courseName":"No Band","distanceInMeters":1000,` +
		`"elevationGainInMeters":10,"elevationLossInMeters":5}]`
	script := testkit.NewScript().With(client.PathCourseBase, testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	courses, err := newCourses(t, h).GetCourses(t.Context(), h.session)
	if err != nil {
		t.Fatalf("GetCourses() = %v", err)
	}
	if len(courses) != 1 {
		t.Fatalf("len(courses) = %d, want 1", len(courses))
	}
	if got := courses[0].HasPaceBand; got != nil {
		t.Errorf("HasPaceBand = %v, want nil for a missing field", *got)
	}
}

// TestDeleteCourseSendsADeleteToTheCoursePath pins courses.py:295-298's
// DELETE "/course-service/course/{course_id}".
func TestDeleteCourseSendsADeleteToTheCoursePath(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathCourseBase+"/9001", testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	id, err := client.NewID(9001)
	if err != nil {
		t.Fatalf("NewID() = %v", err)
	}
	if _, err := newCourses(t, h).DeleteCourse(t.Context(), h.session, id); err != nil {
		t.Fatalf("DeleteCourse() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(requests))
	}
	if requests[0].Method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", requests[0].Method)
	}
	if requests[0].Path != client.PathCourseBase+"/9001" {
		t.Errorf("path = %q, want %q", requests[0].Path, client.PathCourseBase+"/9001")
	}
}

func TestDeleteCourseRefusesAZeroIdentifier(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	_, err := newCourses(t, h).DeleteCourse(t.Context(), h.session, client.ID{})
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("DeleteCourse() with a zero id = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
