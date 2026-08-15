package tools

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The names of the workout writes. update_workout has no entry in the pinned
// manifest: it comes from an unmerged upstream proposal and is an addition.
const (
	ToolUploadWorkout  = "upload_workout"
	ToolUploadWorkouts = "upload_workouts"
	ToolUpdateWorkout  = "update_workout"
)

// A SavedWorkoutResult reports what Garmin saved.
//
// The identifier and the name are the server's, not the caller's: Garmin normalizes
// a name and assigns an id, and reporting what was sent instead of what was saved is
// how a caller ends up scheduling a workout that does not exist.
type SavedWorkoutResult struct {
	WorkoutID int64  `json:"workout_id" jsonschema:"the identifier Garmin saved the workout under"`
	Name      string `json:"name,omitempty" jsonschema:"the name Garmin saved, which may differ"`
}

// LogValue reports that a workout was saved, never what was saved.
func (r SavedWorkoutResult) LogValue() slog.Value {
	return shape("savedWorkout", slog.String(argNameName, presence(r.Name != "")))
}

// newSavedWorkoutResult prefers what Garmin returned over what the caller sent.
func newSavedWorkoutResult(saved api.SavedWorkout) (SavedWorkoutResult, error) {
	id, err := saved.ID()
	if err != nil {
		return SavedWorkoutResult{}, fail(err)
	}
	name, _ := saved.Name()
	return SavedWorkoutResult{WorkoutID: id.Int64(), Name: name}, nil
}

// workoutDataProperty declares one caller-supplied workout document.
func workoutDataProperty(name, description string) Property {
	return Property{
		Name:        name,
		Types:       []string{typeObject},
		Description: description,
		Required:    true,
	}
}

// uploadWorkoutInput is the single-upload argument set. The document is carried
// verbatim: Garmin owns the workout schema and it drifts, so this server validates
// that it is a well-formed, bounded JSON object and nothing more.
type uploadWorkoutInput struct {
	WorkoutData json.RawMessage `json:"workout_data" jsonschema:"the Garmin workout document"`
}

func uploadWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolUploadWorkout,
			Title: "Upload a workout",
			Description: "create one workout in Garmin Connect from a Garmin workout " +
				"document. It creates a new workout every time it is called, so a repeat " +
				"after a transport failure creates a second workout: read get_workouts " +
				"before calling again",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(workoutDataProperty(
			"workout_data", "the Garmin workout document, with its segments and steps")),
	}
}

func registerUploadWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in uploadWorkoutInput) (
		*mcp.CallToolResult, SavedWorkoutResult, error,
	) {
		document, err := parseWorkoutDocument("workout_data", in.WorkoutData)
		if err != nil {
			return nil, SavedWorkoutResult{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, SavedWorkoutResult{}, err
		}

		saved, err := svc.workouts.Upload(ctx, session, document)
		if err != nil {
			return nil, SavedWorkoutResult{}, fail(err)
		}
		result, err := newSavedWorkoutResult(saved)
		return nil, result, err
	}
	return mcpserver.AddTool(registry, uploadWorkoutContract().Registration(), handler)
}

// uploadWorkoutsInput is the batch-upload argument set.
type uploadWorkoutsInput struct {
	Workouts []json.RawMessage `json:"workouts" jsonschema:"the Garmin workout documents"`
}

// An UploadBatchResult reports one batch upload.
type UploadBatchResult struct {
	Saved     []SavedWorkoutResult `json:"saved" jsonschema:"the workouts Garmin saved, in request order"`
	Failures  []BatchOutcome       `json:"failures" jsonschema:"the items Garmin did not save"`
	Requested int                  `json:"requested" jsonschema:"how many documents were requested"`
}

// LogValue reports the batch counts, never a workout.
func (r UploadBatchResult) LogValue() slog.Value {
	return shape("uploadBatch",
		slog.Int("requested", r.Requested),
		slog.Int("saved", len(r.Saved)),
	)
}

func uploadWorkoutsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolUploadWorkouts,
			Title: "Upload several workouts",
			Description: "create several workouts in one call. Each document is uploaded on " +
				"its own, so one rejection does not abandon the rest, and every item is " +
				"reported separately. It creates new workouts every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(Property{
			Name:        "workouts",
			Types:       []string{typeArray},
			Description: "the Garmin workout documents, each shaped like upload_workout",
			Items:       map[string]any{keyType: typeObject},
			MinItems:    new(1),
			MaxItems:    new(DefaultMaxBatchItems),
			Required:    true,
		}),
	}
}

