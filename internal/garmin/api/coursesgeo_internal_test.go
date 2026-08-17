package api

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func mustGeoPoint(t *testing.T, lat, lon float64, elevation *float64) geoPoint {
	t.Helper()

	p := geoPoint{}
	if err := p.setNumber("latitude", lat); err != nil {
		t.Fatalf("setNumber(latitude) = %v", err)
	}
	if err := p.setNumber("longitude", lon); err != nil {
		t.Fatalf("setNumber(longitude) = %v", err)
	}
	if elevation != nil {
		if err := p.setNumber("elevation", *elevation); err != nil {
			t.Fatalf("setNumber(elevation) = %v", err)
		}
	}
	return p
}

// TestSetNumberRefusesANonFiniteValue proves a NaN or infinite value is
// reported as an error rather than silently written as JSON null: a nil
// json.RawMessage marshals as "null", which would otherwise hide a
// computation defect (for instance haversineMeters on a degenerate point
// pair) behind an unremarkable-looking null in the create payload.
func TestSetNumberRefusesANonFiniteValue(t *testing.T) {
	t.Parallel()

	p := geoPoint{}
	if err := p.setNumber("distance", math.NaN()); !errors.Is(err, client.ErrValidation) {
		t.Errorf("setNumber(NaN) = %v, want ErrValidation", err)
	}
	if _, ok := p["distance"]; ok {
		t.Errorf(`p["distance"] was set despite the marshal failure: %s`, p["distance"])
	}
}

// TestHaversineMetersMatchesAKnownDistance pins _haversine's formula
// (courses.py:39-44) against a well-known distance: one degree of latitude
// is close to 111.19 km at the equator.
func TestHaversineMetersMatchesAKnownDistance(t *testing.T) {
	t.Parallel()

	a := mustGeoPoint(t, 0, 0, nil)
	b := mustGeoPoint(t, 1, 0, nil)

	got, err := haversineMeters(a, b)
	if err != nil {
		t.Fatalf("haversineMeters() = %v", err)
	}
	want := 111194.0
	if math.Abs(got-want) > 200 {
		t.Errorf("haversineMeters() = %v, want close to %v", got, want)
	}
}

func TestHaversineMetersRefusesAMissingCoordinate(t *testing.T) {
	t.Parallel()

	a := mustGeoPoint(t, 0, 0, nil)
	b := geoPoint{}
	if _, err := haversineMeters(a, b); !errors.Is(err, client.ErrValidation) {
		t.Errorf("haversineMeters() with a missing coordinate = %v, want ErrValidation", err)
	}
}

// TestInitialBearingDegreesPointsDueEast pins _initial_bearing (courses.py:47-52):
// due east along the equator is bearing 90.
func TestInitialBearingDegreesPointsDueEast(t *testing.T) {
	t.Parallel()

	a := mustGeoPoint(t, 0, 0, nil)
	b := mustGeoPoint(t, 0, 1, nil)

	got, err := initialBearingDegrees(a, b)
	if err != nil {
		t.Fatalf("initialBearingDegrees() = %v", err)
	}
	if math.Abs(got-90) > 0.01 {
		t.Errorf("initialBearingDegrees() = %v, want close to 90", got)
	}
}

