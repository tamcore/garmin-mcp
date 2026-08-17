package api

import (
	"context"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Courses reads and writes the course-management surface: get_courses,
// upload_course and delete_course.
//
// Source: the Taxuspt pinned curation at src/garmin_mcp/courses.py, which is
// the only source for this surface. The pinned upstream python-garminconnect
// 0.3.10 release exposes no course methods at all, so every field spelling
// here is cited to courses.py directly rather than to
// garminconnect/__init__.py.
//
// Every course this client reads or writes carries a route: a sequence of
// latitude/longitude points. That is location data tied to a person, so no
// model here is ever logged with its content, only its shape — see
// coursessensitive.go.
type Courses struct {
	req requester
}

// NewCourses returns a course-management client over the request layer.
func NewCourses(rc *client.Client) (*Courses, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Courses{req: req}, nil
}

// CourseActivityType is the nested activity-type object one course carries.
//
// Source: courses.py:192, `(c.get("activityType") or {}).get("typeKey")`.
type CourseActivityType struct {
	TypeKey client.Text `json:"typeKey"`
}

// Course is one entry in get_courses's listing.
//
// Source: courses.py:185-196, each field read via .get() from one object in
// the array GET "/course-service/course" answers:
//
//	"course_id": c.get("courseId"),
//	"name": c.get("courseName"),
//	"distance_m": c.get("distanceInMeters"),
//	"elevation_gain_m": c.get("elevationGainInMeters"),
//	"elevation_loss_m": c.get("elevationLossInMeters"),
//	"activity": (c.get("activityType") or {}).get("typeKey"),
//	"has_pace_band": c.get("hasPaceBand"),
//	"created": c.get("createdDateFormatted"),
//
// HasPaceBand is a pointer: courses.py's own `c.get("hasPaceBand")` reports
// upstream's real absence as Python None, never coerces it to false, and
// this port preserves that distinction rather than mapping "the field was
// missing or null" onto the same value as "the field was false".
type Course struct {
	CourseID            client.Number       `json:"courseId"`
	Name                client.Text         `json:"courseName"`
	DistanceMeters      client.Number       `json:"distanceInMeters"`
	ElevationGainMeters client.Number       `json:"elevationGainInMeters"`
	ElevationLossMeters client.Number       `json:"elevationLossInMeters"`
	ActivityType        *CourseActivityType `json:"activityType"`
	HasPaceBand         *bool               `json:"hasPaceBand"`
	Created             client.Text         `json:"createdDateFormatted"`
}

// GetCourses lists every course saved on Garmin Connect.
//
// Source: courses.py:179-198, GET "/course-service/course", answering a
// plain JSON array (`not isinstance(data, list)` guards against a
// differently-shaped error body, which this package's own typed decode
// already refuses closed).
func (c *Courses) GetCourses(ctx context.Context, session client.Session) ([]Course, error) {
	req := readRequest(client.OpGetCourses, client.EndpointCourseList, client.PathCourseBase, nil)
	var courses []Course
	if _, err := c.req.read(ctx, session, req, &courses); err != nil {
		return nil, err
	}
	return courses, nil
}

// DeleteCourse permanently removes one course.
//
// Its effect is EffectDelete: Garmin gives no guarantee that a rejected
// delete was not already applied, so the retry layer must never repeat it.
//
// Source: courses.py:288-308, DELETE "/course-service/course/{course_id}".
func (c *Courses) DeleteCourse(
	ctx context.Context, session client.Session, id client.ID,
) (WriteResult, error) {
	req := writeRequest(client.OpDeleteCourse, client.EndpointCourseDelete,
		http.MethodDelete, client.PathCourseBase+"/"+id.String(), client.EffectDelete)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}

	payload, err := c.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}
