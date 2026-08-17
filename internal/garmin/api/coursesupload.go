package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// maxCourseGPXBytes bounds the GPX content this package will upload. GPX is
// XML text; a real course file, even a long ultramarathon route, is tens of
// kilobytes. This is a safety bound on the upload itself, not a guarantee
// about the create-course JSON built afterward: Garmin's own /import step
// can answer with far more geo point data than the uploaded GPX's byte count
// alone would suggest (extra per-point fields, added precision), so the
// bound that actually keeps the create-course JSON under
// MaxRequestBodyBytes is maxCourseGeoPoints below, not this one.
const maxCourseGPXBytes = 256 * 1024

// courseGeoPointJSONBytes is a conservative per-point byte estimate used to
// derive maxCourseGeoPoints. A parsed geo point (latitude, longitude,
// elevation and the distance this package itself computes in
// buildCoursePayload, each a quoted key and a double-precision number)
// measures roughly 65-90 bytes JSON-encoded in practice; 160 is a
// deliberately generous ceiling above that measured range, to absorb a point
// carrying extra fields Garmin's /import response preserved.
const courseGeoPointJSONBytes = 160

// maxCourseGeoPoints bounds how many geo points one imported course may
// carry before this package will build a create payload for them. It is not
// a defensive round number: courseCreatePayload serializes the identical
// point list twice — once in courseLine.Points, once again at the top level
// in GeoPoints (see courseCreatePayload's own field comment) — so the point
// data alone costs roughly 2 * courseGeoPointJSONBytes per point, and this
// bound keeps that under three quarters of MaxRequestBodyBytes, leaving
// headroom for the rest of the payload (name, description, bounding box,
// course line metadata). A route that passes this check is provably safe to
// marshal, not merely likely to be.
//
// This is enforced in buildCoursePayload, before the create JSON is ever
// built: that is the earliest point in the flow where this package can
// check, because the point count is Garmin's own /import response and is
// not known before that call returns. A route rejected here therefore still
// costs one /import round trip; avoiding even that would need this package
// to parse the GPX itself before upload, which it deliberately does not do
// (courses.py never validates a GPX locally either; Garmin's own service is
// the parser).
const maxCourseGeoPoints = (MaxRequestBodyBytes * 3 / 4) / (2 * courseGeoPointJSONBytes)

// courseCoordinateSystem is the literal every coordinate-system field in the
// create payload carries. Source: courses.py:150-156, "WGS84" repeated on
// coordinateSystem, originalCoordinateSystem and targetCoordinateSystem.
const courseCoordinateSystem = "WGS84"

// defaultUploadedCourseName is used only when neither the caller nor
// Garmin's own /import response names the course. Upstream's own fallback
// (courses.py:257, `os.path.splitext(os.path.basename(gpx_path))[0]`) is the
// GPX file's own filename stem, which does not exist here: this package
// never takes a caller-supplied filesystem path (see AGENTS.md's file
// discipline and docs/parity.md on download_activity_file), so there is no
// filename to fall back to. This literal is the deliberate, documented
// replacement.
const defaultUploadedCourseName = "Uploaded course"

// courseGPXFilename and courseGPXContentType are the multipart part Garmin's
// import step is sent under. Source: courses.py:244-250,
// `files={"file": (os.path.basename(gpx_path), gpx_bytes,
// "application/gpx+xml")}`. The filename itself carries no meaning to
// Garmin's parser (only the bytes and the declared content type do), so a
// fixed name replaces the caller's own filename, which this package never
// receives.
const (
	courseGPXFilename    = "course.gpx"
	courseGPXContentType = "application/gpx+xml"
)

// courseActivityTypeIDs maps a course activity_type key to Garmin's
// course-service activity type id. A function, not a var: AGENTS.md allows
// no package-level mutable state.
//
// Source: courses.py:57-66, _ACTIVITY_TYPE_IDS.
func courseActivityTypeIDs() map[string]int {
	return map[string]int{
		"running":         1,
		"cycling":         2,
		"hiking":          3,
		"walking":         9,
		"trail_running":   6,
		"mountain_biking": 5,
		"road_biking":     10,
		"gravel_cycling":  4,
	}
}

