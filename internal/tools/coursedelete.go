package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolDeleteCourse is the upstream compatibility name of the course delete.
const ToolDeleteCourse = "delete_course"

// argCourseID is the wire argument name, named once so the schema
// declaration, the struct tags and the parse call cannot drift to different
// spellings.
const argCourseID = "course_id"

// DeleteCourseResult is what delete_course reports, matching the manifest's
// staticTopLevelKeys (course_id, message, status).
type DeleteCourseResult struct {
	CourseID int64  `json:"course_id" jsonschema:"the course that was removed"`
	Status   int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
	Message  string `json:"message" jsonschema:"a human-readable confirmation"`
}

// LogValue reports that a removal happened, never which course it named.
func (r DeleteCourseResult) LogValue() slog.Value {
	return shape("deleteCourseResult", slog.Int("status", r.Status))
}

// deleteCourseInput is the strict argument set.
type deleteCourseInput struct {
	CourseID int64 `json:"course_id" jsonschema:"the course to delete, from get_courses"`
}

func deleteCourseContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDeleteCourse,
			Title: "Delete a course",
			Description: "permanently remove one course from Garmin Connect. It cannot be " +
				"undone and it requires confirmation",
			Tier:        policy.TierDestructive,
			Category:    categoryLocation,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(Property{
			Name: argCourseID, Types: []string{typeInteger},
			Description: "the course to delete, from get_courses",
			Minimum:     bound(1),
			Required:    true,
		}),
	}
}

// registerDeleteCourse registers the tool.
//
// Confirmation happens in the shared destructive-tier middleware
// (mcpserver/confirm.go), the same as every other destructive tool: it asks
// before this handler ever runs.
func registerDeleteCourse(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deleteCourseInput) (
		*mcp.CallToolResult, DeleteCourseResult, error,
	) {
		out, err := svc.deleteCourse(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, deleteCourseContract().Registration(), handler)
}

// deleteCourse performs the removal behind the tool.
func (s *service) deleteCourse(ctx context.Context, in deleteCourseInput) (DeleteCourseResult, error) {
	id, err := parseIdentifier(argCourseID, in.CourseID)
	if err != nil {
		return DeleteCourseResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return DeleteCourseResult{}, err
	}

	result, err := s.courses.DeleteCourse(ctx, session, id)
	if err != nil {
		return DeleteCourseResult{}, fail(err)
	}
	return DeleteCourseResult{
		CourseID: id.Int64(),
		Status:   result.Status,
		Message:  "Course deleted successfully.",
	}, nil
}
