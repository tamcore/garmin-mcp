package tools

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolDeleteWeighIns is the upstream compatibility name of the weigh-in delete.
const ToolDeleteWeighIns = "delete_weigh_ins"

// defaultDeleteAllWeighIns is the manifest default for delete_all: true. This is
// upstream's own behavior (weight_management.py:136, delete_all: bool = True) and
// this port keeps it rather than defaulting to the safer false, because changing
// a pinned tool's default is a compatibility break of its own. The description
// below states the consequence plainly instead.
const defaultDeleteAllWeighIns = true

// DeleteWeighInsResult is what delete_weigh_ins reports, matching the manifest's
// staticTopLevelKeys (date, deleted_count, message, status), with the same
// status-as-HTTP-int deviation weighinwrites.go documents for the add tools:
// upstream's own "status" is the literal string "success"
// (weight_management.py:147), never rendered here because every other write and
// delete result in this package reports status as the HTTP code Garmin answered
// with.
type DeleteWeighInsResult struct {
	Date         string `json:"date" jsonschema:"the calendar day weigh-ins were removed for"`
	DeletedCount int    `json:"deleted_count" jsonschema:"how many weigh-ins were actually removed"`
	Status       int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
	Message      string `json:"message" jsonschema:"a human-readable confirmation"`
}

// LogValue reports how many weigh-ins were removed, never their identifiers.
func (r DeleteWeighInsResult) LogValue() slog.Value {
	return shape("deleteWeighInsResult", slog.Int("deletedCount", r.DeletedCount))
}

// deleteWeighInsInput is the strict argument set.
type deleteWeighInsInput struct {
	Date      string `json:"date" jsonschema:"the calendar day to remove weigh-ins for, YYYY-MM-DD"`
	DeleteAll *bool  `json:"delete_all,omitempty" jsonschema:"whether to remove every weigh-in for the day, default true"`
}

func deleteWeighInsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDeleteWeighIns,
			Title: "Delete weigh-ins for one day",
			Description: "permanently remove weigh-ins Garmin holds for one calendar day. " +
				"delete_all defaults to true, and at that default EVERY weigh-in recorded " +
				"for the day is removed, not just one. Setting delete_all to false instead " +
				"requires the day to carry exactly one weigh-in, refusing a day with more " +
				"than one rather than guessing which to remove. A day with no weigh-ins is " +
				"not an error. It cannot be undone and it requires confirmation",
			Tier:        policy.TierDestructive,
			Category:    categoryHealth,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("date", "the calendar day to remove weigh-ins for"),
			Property{
				Name:        "delete_all",
				Types:       []string{typeBoolean},
				Description: "whether to remove every weigh-in for the day, rather than requiring exactly one",
				Default:     defaultDeleteAllWeighIns,
			},
		),
	}
}

// registerDeleteWeighIns registers the tool.
//
// Confirmation happens in the shared destructive-tier middleware
// (mcpserver/confirm.go), keyed off this tool's own registered description: the
// middleware asks before this handler ever runs, so no per-call weigh-in count
// can reach the confirmation prompt itself. The count this tool's own result
// reports (deleted_count) is the only place a caller can learn how many entries
// were actually removed.
func registerDeleteWeighIns(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deleteWeighInsInput) (
		*mcp.CallToolResult, DeleteWeighInsResult, error,
	) {
		out, err := svc.deleteWeighIns(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, deleteWeighInsContract().Registration(), handler)
}

// deleteWeighIns performs the removal behind the tool.
func (s *service) deleteWeighIns(ctx context.Context, in deleteWeighInsInput) (DeleteWeighInsResult, error) {
	date, err := parseCalendarDate("date", in.Date)
	if err != nil {
		return DeleteWeighInsResult{}, err
	}
	deleteAll := defaultDeleteAllWeighIns
	if in.DeleteAll != nil {
		deleteAll = *in.DeleteAll
	}
	session, err := s.session(ctx)
	if err != nil {
		return DeleteWeighInsResult{}, err
	}

	result, err := s.weight.DeleteWeighIns(ctx, session, date, deleteAll)
	if err != nil {
		return DeleteWeighInsResult{}, fail(err)
	}

	// A day with nothing to delete is not an error (weight_management.py's own
	// "no weigh-ins found" case is a success message, not a failure), so there is
	// no dispatched WriteResult to read a status from; http.StatusOK reports the
	// same "nothing left to do" outcome a caller would see from repeating the
	// call after a real deletion.
	status := http.StatusOK
	if last := len(result.Deleted); last > 0 {
		status = result.Deleted[last-1].Result.Status
	}
	return DeleteWeighInsResult{
		Date:         date.String(),
		DeletedCount: len(result.Deleted),
		Status:       status,
		Message:      "Weight measurements deleted for " + date.String() + ".",
	}, nil
}