// CourseActivityTypeKeys returns the accepted activity_type keys, sorted, so
// a caller (the tool schema's enum) can enumerate them without duplicating
// courseActivityTypeIDs.
func CourseActivityTypeKeys() []string {
	ids := courseActivityTypeIDs()
	keys := make([]string, 0, len(ids))
	for key := range ids {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// ParseCourseActivityType validates a course activity_type key against
// courses.py's own closed set.
func ParseCourseActivityType(value string) (int, error) {
	id, ok := courseActivityTypeIDs()[value]
	if !ok {
		return 0, fmt.Errorf("%w: activity_type must be one of the course activity types",
			client.ErrValidation)
	}
	return id, nil
}

// CourseUpload is the caller-supplied shape of one upload_course call.
//
// GPX is the file's own bytes: this package takes content, never a path.
// Name and Description are the caller's overrides and may be empty, matching
// upstream's own Optional[str] = None.
type CourseUpload struct {
	GPX          []byte
	Name         string
	ActivityType string
	Description  string
}

// UploadedCourse is what UploadCourse reports.
type UploadedCourse struct {
	CourseID            client.ID
	Name                string
	DistanceMeters      float64
	ElevationGainMeters float64
	ElevationLossMeters float64
	ActivityTypeID      int64
	// Status is the HTTP status Garmin's create-course response answered
	// with, the same status-as-HTTP-int convention weightwrite.go's
	// AddWeighInResult documents rather than upstream's own literal
	// "success" string.
	Status int
	// URL is this server's own construction, never a value Garmin returned:
	// courses.py:280 builds it the same way, from the account's own connect
	// host and the saved course id.
	URL string
}

// geoPoint is one point Garmin's /import step returned, kept as a map of raw
// JSON fields rather than a strict struct: courses.py only ever reads
// "latitude", "longitude" and "elevation" off of it (_haversine,
// _initial_bearing, _build_course_payload) and writes "distance" back, while
// preserving whatever other fields the point arrived with untouched
// (courses.py:83-90 mutates the same dict in place). A strict struct would
// silently drop any field this package has no evidence for; this shape
// cannot.
type geoPoint map[string]json.RawMessage

// number reads a numeric field out of the point. A field present but
// explicitly JSON null is reported absent, the same as a field that is not
// present at all: unmarshaling JSON null into a non-pointer float64 leaves
// the zero value in place with no error, which would otherwise make a null
// field indistinguishable from a real 0 and let the literal null survive
// into the create payload untouched (courses.py:89-90 normalises exactly
// this case for elevation, `if p.get("elevation") is None: p["elevation"] =
// 0.0`).
func (p geoPoint) number(key string) (float64, bool) {
	raw, ok := p[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}

// setNumber writes a numeric field into the point. value is usually a
// locally computed float, so the marshal is expected to succeed, but the
// error is never discarded: json.Marshal refuses NaN and +/-Inf, and a
// silently swallowed error there would leave the field encoded as JSON null
// (a nil json.RawMessage marshals as "null"), turning a computation defect —
// for instance haversineMeters on a degenerate pair of points — into an
// unremarkable-looking null in the create payload instead of a visible
// failure.
func (p geoPoint) setNumber(key string, value float64) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: %s could not be computed as a finite number",
			client.ErrValidation, key)
	}
	p[key] = encoded
	return nil
}

// courseImportResult is the /course/import response this package reads.
//
// Source: courses.py:254-258 (`parsed.get("courseName")`) and :77
// (`parsed.get("geoPoints")`).
type courseImportResult struct {
	CourseName client.Text `json:"courseName"`
	GeoPoints  []geoPoint  `json:"geoPoints"`
}