// TestBuildCoursePayloadComputesDistanceAndDefaultsElevation pins
// _build_course_payload (courses.py:69-166): cumulative distance per point,
// a default elevation of 0.0 for a point that omits it, and a preserved
// unevidenced field on each point.
func TestBuildCoursePayloadComputesDistanceAndDefaultsElevation(t *testing.T) {
	t.Parallel()

	elevation := 410.5
	first := mustGeoPoint(t, 47.0, 8.0, nil)
	first["sequence"] = json.RawMessage(`7`) // an unevidenced field this package must preserve verbatim.
	second := mustGeoPoint(t, 47.01, 8.01, &elevation)
	points := []geoPoint{first, second}

	payload, err := buildCoursePayload(points, "Loop", 1, "")
	if err != nil {
		t.Fatalf("buildCoursePayload() = %v", err)
	}

	if payload.DistanceMeter <= 0 {
		t.Errorf("DistanceMeter = %v, want > 0", payload.DistanceMeter)
	}
	if len(payload.GeoPoints) != 2 {
		t.Fatalf("len(GeoPoints) = %d, want 2", len(payload.GeoPoints))
	}
	firstElevation, ok := payload.GeoPoints[0].number("elevation")
	if !ok || firstElevation != 0 {
		t.Errorf("first point elevation = %v, %v, want 0, true", firstElevation, ok)
	}
	firstDistance, ok := payload.GeoPoints[0].number("distance")
	if !ok || firstDistance != 0 {
		t.Errorf("first point distance = %v, %v, want 0, true", firstDistance, ok)
	}
	if _, ok := payload.GeoPoints[0]["sequence"]; !ok {
		t.Errorf("the unevidenced sequence field was dropped")
	}
	if payload.StartPoint.Latitude != 47.0 || payload.StartPoint.Longitude != 8.0 {
		t.Errorf("StartPoint = %+v, want the first point's coordinates", payload.StartPoint)
	}
	if payload.CourseName != "Loop" || payload.ActivityTypePk != 1 {
		t.Errorf("CourseName/ActivityTypePk = %q/%d, want Loop/1", payload.CourseName, payload.ActivityTypePk)
	}
	if payload.Description != nil {
		t.Errorf("Description = %v, want nil for an empty description", *payload.Description)
	}
}

func TestBuildCoursePayloadRefusesFewerThanTwoPoints(t *testing.T) {
	t.Parallel()

	points := []geoPoint{mustGeoPoint(t, 0, 0, nil)}
	if _, err := buildCoursePayload(points, "Loop", 1, ""); !errors.Is(err, client.ErrValidation) {
		t.Errorf("buildCoursePayload() with one point = %v, want ErrValidation", err)
	}
}

func TestBuildCoursePayloadRefusesMoreThanMaxCourseGeoPoints(t *testing.T) {
	t.Parallel()

	points := manyGeoPoints(t, maxCourseGeoPoints+1)
	if _, err := buildCoursePayload(points, "Loop", 1, ""); !errors.Is(err, client.ErrValidation) {
		t.Errorf("buildCoursePayload() with maxCourseGeoPoints+1 points = %v, want ErrValidation", err)
	}
}

// TestMaxCourseGeoPointsKeepsTheCreatePayloadUnderTheBodyBound proves
// maxCourseGeoPoints is an honest bound, not the dead 20,000 magic number it
// replaced: a payload built at exactly the bound must marshal under
// MaxRequestBodyBytes, because the create payload serializes the point list
// twice (courseLine.Points and the top-level GeoPoints).
func TestMaxCourseGeoPointsKeepsTheCreatePayloadUnderTheBodyBound(t *testing.T) {
	t.Parallel()

	points := manyGeoPoints(t, maxCourseGeoPoints)
	payload, err := buildCoursePayload(points, "Loop", 1, "a synthetic route description")
	if err != nil {
		t.Fatalf("buildCoursePayload() = %v", err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() = %v", err)
	}
	if len(encoded) > MaxRequestBodyBytes {
		t.Errorf("len(encoded) = %d, want <= %d (MaxRequestBodyBytes)", len(encoded), MaxRequestBodyBytes)
	}
}

// manyGeoPoints builds a synthetic point list of the given length, each
// point a fraction of a degree further along a line so every one is
// distinct.
func manyGeoPoints(t *testing.T, count int) []geoPoint {
	t.Helper()

	points := make([]geoPoint, count)
	for i := range points {
		points[i] = mustGeoPoint(t, float64(i)*0.0001, float64(i)*0.0001, nil)
	}
	return points
}
