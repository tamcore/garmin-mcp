package tools

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetActivitiesByDate is the upstream compatibility name of the date-window tool.
const ToolGetActivitiesByDate = "get_activities_by_date"

// DateWindow is the inclusive window a result came from, echoed back so a caller can
// see exactly what was read.
type DateWindow struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`
}

// ActivityWindow is one page of the activities inside a date window.
//
// The keys are upstream's: activities, count, date_range, has_more, page and
// page_size, plus next_page while more pages remain.
type ActivityWindow struct {
	Activities []ActivitySummary `json:"activities" jsonschema:"the activities on this page, newest first"`
	Count      int               `json:"count" jsonschema:"how many activities this page carries"`
	DateRange  DateWindow        `json:"date_range" jsonschema:"the window that was read"`
	HasMore    bool              `json:"has_more" jsonschema:"whether another page follows"`
	Page       int               `json:"page" jsonschema:"the zero-based page index this result is"`
	PageSize   int               `json:"page_size" jsonschema:"the page size applied"`
	NextPage   *int              `json:"next_page,omitempty" jsonschema:"the page to request next"`
}

// LogValue reports the window shape, never the activities in it.
func (w ActivityWindow) LogValue() slog.Value {
	return shape("activityWindow",
		slog.Int("activities", len(w.Activities)),
		slog.Int("page", w.Page),
		slog.Int("pageSize", w.PageSize),
		slog.Bool("hasMore", w.HasMore),
	)
}

// getActivitiesByDateInput is the strict argument set.
type getActivitiesByDateInput struct {
	StartDate    string `json:"start_date" jsonschema:"inclusive first day, YYYY-MM-DD"`
	EndDate      string `json:"end_date" jsonschema:"inclusive last day, YYYY-MM-DD"`
	ActivityType string `json:"activity_type,omitempty" jsonschema:"optional lowercase activity key"`
	Page         *int   `json:"page,omitempty" jsonschema:"zero-based page index, default 0"`
	PageSize     *int   `json:"page_size,omitempty" jsonschema:"activities per page, 1 to 200, default 100"`
}

func getActivitiesByDateContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivitiesByDate,
			Title: "Get activities by date",
			Description: "read the account's activities inside an inclusive date window, " +
				"newest first, one page at a time. When has_more is true, call again with " +
				"page set to next_page. The window is bounded by this server; narrow it if " +
				"the call is refused. Start coordinates are not returned",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("start_date", "inclusive first day of the window"),
			dateProperty("end_date", "inclusive last day of the window"),
			Property{
				Name:        "activity_type",
				Types:       []string{typeString},
				Description: "optional lowercase Garmin activity key, for example running",
				Pattern:     `^[a-z0-9_]*$`,
				MaxLength:   new(maxActivityTypeArgumentLen),
				Default:     "",
			},
			Property{
				Name:        "page",
				Types:       []string{typeInteger},
				Description: "zero-based page index inside the window",
				Minimum:     bound(0),
				Maximum:     bound(DefaultMaxWindowPage),
				Default:     defaultWindowPage,
			},
			Property{
				Name:        "page_size",
				Types:       []string{typeInteger},
				Description: "how many activities one page carries",
				Minimum:     bound(1),
				Maximum:     bound(DefaultMaxWindowPageSize),
				Default:     defaultWindowPageSize,
			},
		),
	}
}

// dateProperty declares a calendar-date argument, which is the same strict shape
// everywhere it appears.
func dateProperty(name, description string) Property {
	return Property{
		Name:        name,
		Types:       []string{typeString},
		Description: description + ", in YYYY-MM-DD form",
		Format:      "date",
		Pattern:     `^\d{4}-\d{2}-\d{2}$`,
		MaxLength:   new(maxDateArgumentLen),
		Required:    true,
	}
}

// registerGetActivitiesByDate registers the tool.
func registerGetActivitiesByDate(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getActivitiesByDateInput) (
		*mcp.CallToolResult, ActivityWindow, error,
	) {
		request, err := svc.parseWindowRequest(in)
		if err != nil {
			return nil, ActivityWindow{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, ActivityWindow{}, err
		}
		window, err := svc.readWindow(ctx, session, request)
		if err != nil {
			return nil, ActivityWindow{}, err
		}
		return nil, window, nil
	}
	return mcpserver.AddTool(registry, getActivitiesByDateContract().Registration(), handler)
}

// windowRequest is the validated request one call describes.
type windowRequest struct {
	span     client.DateRange
	filter   api.ActivityType
	page     int
	pageSize int
}

// parseWindowRequest validates every argument before anything is dispatched.
func (s *service) parseWindowRequest(in getActivitiesByDateInput) (windowRequest, error) {
	span, err := parseWindow(in.StartDate, in.EndDate, s.limits)
	if err != nil {
		return windowRequest{}, err
	}
	filter, err := parseActivityTypeFilter(in.ActivityType)
	if err != nil {
		return windowRequest{}, err
	}
	page, pageSize, err := resolveWindowPagination(in.Page, in.PageSize)
	if err != nil {
		return windowRequest{}, err
	}
	return windowRequest{span: span, filter: filter, page: page, pageSize: pageSize}, nil
}

// readWindow reads the whole bounded window and then hands back the requested page.
//
// The window is read in full because Garmin cannot report a window's total without
// walking it, and a caller needs has_more to be honest. The read is bounded three
// times over: by the date window, by the request layer's page count, and by
// MaxWindowActivities here. Passing the bound is refused rather than truncated,
// because silently dropping part of a health history is worse than an actionable
// refusal.
func (s *service) readWindow(
	ctx context.Context, session client.Session, request windowRequest,
) (ActivityWindow, error) {
	all, err := s.activities.ListByDate(ctx, session, api.DateQuery{
		Span:     request.span,
		Type:     request.filter,
		Sort:     api.SortDescending,
		PageSize: s.limits.MaxPageSize,
	})
	if err != nil {
		return ActivityWindow{}, fail(err)
	}
	if len(all) > s.bounds.MaxWindowActivities {
		return ActivityWindow{}, tooLarge("the window holds more than " +
			strconv.Itoa(s.bounds.MaxWindowActivities) +
			" activities, so narrow the date window and read it in parts")
	}
	return newActivityWindow(all, request), nil
}

// newActivityWindow slices the requested page out of the window result.
func newActivityWindow(all []api.Activity, request windowRequest) ActivityWindow {
	offset := request.page * request.pageSize
	var items []api.Activity
	if offset < len(all) {
		items = all[offset:min(offset+request.pageSize, len(all))]
	}

	hasMore := offset+len(items) < len(all)
	window := ActivityWindow{
		Activities: newActivitySummaries(items),
		Count:      len(items),
		DateRange: DateWindow{
			StartDate: request.span.Start().String(),
			EndDate:   request.span.End().String(),
		},
		HasMore:  hasMore,
		Page:     request.page,
		PageSize: request.pageSize,
	}
	if hasMore {
		next := request.page + 1
		window.NextPage = &next
	}
	return window
}