// courseCorner is one latitude/longitude pair in a bounding box.
//
// Source: courses.py:96-101.
type courseCorner struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// courseBoundingBox is the create payload's boundingBox object.
//
// Source: courses.py:95-106.
type courseBoundingBox struct {
	Center              courseCorner `json:"center"`
	LowerLeft           courseCorner `json:"lowerLeft"`
	UpperRight          courseCorner `json:"upperRight"`
	LowerLeftLatIsSet   bool         `json:"lowerLeftLatIsSet"`
	LowerLeftLongIsSet  bool         `json:"lowerLeftLongIsSet"`
	UpperRightLatIsSet  bool         `json:"upperRightLatIsSet"`
	UpperRightLongIsSet bool         `json:"upperRightLongIsSet"`
}

// courseStartPoint is the create payload's startPoint object.
//
// Source: courses.py:108-114.
type courseStartPoint struct {
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Elevation float64  `json:"elevation"`
	Distance  *float64 `json:"distance"`
	Timestamp *string  `json:"timestamp"`
}

// courseLine is the create payload's one courseLines entry.
//
// Source: courses.py:142-153.
type courseLine struct {
	CourseID                 *int64     `json:"courseId"`
	SortOrder                int        `json:"sortOrder"`
	NumberOfPoints           int        `json:"numberOfPoints"`
	DistanceInMeters         float64    `json:"distanceInMeters"`
	Bearing                  float64    `json:"bearing"`
	Points                   []geoPoint `json:"points"`
	CoordinateSystem         string     `json:"coordinateSystem"`
	OriginalCoordinateSystem string     `json:"originalCoordinateSystem"`
}

// courseCreatePayload is the POST "/course-service/course" request body.
//
// Source: courses.py:118-166, _build_course_payload.
type courseCreatePayload struct {
	CourseName               string            `json:"courseName"`
	Description              *string           `json:"description"`
	OpenStreetMap            bool              `json:"openStreetMap"`
	MatchedToSegments        bool              `json:"matchedToSegments"`
	UserProfilePk            *int64            `json:"userProfilePk"`
	UserGroupPk              *int64            `json:"userGroupPk"`
	RulePK                   int               `json:"rulePK"`
	GeoRoutePk               *int64            `json:"geoRoutePk"`
	SourceTypeID             int               `json:"sourceTypeId"`
	SourcePk                 *int64            `json:"sourcePk"`
	DistanceMeter            float64           `json:"distanceMeter"`
	ElevationGainMeter       float64           `json:"elevationGainMeter"`
	ElevationLossMeter       float64           `json:"elevationLossMeter"`
	StartPoint               courseStartPoint  `json:"startPoint"`
	CoursePoints             []any             `json:"coursePoints"`
	BoundingBox              courseBoundingBox `json:"boundingBox"`
	HasShareableEvent        bool              `json:"hasShareableEvent"`
	HasTurnDetectionDisabled bool              `json:"hasTurnDetectionDisabled"`
	ActivityTypePk           int               `json:"activityTypePk"`
	VirtualPartnerID         *int64            `json:"virtualPartnerId"`
	IncludeLaps              bool              `json:"includeLaps"`
	ElapsedSeconds           *int64            `json:"elapsedSeconds"`
	SpeedMeterPerSecond      *float64          `json:"speedMeterPerSecond"`
	CourseLines              []courseLine      `json:"courseLines"`
	CoordinateSystem         string            `json:"coordinateSystem"`
	TargetCoordinateSystem   string            `json:"targetCoordinateSystem"`
	OriginalCoordinateSystem string            `json:"originalCoordinateSystem"`
	Consumer                 *string           `json:"consumer"`
	ElevationSource          int               `json:"elevationSource"`
	HasPaceBand              bool              `json:"hasPaceBand"`
	HasPowerGuide            bool              `json:"hasPowerGuide"`
	Favorite                 bool              `json:"favorite"`
	StartNote                *string           `json:"startNote"`
	FinishNote               *string           `json:"finishNote"`
	CutoffDuration           *int64            `json:"cutoffDuration"`
	GeoPoints                []geoPoint        `json:"geoPoints"`
}

