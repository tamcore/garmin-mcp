package tools

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func getCoursesRegistration() newToolsRegistration {
	return newToolsRegistration{name: ToolGetCourses, register: registerGetCourses}
}

// TestGetCoursesReturnsTheCuratedListing drives get_courses through the real
// server, pinning the same curated shape courses_test.go's api-layer test
// asserts, plus the tool's own count/truncated envelope.
func TestGetCoursesReturnsTheCuratedListing(t *testing.T) {
	t.Parallel()

	body := `[{"courseId":9001,"courseName":"Loop Trail","distanceInMeters":5230.5,` +
		`"elevationGainInMeters":120,"elevationLossInMeters":118,` +
		`"activityType":{"typeKey":"running"},"hasPaceBand":true,` +
		`"createdDateFormatted":"2026-02-01"}]`
	svc, _ := newToolsService(t, testkit.NewScript().
		With(client.PathCourseBase, testkit.JSON(http.StatusOK, body)))
	server := newToolsServer(t, svc, newToolsServerConfig{
		readOnly: []newToolsRegistration{getCoursesRegistration()},
	})
	session := newToolsSession(t, server)

	out := newToolsCall(t, session, ToolGetCourses, nil)
	if got := out["count"]; got != float64(1) {
		t.Errorf("count = %v, want 1", got)
	}
	courses := list(t, out, "courses")
	if len(courses) != 1 {
		t.Fatalf("len(courses) = %d, want 1", len(courses))
	}
	course := entry(t, courses, 0)
	if course["course_id"] != float64(9001) {
		t.Errorf("course_id = %v, want 9001", course["course_id"])
	}
	if course["name"] != "Loop Trail" {
		t.Errorf("name = %v, want Loop Trail", course["name"])
	}
	if course["activity"] != "running" {
		t.Errorf("activity = %v, want running", course["activity"])
	}
	if out["truncated"] != false {
		t.Errorf("truncated = %v, want false", out["truncated"])
	}
}

// TestGetCoursesDeclaresTheUpstreamContract pins the wire name, tier and
// category.
func TestGetCoursesDeclaresTheUpstreamContract(t *testing.T) {
	t.Parallel()

	contract := getCoursesContract()
	if contract.Spec.Name != ToolGetCourses {
		t.Errorf("name = %q, want %q", contract.Spec.Name, ToolGetCourses)
	}
	if contract.Spec.Category != categoryLocation {
		t.Errorf("category = %q, want %q", contract.Spec.Category, categoryLocation)
	}
	if !contract.Spec.Annotations.ReadOnly {
		t.Error("annotations.ReadOnly = false, want true")
	}
}
