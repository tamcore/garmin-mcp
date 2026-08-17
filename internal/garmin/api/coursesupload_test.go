package api_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// testUploadedCourseName is the distinctive fixture name this file's upload
// tests share, so it is a single package constant rather than a repeated
// literal.
const testUploadedCourseName = "My Course"

// TestUploadCourseDispatchesTheTwoStepFlow pins courses.py:202-286's two-step
// upload: a multipart POST to /course-service/course/import, then a JSON
// POST to /course-service/course carrying the enriched payload
// _build_course_payload assembles, reading back courseId, courseName,
// distanceMeter, elevationGainMeter, elevationLossMeter and activityTypePk
// (courses.py:274-279).
func TestUploadCourseDispatchesTheTwoStepFlow(t *testing.T) {
	t.Parallel()

	importBody := `{"courseName":"Parsed Name","geoPoints":[` +
		`{"latitude":47.0,"longitude":8.0},` +
		`{"latitude":47.01,"longitude":8.01,"elevation":410.5}]}`
	savedBody := `{"courseId":4242,"courseName":"` + testUploadedCourseName + `","distanceMeter":1500.25,` +
		`"elevationGainMeter":30,"elevationLossMeter":12,"activityTypePk":1}`

	script := testkit.NewScript().
		With(client.PathCourseImport, testkit.JSON(http.StatusOK, importBody)).
		With(client.PathCourseBase, testkit.JSON(http.StatusOK, savedBody))
	h := newHarness(t, script, client.Limits{})

	upload := api.CourseUpload{
		GPX:          []byte("<gpx>synthetic</gpx>"),
		Name:         testUploadedCourseName,
		ActivityType: seriesRunning,
		Description:  "a synthetic loop",
	}
	result, err := newCourses(t, h).UploadCourse(t.Context(), h.session, upload)
	if err != nil {
		t.Fatalf("UploadCourse() = %v", err)
	}
	assertUploadedCourseResult(t, result)
	assertUploadCourseRequests(t, h.server.Requests())
}

// assertUploadedCourseResult checks the mapped result UploadCourse reports.
func assertUploadedCourseResult(t *testing.T, result api.UploadedCourse) {
	t.Helper()

	if result.CourseID.Int64() != 4242 {
		t.Errorf("CourseID = %v, want 4242", result.CourseID.Int64())
	}
	if result.Name != testUploadedCourseName {
		t.Errorf("Name = %q, want %q", result.Name, testUploadedCourseName)
	}
	if result.DistanceMeters != 1500.25 {
		t.Errorf("DistanceMeters = %v, want 1500.25", result.DistanceMeters)
	}
	if result.URL == "" || !strings.Contains(result.URL, "/modern/course/4242") {
		t.Errorf("URL = %q, want it to contain /modern/course/4242", result.URL)
	}
}

// assertUploadCourseRequests checks the two dispatched requests: the
// multipart import, then the JSON create.
func assertUploadCourseRequests(t *testing.T, requests []testkit.RecordedRequest) {
	t.Helper()

	if len(requests) != 2 {
		t.Fatalf("len(requests) = %d, want 2", len(requests))
	}
	if requests[0].Path != client.PathCourseImport || requests[0].Method != http.MethodPost {
		t.Errorf("first request = %+v, want a POST to %q", requests[0], client.PathCourseImport)
	}
	if !strings.Contains(requests[0].Header.Get("Content-Type"), "multipart/form-data") {
		t.Errorf("first request content type = %q, want multipart/form-data",
			requests[0].Header.Get("Content-Type"))
	}
	if !strings.Contains(string(requests[0].Body), "synthetic") {
		t.Errorf("first request body = %q, want it to carry the GPX bytes", requests[0].Body)
	}

	if requests[1].Path != client.PathCourseBase || requests[1].Method != http.MethodPost {
		t.Errorf("second request = %+v, want a POST to %q", requests[1], client.PathCourseBase)
	}
	body := decodeBody(t, requests[1].Body)
	if body["courseName"] != testUploadedCourseName {
		t.Errorf("courseName = %v, want %q", body["courseName"], testUploadedCourseName)
	}
	if body["activityTypePk"] != float64(1) {
		t.Errorf("activityTypePk = %v, want 1", body["activityTypePk"])
	}
	if body["distanceMeter"] == nil {
		t.Errorf("distanceMeter missing from the create payload")
	}
}