// courseSavedDTO is the POST "/course-service/course" response this package
// reads back.
//
// Source: courses.py:274-279.
type courseSavedDTO struct {
	CourseID           client.Number `json:"courseId"`
	CourseName         client.Text   `json:"courseName"`
	DistanceMeter      client.Number `json:"distanceMeter"`
	ElevationGainMeter client.Number `json:"elevationGainMeter"`
	ElevationLossMeter client.Number `json:"elevationLossMeter"`
	ActivityTypePk     client.Number `json:"activityTypePk"`
}

// earthRadiusMeters is the sphere _haversine assumes.
//
// Source: courses.py:36, _EARTH_RADIUS_M = 6371000.0.
const earthRadiusMeters = 6371000.0

// haversineMeters is _haversine, ported directly.
//
// Source: courses.py:39-44.
func haversineMeters(a, b geoPoint) (float64, error) {
	lat1, lon1, ok1 := a.latLon()
	lat2, lon2, ok2 := b.latLon()
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("%w: a geo point is missing latitude or longitude",
			client.ErrValidation)
	}
	lat1, lon1 = lat1*math.Pi/180, lon1*math.Pi/180
	lat2, lon2 = lat2*math.Pi/180, lon2*math.Pi/180
	dLat, dLon := lat2-lat1, lon2-lon1
	sinDLat, sinDLon := math.Sin(dLat/2), math.Sin(dLon/2)
	a2 := sinDLat*sinDLat + math.Cos(lat1)*math.Cos(lat2)*sinDLon*sinDLon
	return 2 * earthRadiusMeters * math.Asin(math.Sqrt(a2)), nil
}

// initialBearingDegrees is _initial_bearing, ported directly.
//
// Source: courses.py:47-52.
func initialBearingDegrees(a, b geoPoint) (float64, error) {
	lat1, lon1, ok1 := a.latLon()
	lat2, lon2, ok2 := b.latLon()
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("%w: a geo point is missing latitude or longitude",
			client.ErrValidation)
	}
	lat1r, lat2r := lat1*math.Pi/180, lat2*math.Pi/180
	dLon := (lon2 - lon1) * math.Pi / 180
	x := math.Sin(dLon) * math.Cos(lat2r)
	y := math.Cos(lat1r)*math.Sin(lat2r) - math.Sin(lat1r)*math.Cos(lat2r)*math.Cos(dLon)
	bearing := math.Atan2(x, y) * 180 / math.Pi
	return math.Mod(bearing+360, 360), nil
}

// latLon reads both coordinates at once.
func (p geoPoint) latLon() (lat, lon float64, ok bool) {
	lat, latOK := p.number("latitude")
	lon, lonOK := p.number("longitude")
	return lat, lon, latOK && lonOK
}

