package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolRequestReload is the upstream compatibility name.
const ToolRequestReload = "request_reload"

// ReloadRequest is the acknowledgement of one epoch reload.
//
// It reports what was asked for and the status Garmin answered with, and nothing else:
// the reply body of the write is the account's own wellness data, which no
// acknowledgement returns.
type ReloadRequest struct {
	Date      string `json:"date" jsonschema:"the calendar day whose epoch data was asked for"`
	Requested bool   `json:"requested" jsonschema:"whether Garmin accepted the request"`
	Status    int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
}

// LogValue reports that a reload was asked for, never for whom or what came back.
func (r ReloadRequest) LogValue() slog.Value {
	return shape("reloadRequest", slog.Int("status", r.Status))
}

// requestReloadInput is the strict argument set: one calendar day.
type requestReloadInput struct {
	Date string `json:"date" jsonschema:"the calendar day to reload, YYYY-MM-DD"`
}

func requestReloadContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolRequestReload,
			Title: "Request an epoch reload",
			Description: "ask Garmin to re-derive the epoch data of one calendar day. " +
				"Garmin offloads older days, and a read of one can come back empty until " +
				"it has been rebuilt. The request creates no record of its own and " +
				"repeating it converges on the same result, so it is safe to retry",
			Tier:     policy.TierWrite,
			Category: categoryHealth,
			// Idempotent: asking twice re-triggers the same server-side recompute for
			// the same day and creates no second record. This is the manifest's own
			// classification, and it is the same reading that lets the request layer
			// repeat the POST after a transient failure.
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to reload")),
	}
}

// registerRequestReload registers the tool.
func registerRequestReload(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in requestReloadInput) (
		*mcp.CallToolResult, ReloadRequest, error,
	) {
		out, err := svc.requestReload(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, requestReloadContract().Registration(), handler)
}

// requestReload performs the write behind the tool.
func (s *service) requestReload(ctx context.Context, date string) (ReloadRequest, error) {
	day, err := parseCalendarDate("date", date)
	if err != nil {
		return ReloadRequest{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return ReloadRequest{}, err
	}

	result, err := s.trends().RequestReload(ctx, session, day)
	if err != nil {
		return ReloadRequest{}, fail(err)
	}
	return newReloadRequest(day.String(), result), nil
}

// newReloadRequest maps the write result onto the bounded acknowledgement.
func newReloadRequest(date string, result api.WriteResult) ReloadRequest {
	return ReloadRequest{Date: date, Requested: true, Status: result.Status}
}
