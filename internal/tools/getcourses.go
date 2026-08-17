package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetCourses is the upstream compatibility name of the course listing.
const ToolGetCourses = "get_courses"

// maxCourses bounds the returned course listing. Not one of deps.go's Bounds
// fields: this is the one tool in this slice that reads a list, and a local
// bound keeps deps.go's shared Bounds struct untouched.
const maxCourses = 500

// CourseSummary is one course get_courses reports, matching
// compat/tools.json's curated shape.
type CourseSummary struct {
	CourseID            *int64   `json:"course_id,omitempty" jsonschema:"the course identifier"`
	Name                *string  `json:"name,omitempty" jsonschema:"the course name"`
	DistanceMeters      *float64 `json:"distance_m,omitempty" jsonschema:"the course distance in meters"`
	ElevationGainMeters *float64 `json:"elevation_gain_m,omitempty" jsonschema:"the course's total climb in meters"`
	ElevationLossMeters *float64 `json:"elevation_loss_m,omitempty" jsonschema:"the course's total descent in meters"`
	Activity            *string  `json:"activity,omitempty" jsonschema:"the course's activity type key"`
	// HasPaceBand is nullable: Garmin's own listing can omit or null the
	// field, and this tool reports that absence as absence rather than
	// coercing it to false.
	HasPaceBand *bool   `json:"has_pace_band,omitempty" jsonschema:"whether the course carries a PacePro pace band"`
	Created     *string `json:"created,omitempty" jsonschema:"when the course was created"`
}

// CourseList is the bounded course listing.
type CourseList struct {
	Count     int             `json:"count" jsonschema:"how many courses this result carries"`
	Courses   []CourseSummary `json:"courses" jsonschema:"the account's saved courses"`
	Truncated bool            `json:"truncated" jsonschema:"whether the list was cut at this server's bound"`
}

// LogValue reports the course count, never a name, a distance or a route.
func (l CourseList) LogValue() slog.Value {
	return shape("courseList",
		slog.Int("courses", len(l.Courses)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getCoursesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetCourses,
			Title: "List courses",
			Description: "list every course saved on Garmin Connect: identifier, name, " +
				"distance, activity type and creation date. Takes no arguments. An " +
				"account with no saved course returns an empty list, which is a normal state",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetCourses registers the tool.
func registerGetCourses(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, CourseList, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, CourseList{}, err
		}
		courses, err := svc.courses.GetCourses(ctx, session)
		if err != nil {
			return nil, CourseList{}, fail(err)
		}
		return nil, newCourseList(courses, maxCourses), nil
	}
	return mcpserver.AddTool(registry, getCoursesContract().Registration(), handler)
}

// newCourseList maps the domain models onto the bounded result. A course
// list is truncated rather than refused, the same choice newDeviceList
// documents.
func newCourseList(courses []api.Course, limit int) CourseList {
	truncated := len(courses) > limit
	if truncated {
		courses = courses[:limit]
	}

	out := make([]CourseSummary, 0, len(courses))
	for _, course := range courses {
		out = append(out, newCourseSummary(course))
	}
	return CourseList{Count: len(out), Courses: out, Truncated: truncated}
}

// newCourseSummary maps one domain course onto its curated summary.
func newCourseSummary(course api.Course) CourseSummary {
	summary := CourseSummary{HasPaceBand: course.HasPaceBand}
	if id, ok := course.CourseID.Int64Exact(); ok {
		summary.CourseID = &id
	}
	if name, ok := course.Name.Value(); ok {
		summary.Name = &name
	}
	if distance, ok := course.DistanceMeters.Float64(); ok {
		summary.DistanceMeters = &distance
	}
	if gain, ok := course.ElevationGainMeters.Float64(); ok {
		summary.ElevationGainMeters = &gain
	}
	if loss, ok := course.ElevationLossMeters.Float64(); ok {
		summary.ElevationLossMeters = &loss
	}
	if course.ActivityType != nil {
		if key, ok := course.ActivityType.TypeKey.Value(); ok {
			summary.Activity = &key
		}
	}
	if created, ok := course.Created.Value(); ok {
		summary.Created = &created
	}
	return summary
}