// buildCoursePayload is _build_course_payload, ported directly.
//
// Source: courses.py:69-166.
func buildCoursePayload(
	points []geoPoint, courseName string, activityTypeID int, description string,
) (courseCreatePayload, error) {
	if len(points) < 2 {
		return courseCreatePayload{}, fmt.Errorf(
			"%w: the parsed course has fewer than two geo points; the GPX is empty or invalid",
			client.ErrValidation)
	}
	if len(points) > maxCourseGeoPoints {
		return courseCreatePayload{}, fmt.Errorf(
			"%w: the parsed course carries more geo points than this server will process",
			client.ErrValidation)
	}

	var totalDistance float64
	minLat, maxLat := math.Inf(1), math.Inf(-1)
	minLon, maxLon := math.Inf(1), math.Inf(-1)
	for i, point := range points {
		lat, lon, ok := point.latLon()
		if !ok {
			return courseCreatePayload{}, fmt.Errorf(
				"%w: a geo point is missing latitude or longitude", client.ErrValidation)
		}
		minLat, maxLat = math.Min(minLat, lat), math.Max(maxLat, lat)
		minLon, maxLon = math.Min(minLon, lon), math.Max(maxLon, lon)

		if i == 0 {
			if err := point.setNumber("distance", 0); err != nil {
				return courseCreatePayload{}, err
			}
		} else {
			delta, err := haversineMeters(points[i-1], point)
			if err != nil {
				return courseCreatePayload{}, err
			}
			totalDistance += delta
			if err := point.setNumber("distance", totalDistance); err != nil {
				return courseCreatePayload{}, err
			}
		}
		if _, ok := point.number("elevation"); !ok {
			if err := point.setNumber("elevation", 0); err != nil {
				return courseCreatePayload{}, err
			}
		}
	}

	startLat, startLon, _ := points[0].latLon()
	startElevation, _ := points[0].number("elevation")
	bearing, err := initialBearingDegrees(points[0], points[len(points)-1])
	if err != nil {
		return courseCreatePayload{}, err
	}

	var descriptionPtr *string
	if description != "" {
		descriptionPtr = &description
	}

	return courseCreatePayload{
		CourseName:         courseName,
		Description:        descriptionPtr,
		OpenStreetMap:      false,
		MatchedToSegments:  false,
		RulePK:             2,
		SourceTypeID:       3,
		DistanceMeter:      totalDistance,
		ElevationGainMeter: 0,
		ElevationLossMeter: 0,
		StartPoint: courseStartPoint{
			Latitude:  startLat,
			Longitude: startLon,
			Elevation: startElevation,
		},
		CoursePoints: []any{},
		BoundingBox: courseBoundingBox{
			Center:              courseCorner{Latitude: (minLat + maxLat) / 2, Longitude: (minLon + maxLon) / 2},
			LowerLeft:           courseCorner{Latitude: minLat, Longitude: minLon},
			UpperRight:          courseCorner{Latitude: maxLat, Longitude: maxLon},
			LowerLeftLatIsSet:   true,
			LowerLeftLongIsSet:  true,
			UpperRightLatIsSet:  true,
			UpperRightLongIsSet: true,
		},
		HasShareableEvent:        false,
		HasTurnDetectionDisabled: false,
		ActivityTypePk:           activityTypeID,
		IncludeLaps:              false,
		CourseLines: []courseLine{{
			SortOrder:                1,
			NumberOfPoints:           len(points),
			DistanceInMeters:         totalDistance,
			Bearing:                  bearing,
			Points:                   points,
			CoordinateSystem:         courseCoordinateSystem,
			OriginalCoordinateSystem: courseCoordinateSystem,
		}},
		CoordinateSystem:         courseCoordinateSystem,
		TargetCoordinateSystem:   courseCoordinateSystem,
		OriginalCoordinateSystem: courseCoordinateSystem,
		ElevationSource:          3,
		HasPaceBand:              false,
		HasPowerGuide:            false,
		Favorite:                 false,
		GeoPoints:                points,
	}, nil
}

