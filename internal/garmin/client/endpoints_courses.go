package client

// Course-management API-tier paths. Source: the Taxuspt pinned curation at
// src/garmin_mcp/courses.py, which is the only source for this surface: the
// pinned upstream python-garminconnect 0.3.10 release exposes no course
// methods at all, so every path here comes from courses.py's own
// garmin_client.client.connectapi/post/delete calls rather than from
// garminconnect/__init__.py.
const (
	// PathCourseBase lists courses on GET and creates one on POST. Source:
	// courses.py:180 (GET "/course-service/course") and :268-270 (POST the
	// same path to save a parsed course).
	PathCourseBase = "/course-service/course"

	// PathCourseImport parses an uploaded GPX file into a course skeleton.
	// Source: courses.py:241-252, POST "/course-service/course/import".
	PathCourseImport = "/course-service/course/import"
)

// Sanitized endpoint labels for the course-management tier. They never
// contain a host, a credential or a query string.
const (
	EndpointCourseList   = Endpoint("connectapi.course.list")
	EndpointCourseImport = Endpoint("connectapi.course.import")
	EndpointCourseCreate = Endpoint("connectapi.course.create")
	EndpointCourseDelete = Endpoint("connectapi.course.delete")
)

// courseEndpoints returns the course-management labels. A function, not a
// var: AGENTS.md allows no package-level mutable state.
func courseEndpoints() []Endpoint {
	return []Endpoint{
		EndpointCourseList,
		EndpointCourseImport,
		EndpointCourseCreate,
		EndpointCourseDelete,
	}
}

// Sanitized operation labels, one per course-management tool.
const (
	OpGetCourses   = Op("get_courses")
	OpUploadCourse = Op("upload_course")
	OpDeleteCourse = Op("delete_course")
)

// courseOps returns the course-management operations. A function for the
// same reason as courseEndpoints.
func courseOps() []Op {
	return []Op{
		OpGetCourses,
		OpUploadCourse,
		OpDeleteCourse,
	}
}
