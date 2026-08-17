package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolUploadCourse is the upstream compatibility name of the course upload.
const ToolUploadCourse = "upload_course"

// maxGPXContentArgumentLen bounds the gpx_content argument. It mirrors
// coursesupload.go's own maxCourseGPXBytes bound in internal/garmin/api: the
// domain client enforces the real ceiling regardless, but declaring the
// same bound here means an oversized argument is refused before its bytes
// are even handed to the domain client.
const maxGPXContentArgumentLen = 256 * 1024

// defaultCourseActivityType is the manifest default for activity_type.
// Source: compat/tools.json's upload_course inputSchema,
// "activity_type": {"type": "string", "default": "running"}.
const defaultCourseActivityType = "running"

// Argument names, named once so the schema declaration, the struct tags
// below and the parse calls cannot drift to different spellings.
const (
	argGPXContent        = "gpx_content"
	argCourseName        = "course_name"
	argCourseActivity    = "activity_type"
	argCourseDescription = "description"
)

// UploadCourseResult is what upload_course reports, matching the manifest's
// staticTopLevelKeys (activity_type_id, course_id, distance_m,
// elevation_gain_m, elevation_loss_m, name, status, url).
type UploadCourseResult struct {
	CourseID            int64   `json:"course_id" jsonschema:"the identifier Garmin assigned the saved course"`
	Name                string  `json:"name" jsonschema:"the course's name"`
	DistanceMeters      float64 `json:"distance_m" jsonschema:"the course distance in meters"`
	ElevationGainMeters float64 `json:"elevation_gain_m" jsonschema:"the course's total climb in meters"`
	ElevationLossMeters float64 `json:"elevation_loss_m" jsonschema:"the course's total descent in meters"`
	ActivityTypeID      int64   `json:"activity_type_id" jsonschema:"Garmin's course activity type id"`
	Status              int     `json:"status" jsonschema:"the HTTP status Garmin answered with"`
	URL                 string  `json:"url" jsonschema:"a web link to the saved course"`
}

// LogValue reports that a course was saved, never its name, its route or
// its share URL.
func (r UploadCourseResult) LogValue() slog.Value {
	return shape("uploadCourseResult",
		slog.Int("courseId", int(r.CourseID)),
		slog.Int("status", r.Status),
	)
}

// uploadCourseInput is the strict argument set.
//
// Deliberate deviation from compat/tools.json's own inputSchema: upstream's
// gpx_path takes an absolute filesystem path and reads the file itself
// (courses.py:222-238). This server accepts no caller-supplied filesystem
// path anywhere (see AGENTS.md's file discipline and docs/parity.md on
// download_activity_file and set_fit_download_dir), so gpx_content takes
// the file's own bytes as a UTF-8 string instead. That part of upstream
// does not port; the caller supplies content, never a path.
type uploadCourseInput struct {
	GPXContent string `json:"gpx_content" jsonschema:"the GPX file's own XML content"`
	// CourseName overrides the course name; defaults to the name parsed from the GPX.
	CourseName *string `json:"course_name,omitempty" jsonschema:"override the course name"`
	// ActivityType is the course activity type, default running.
	ActivityType *string `json:"activity_type,omitempty" jsonschema:"the course activity type"`
	// Description is shown on the course detail page.
	Description *string `json:"description,omitempty" jsonschema:"an optional description"`
}

// courseActivityTypeProperty declares the activity_type argument, with its
// closed enum sourced from api.CourseActivityTypeKeys rather than
// duplicated here.
func courseActivityTypeProperty() Property {
	enum := make([]any, 0, len(api.CourseActivityTypeKeys()))
	for _, key := range api.CourseActivityTypeKeys() {
		enum = append(enum, key)
	}
	return Property{
		Name:        argCourseActivity,
		Types:       []string{typeString},
		Description: "the course activity type",
		Enum:        enum,
		Default:     defaultCourseActivityType,
	}
}

func uploadCourseContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolUploadCourse,
			Title: "Upload a course",
			Description: "upload a GPX file's content as a Garmin Connect course. The course " +
				"can then be loaded onto a watch and used as a navigation course or to build " +
				"a PacePro strategy. Unlike the pinned upstream tool, this server takes the " +
				"GPX content directly rather than a filesystem path: it accepts no " +
				"caller-supplied path anywhere. Creates a new course every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryLocation,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			Property{
				Name:        argGPXContent,
				Types:       []string{typeString},
				Description: "the GPX file's own XML content",
				MaxLength:   new(maxGPXContentArgumentLen),
				Required:    true,
			},
			Property{
				Name:        argCourseName,
				Types:       []string{typeString},
				Description: "override the course name; defaults to the name parsed from the GPX",
				MaxLength:   new(maxNameArgumentLen),
				Nullable:    true,
			},
			courseActivityTypeProperty(),
			Property{
				Name:        argCourseDescription,
				Types:       []string{typeString},
				Description: "an optional description shown on the course detail page",
				MaxLength:   new(maxDescriptionArgumentLen),
				Nullable:    true,
			},
		),
	}
}

// registerUploadCourse registers the tool.
func registerUploadCourse(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in uploadCourseInput) (
		*mcp.CallToolResult, UploadCourseResult, error,
	) {
		out, err := svc.uploadCourse(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, uploadCourseContract().Registration(), handler)
}

// uploadCourse performs the write behind the tool.
func (s *service) uploadCourse(ctx context.Context, in uploadCourseInput) (UploadCourseResult, error) {
	gpx, err := parseXMLDocument(argGPXContent, in.GPXContent, maxGPXContentArgumentLen)
	if err != nil {
		return UploadCourseResult{}, err
	}
	if gpx == "" {
		return UploadCourseResult{}, invalidArgument(argGPXContent + " must not be empty")
	}

	activityType := defaultCourseActivityType
	if in.ActivityType != nil {
		activityType = *in.ActivityType
	}
	courseName, err := optionalNullableText(argCourseName, in.CourseName, maxNameArgumentLen)
	if err != nil {
		return UploadCourseResult{}, err
	}
	description, err := optionalNullableText(argCourseDescription, in.Description, maxDescriptionArgumentLen)
	if err != nil {
		return UploadCourseResult{}, err
	}

	session, err := s.session(ctx)
	if err != nil {
		return UploadCourseResult{}, err
	}

	result, err := s.courses.UploadCourse(ctx, session, api.CourseUpload{
		GPX:          []byte(gpx),
		Name:         courseName,
		ActivityType: activityType,
		Description:  description,
	})
	if err != nil {
		return UploadCourseResult{}, fail(err)
	}
	return UploadCourseResult{
		CourseID:            result.CourseID.Int64(),
		Name:                result.Name,
		DistanceMeters:      result.DistanceMeters,
		ElevationGainMeters: result.ElevationGainMeters,
		ElevationLossMeters: result.ElevationLossMeters,
		ActivityTypeID:      result.ActivityTypeID,
		Status:              result.Status,
		URL:                 result.URL,
	}, nil
}

// optionalNullableText resolves a nullable optional text argument to its
// trimmed value, or "" when absent or null.
func optionalNullableText(field string, value *string, limit int) (string, error) {
	if value == nil {
		return "", nil
	}
	return parseText(field, *value, limit)
}