// buildMultipartFile renders a single-part multipart/form-data body carrying
// one file, and reports the Content-Type header the request must send. It is
// generic RFC 2388 plumbing, not FIT- or GPX-specific, and is shared by
// UploadCourse and datamanagement.go's AddBodyComposition.
func buildMultipartFile(fieldName, filename, contentType string, data []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name=%q; filename=%q`, fieldName, filename))
	header.Set("Content-Type", contentType)

	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", fmt.Errorf("garmin api: building multipart body: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", fmt.Errorf("garmin api: writing multipart body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("garmin api: closing multipart body: %w", err)
	}
	return buf.Bytes(), writer.FormDataContentType(), nil
}

// UploadCourse uploads a GPX file as a Garmin Connect course, in the same
// two-step flow courses.py documents: an /import step Garmin parses the GPX
// with, and a /course create step this package builds the enriched payload
// for and saves.
//
// Both requests carry EffectUnsafeWrite: each POST with no target identifier
// creates state, so the retry layer must never replay a lost response.
//
// Source: courses.py:202-286.
func (c *Courses) UploadCourse(
	ctx context.Context, session client.Session, upload CourseUpload,
) (UploadedCourse, error) {
	importReq := writeRequest(client.OpUploadCourse, client.EndpointCourseImport,
		http.MethodPost, client.PathCourseImport, client.EffectUnsafeWrite)
	importReq.FileTransfer = true

	if len(upload.GPX) == 0 {
		return UploadedCourse{}, invalid(importReq, fmt.Errorf(
			"%w: GPX content is required", client.ErrValidation))
	}
	if len(upload.GPX) > maxCourseGPXBytes {
		return UploadedCourse{}, invalid(importReq, fmt.Errorf(
			"%w: GPX content exceeds its bound", client.ErrValidation))
	}
	activityTypeID, err := ParseCourseActivityType(upload.ActivityType)
	if err != nil {
		return UploadedCourse{}, invalid(importReq, err)
	}

	body, contentType, err := buildMultipartFile(
		"file", courseGPXFilename, courseGPXContentType, upload.GPX)
	if err != nil {
		return UploadedCourse{}, invalid(importReq, err)
	}
	importReq.Body = body
	importReq.ContentType = contentType

	var imported courseImportResult
	if _, err := c.req.write(ctx, session, importReq, &imported); err != nil {
		return UploadedCourse{}, err
	}

	name := upload.Name
	if name == "" {
		if parsed, ok := imported.CourseName.Value(); ok && parsed != "" {
			name = parsed
		} else {
			name = defaultUploadedCourseName
		}
	}

	payload, err := buildCoursePayload(imported.GeoPoints, name, activityTypeID, upload.Description)
	if err != nil {
		return UploadedCourse{}, invalid(importReq, err)
	}

	createReq := writeRequest(client.OpUploadCourse, client.EndpointCourseCreate,
		http.MethodPost, client.PathCourseBase, client.EffectUnsafeWrite)
	createBody, err := jsonBody(createReq, payload)
	if err != nil {
		return UploadedCourse{}, err
	}
	createReq.Body = createBody

	var saved courseSavedDTO
	response, err := c.req.write(ctx, session, createReq, &saved)
	if err != nil {
		return UploadedCourse{}, err
	}
	return newUploadedCourse(c.req, saved, response.Status())
}

// newUploadedCourse maps the saved course's response onto the result,
// building the share URL this server derives locally rather than one Garmin
// returned.
//
// It refuses rather than reports success when Garmin's own response carries
// no usable course identifier: a caller told a create "succeeded" with
// course_id 0 and an empty url cannot ever name the object again — not to
// read it back, and not to call delete_course on it — so a missing or
// non-exact-integer id is treated as a failed write, not a partially
// successful one.
//
// Source: courses.py:271-282.
func newUploadedCourse(req requester, saved courseSavedDTO, status int) (UploadedCourse, error) {
	rawID, ok := saved.CourseID.Int64Exact()
	if !ok {
		return UploadedCourse{}, fmt.Errorf(
			"%w: Garmin's create-course response carried no usable course identifier",
			client.ErrUnexpectedResponse)
	}
	id, err := client.NewID(rawID)
	if err != nil {
		return UploadedCourse{}, fmt.Errorf(
			"%w: Garmin's create-course response carried an invalid course identifier: %w",
			client.ErrUnexpectedResponse, err)
	}

	distance, _ := saved.DistanceMeter.Float64()
	gain, _ := saved.ElevationGainMeter.Float64()
	loss, _ := saved.ElevationLossMeter.Float64()
	activityTypeID, _ := saved.ActivityTypePk.Int64()
	name, _ := saved.CourseName.Value()

	return UploadedCourse{
		CourseID:            id,
		Name:                name,
		DistanceMeters:      distance,
		ElevationGainMeters: gain,
		ElevationLossMeters: loss,
		ActivityTypeID:      activityTypeID,
		Status:              status,
		URL:                 req.rc.Hosts().ConnectBase() + "/modern/course/" + id.String(),
	}, nil
}