func registerUploadWorkouts(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in uploadWorkoutsInput) (
		*mcp.CallToolResult, UploadBatchResult, error,
	) {
		documents, err := parseWorkoutDocuments(in.Workouts, svc.bounds.MaxBatchItems)
		if err != nil {
			return nil, UploadBatchResult{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, UploadBatchResult{}, err
		}
		return nil, svc.uploadEach(ctx, session, documents), nil
	}
	return mcpserver.AddTool(registry, uploadWorkoutsContract().Registration(), handler)
}

// uploadEach loops the single-document upload, reporting each item separately.
func (s *service) uploadEach(
	ctx context.Context, session client.Session, documents []api.WorkoutDocument,
) UploadBatchResult {
	saved := make([]SavedWorkoutResult, 0, len(documents))
	failures := make([]BatchOutcome, 0)

	for index, document := range documents {
		stored, err := s.workouts.Upload(ctx, session, document)
		if err != nil {
			failures = append(failures, failedOutcome(int64(index), err))
			continue
		}
		result, err := newSavedWorkoutResult(stored)
		if err != nil {
			failures = append(failures, failedOutcome(int64(index), err))
			continue
		}
		saved = append(saved, result)
	}
	return UploadBatchResult{Saved: saved, Failures: failures, Requested: len(documents)}
}

// updateWorkoutInput is the in-place update argument set.
type updateWorkoutInput struct {
	WorkoutID   int64           `json:"workout_id" jsonschema:"the workout to replace in place"`
	WorkoutData json.RawMessage `json:"workout_data" jsonschema:"the replacement workout document"`
}

func updateWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolUpdateWorkout,
			Title: "Update a workout in place",
			Description: "replace one workout's content while keeping its identifier, so any " +
				"calendar entry that already points at it stays valid. The identifier in the " +
				"document is forced to match the one in the path, and the saved name and " +
				"identifier Garmin reports back are what this tool returns",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(
			workoutIDIntegerProperty(),
			workoutDataProperty("workout_data",
				"the replacement workout document, complete rather than partial"),
		),
	}
}

func registerUpdateWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in updateWorkoutInput) (
		*mcp.CallToolResult, SavedWorkoutResult, error,
	) {
		document, err := parseWorkoutDocument("workout_data", in.WorkoutData)
		if err != nil {
			return nil, SavedWorkoutResult{}, err
		}
		id, session, err := svc.resolveWorkoutRead(ctx, in.WorkoutID)
		if err != nil {
			return nil, SavedWorkoutResult{}, err
		}

		saved, err := svc.workouts.Update(ctx, session, id, document)
		if err != nil {
			return nil, SavedWorkoutResult{}, fail(err)
		}
		result, err := newSavedWorkoutResult(saved)
		return nil, result, err
	}
	return mcpserver.AddTool(registry, updateWorkoutContract().Registration(), handler)
}

// parseWorkoutDocument validates one caller-supplied document.
func parseWorkoutDocument(field string, raw json.RawMessage) (api.WorkoutDocument, error) {
	switch {
	case len(raw) == 0, string(raw) == jsonNull:
		return api.WorkoutDocument{}, invalidArgument(field + " is required")
	case len(raw) > api.MaxRequestBodyBytes:
		return api.WorkoutDocument{}, invalidArgument(
			field + " is larger than this server will send")
	}

	document, err := api.ParseWorkoutDocument(raw)
	if err != nil {
		return api.WorkoutDocument{}, invalidArgument(
			field + " must be a complete Garmin workout document")
	}
	if !document.IsObject() {
		return api.WorkoutDocument{}, invalidArgument(field + " must be a JSON object")
	}
	return document, nil
}

// parseWorkoutDocuments validates a bounded batch of documents.
func parseWorkoutDocuments(raw []json.RawMessage, limit int) ([]api.WorkoutDocument, error) {
	if err := boundedCount("workouts", len(raw), limit); err != nil {
		return nil, err
	}
	out := make([]api.WorkoutDocument, 0, len(raw))
	for _, entry := range raw {
		document, err := parseWorkoutDocument("workouts", entry)
		if err != nil {
			return nil, err
		}
		out = append(out, document)
	}
	return out, nil
}
