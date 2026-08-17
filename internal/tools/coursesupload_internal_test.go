package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func uploadCourseRegistration() newToolsRegistration {
	return newToolsRegistration{name: ToolUploadCourse, register: registerUploadCourse}
}

// TestUploadCourseSendsContentNotAPath proves the tool argument is
// gpx_content, never a filesystem path, and that a call with content
// dispatches the two-step upload.
func TestUploadCourseSendsContentNotAPath(t *testing.T) {
	t.Parallel()

	importBody := `{"courseName":"Parsed Name","geoPoints":[` +
		`{"latitude":47.0,"longitude":8.0},{"latitude":47.01,"longitude":8.01}]}`
	savedBody := `{"courseId":4242,"courseName":"My Course","distanceMeter":1500.25,` +
		`"elevationGainMeter":30,"elevationLossMeter":12,"activityTypePk":1}`
	svc, fake := newToolsService(t, testkit.NewScript().
		With(client.PathCourseImport, testkit.JSON(http.StatusOK, importBody)).
		With(client.PathCourseBase, testkit.JSON(http.StatusOK, savedBody)))
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{uploadCourseRegistration()},
	})
	session := newToolsSession(t, server)

	out := newToolsCall(t, session, ToolUploadCourse, map[string]any{
		argGPXContent: "<gpx>synthetic route</gpx>", argCourseName: "My Course",
	})
	if out["course_id"] != float64(4242) {
		t.Errorf("course_id = %v, want 4242", out["course_id"])
	}
	if out["name"] != "My Course" {
		t.Errorf("name = %v, want My Course", out["name"])
	}
	if url, _ := out["url"].(string); !strings.Contains(url, "/modern/course/4242") {
		t.Errorf("url = %q, want it to contain /modern/course/4242", url)
	}

	requests := fake.Requests()
	if len(requests) != 2 {
		t.Fatalf("len(requests) = %d, want 2", len(requests))
	}
	if !strings.Contains(string(requests[0].Body), "synthetic route") {
		t.Errorf("the import request body = %q, want it to carry the GPX content", requests[0].Body)
	}
}

// TestUploadCourseAcceptsCRLFTerminatedGPX proves a Windows-exported,
// CRLF-terminated GPX document is accepted rather than refused as carrying a
// control character: parseText's own \r refusal is correct for a free-text
// name or note, but gpx_content is an XML document, and most GPX files
// exported on Windows are CRLF-terminated.
func TestUploadCourseAcceptsCRLFTerminatedGPX(t *testing.T) {
	t.Parallel()

	importBody := `{"courseName":"Parsed Name","geoPoints":[` +
		`{"latitude":47.0,"longitude":8.0},{"latitude":47.01,"longitude":8.01}]}`
	savedBody := `{"courseId":4242,"courseName":"My Course","distanceMeter":1500.25,` +
		`"elevationGainMeter":30,"elevationLossMeter":12,"activityTypePk":1}`
	svc, _ := newToolsService(t, testkit.NewScript().
		With(client.PathCourseImport, testkit.JSON(http.StatusOK, importBody)).
		With(client.PathCourseBase, testkit.JSON(http.StatusOK, savedBody)))
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{uploadCourseRegistration()},
	})
	session := newToolsSession(t, server)

	crlfGPX := "<?xml version=\"1.0\"?>\r\n<gpx>\r\n<trk></trk>\r\n</gpx>\r\n"
	out := newToolsCall(t, session, ToolUploadCourse, map[string]any{argGPXContent: crlfGPX})
	if out["course_id"] != float64(4242) {
		t.Errorf("course_id = %v, want 4242 (a CRLF-terminated GPX must not be refused)", out["course_id"])
	}
}

func TestUploadCourseRefusesEmptyContent(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript())
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{uploadCourseRegistration()},
	})
	session := newToolsSession(t, server)

	message := newToolsCallError(t, session, ToolUploadCourse, map[string]any{argGPXContent: ""})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestUploadCourseDeclaresNoGPXPathArgument(t *testing.T) {
	t.Parallel()

	schema := uploadCourseContract().Schema
	for _, property := range schema.Properties() {
		if property.Name == "gpx_path" {
			t.Fatal("the schema declares gpx_path, which would accept a caller-supplied filesystem path")
		}
	}
	if got := schema.Required(); len(got) != 1 || got[0] != argGPXContent {
		t.Errorf("required = %v, want exactly [gpx_content]", got)
	}
}