func TestUploadCourseRefusesEmptyGPX(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	upload := api.CourseUpload{ActivityType: seriesRunning}
	_, err := newCourses(t, h).UploadCourse(t.Context(), h.session, upload)
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("UploadCourse() with empty GPX = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestUploadCourseRefusesAnUnknownActivityType(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	upload := api.CourseUpload{GPX: []byte("<gpx/>"), ActivityType: "skiing"}
	_, err := newCourses(t, h).UploadCourse(t.Context(), h.session, upload)
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("UploadCourse() with an unknown activity type = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestUploadCourseRefusesOversizedGPX(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	upload := api.CourseUpload{
		GPX:          make([]byte, 300*1024),
		ActivityType: seriesRunning,
	}
	_, err := newCourses(t, h).UploadCourse(t.Context(), h.session, upload)
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("UploadCourse() with oversized GPX = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestUploadCourseFailsWhenGarminReturnsNoCourseID proves the tool fails
// rather than reporting success with a zero course_id and an empty url when
// Garmin's create-course response carries no usable identifier: a caller
// told the write "succeeded" that way could never name the created object
// again, not even to delete it.
func TestUploadCourseFailsWhenGarminReturnsNoCourseID(t *testing.T) {
	t.Parallel()

	importBody := `{"courseName":"Parsed Name","geoPoints":[` +
		`{"latitude":47.0,"longitude":8.0},{"latitude":47.01,"longitude":8.01}]}`
	savedBody := `{"courseName":"` + testUploadedCourseName + `","distanceMeter":1500.25}`

	script := testkit.NewScript().
		With(client.PathCourseImport, testkit.JSON(http.StatusOK, importBody)).
		With(client.PathCourseBase, testkit.JSON(http.StatusOK, savedBody))
	h := newHarness(t, script, client.Limits{})

	upload := api.CourseUpload{GPX: []byte("<gpx/>"), ActivityType: seriesRunning}
	_, err := newCourses(t, h).UploadCourse(t.Context(), h.session, upload)
	if !errors.Is(err, client.ErrUnexpectedResponse) {
		t.Errorf("UploadCourse() with no course id in the response = %v, want ErrUnexpectedResponse", err)
	}
}

// TestUploadCourseNormalizesANullElevationToZero pins courses.py:89-90's own
// `if p.get("elevation") is None: p["elevation"] = 0.0`: a geo point whose
// elevation Garmin's /import response reports as JSON null must reach the
// create payload as 0, not as a literal null.
func TestUploadCourseNormalizesANullElevationToZero(t *testing.T) {
	t.Parallel()

	importBody := `{"courseName":"Parsed Name","geoPoints":[` +
		`{"latitude":47.0,"longitude":8.0,"elevation":null},` +
		`{"latitude":47.01,"longitude":8.01,"elevation":null}]}`
	savedBody := `{"courseId":4243,"courseName":"` + testUploadedCourseName + `"}`

	script := testkit.NewScript().
		With(client.PathCourseImport, testkit.JSON(http.StatusOK, importBody)).
		With(client.PathCourseBase, testkit.JSON(http.StatusOK, savedBody))
	h := newHarness(t, script, client.Limits{})

	upload := api.CourseUpload{GPX: []byte("<gpx/>"), ActivityType: seriesRunning}
	if _, err := newCourses(t, h).UploadCourse(t.Context(), h.session, upload); err != nil {
		t.Fatalf("UploadCourse() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 2 {
		t.Fatalf("len(requests) = %d, want 2", len(requests))
	}
	body := decodeBody(t, requests[1].Body)
	startPoint, _ := body["startPoint"].(map[string]any)
	if startPoint["elevation"] != float64(0) {
		t.Errorf("startPoint.elevation = %v, want 0 (a null elevation must normalize to 0)",
			startPoint["elevation"])
	}
	if strings.Contains(string(requests[1].Body), `"elevation":null`) {
		t.Errorf("the create payload still carries a literal null elevation: %s", requests[1].Body)
	}
}

func TestCourseActivityTypeKeysMatchesTheKnownSet(t *testing.T) {
	t.Parallel()

	keys := api.CourseActivityTypeKeys()
	want := []string{
		activityCycling, "gravel_cycling", "hiking", "mountain_biking",
		"road_biking", seriesRunning, "trail_running", "walking",
	}
	if len(keys) != len(want) {
		t.Fatalf("len(keys) = %d, want %d: %v", len(keys), len(want), keys)
	}
	for i, key := range want {
		if keys[i] != key {
			t.Errorf("keys[%d] = %q, want %q", i, keys[i], key)
		}
	}
}
