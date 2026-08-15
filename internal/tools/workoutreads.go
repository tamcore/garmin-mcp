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

// The upstream compatibility names of the workout reads.
const (
	ToolGetWorkouts     = "get_workouts"
	ToolGetWorkoutByID  = "get_workout_by_id"
	ToolDownloadWorkout = "download_workout"
)

// Workout read constants.
const (
	workoutListPageSize  = 100
	workoutFITMediaType  = "application/vnd.ant.fit"
	workoutResourceStart = "garmin://workout/"
)

// A WorkoutEntry is one summary of the workout library. It is health material: a
// workout describes a person's training.
type WorkoutEntry struct {
	WorkoutID    int64   `json:"workout_id" jsonschema:"the workout identifier"`
	Name         *string `json:"name,omitempty" jsonschema:"the workout name"`
	Description  *string `json:"description,omitempty" jsonschema:"the workout description"`
	SportType    *string `json:"sport_type,omitempty" jsonschema:"the sport key, for example running"`
	EstimatedSec *int    `json:"estimated_seconds,omitempty" jsonschema:"the estimated duration"`
	UpdatedDate  *string `json:"updated_date,omitempty" jsonschema:"when the workout last changed"`
}

// A WorkoutList is the bounded workout library listing.
type WorkoutList struct {
	Workouts  []WorkoutEntry `json:"workouts" jsonschema:"the workout summaries, as Garmin ordered them"`
	Count     int            `json:"count" jsonschema:"how many summaries this result carries"`
	Truncated bool           `json:"truncated" jsonschema:"whether the listing was cut at this server's bound"`
}

// LogValue reports the listing size, never a workout.
func (l WorkoutList) LogValue() slog.Value {
	return shape("workoutList",
		slog.Int("workouts", len(l.Workouts)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getWorkoutsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetWorkouts,
			Title: "Get the workout library",
			Description: "read the account's workout library as summaries. It pages through " +
				"Garmin and stops at this server's bound, reporting whether the listing was " +
				"cut. For a workout's steps, call get_workout_by_id",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

func registerGetWorkouts(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, WorkoutList, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, WorkoutList{}, err
		}

		summaries, truncated, err := svc.listWorkouts(ctx, session)
		if err != nil {
			return nil, WorkoutList{}, err
		}
		return nil, newWorkoutList(summaries, truncated), nil
	}
	return mcpserver.AddTool(registry, getWorkoutsContract().Registration(), handler)
}

// listWorkouts walks the library a page at a time and stops at the configured bound.
//
// The bound is reported rather than hidden: a caller that sees truncated set knows
// the library is larger than this server will return in one call.
func (s *service) listWorkouts(ctx context.Context, session client.Session) (
	[]api.WorkoutSummary, bool, error,
) {
	size := min(workoutListPageSize, s.limits.MaxPageSize)
	collected := make([]api.WorkoutSummary, 0, size)

	for start := 0; len(collected) < s.bounds.MaxWorkouts; start += size {
		page, err := client.NewPage(start, size)
		if err != nil {
			return nil, false, fail(err)
		}
		batch, err := s.workouts.List(ctx, session, page)
		if err != nil {
			return nil, false, fail(err)
		}
		collected = append(collected, batch...)
		if len(batch) < size {
			return collected, false, nil
		}
	}
	return collected, true, nil
}

func newWorkoutList(summaries []api.WorkoutSummary, truncated bool) WorkoutList {
	if len(summaries) > DefaultMaxWorkouts {
		summaries = summaries[:DefaultMaxWorkouts]
		truncated = true
	}
	out := make([]WorkoutEntry, 0, len(summaries))
	for _, summary := range summaries {
		id, _ := summary.WorkoutID.Int64()
		out = append(out, WorkoutEntry{
			WorkoutID:    id,
			Name:         summary.WorkoutName,
			Description:  summary.Description,
			SportType:    sportKeyOf(summary.SportType),
			EstimatedSec: optionalInt(summary.EstimatedSec),
			UpdatedDate:  summary.UpdatedDate,
		})
	}
	return WorkoutList{Workouts: out, Count: len(out), Truncated: truncated}
}

// workoutIDInput is the argument set of the single-workout read.
type workoutIDInput struct {
	WorkoutID any `json:"workout_id" jsonschema:"the Garmin workout identifier"`
}

// A WorkoutDetail is one workout template with its step structure.
//
// The segments keep Garmin's own shape, because Garmin owns that schema and changes
// it: reshaping it here would drop steps this server has never heard of.
type WorkoutDetail struct {
	WorkoutID int64   `json:"workout_id" jsonschema:"the workout identifier"`
	Name      *string `json:"name,omitempty" jsonschema:"the workout name"`
	SportType *string `json:"sport_type,omitempty" jsonschema:"the sport key"`
	Segments  any     `json:"segments,omitempty" jsonschema:"the segments, in Garmin's own shape"`
}

// LogValue reports that a workout was read, never its steps.
func (d WorkoutDetail) LogValue() slog.Value {
	return shape("workoutDetail", slog.String("segments", presence(d.Segments != nil)))
}

func getWorkoutByIDContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetWorkoutByID,
			Title: "Get one workout",
			Description: "read one workout template with its segments and step structure. " +
				"The identifier is the numeric workout id from get_workouts; the UUID form " +
				"that adaptive Garmin Coach plans use is not served by this server",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(workoutIDProperty()),
	}
}

func registerGetWorkoutByID(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in workoutIDInput) (
		*mcp.CallToolResult, WorkoutDetail, error,
	) {
		id, session, err := svc.resolveWorkoutRead(ctx, in.WorkoutID)
		if err != nil {
			return nil, WorkoutDetail{}, err
		}

		workout, err := svc.workouts.Get(ctx, session, id)
		if err != nil {
			return nil, WorkoutDetail{}, fail(err)
		}
		return nil, newWorkoutDetail(id, workout), nil
	}
	return mcpserver.AddTool(registry, getWorkoutByIDContract().Registration(), handler)
}

func newWorkoutDetail(id client.ID, workout api.Workout) WorkoutDetail {
	return WorkoutDetail{
		WorkoutID: id.Int64(),
		Name:      workout.WorkoutName,
		SportType: sportKeyOf(workout.SportType),
		Segments:  decodeRaw(workout.Segments),
	}
}

// sportKeyOf reads the sport key out of the object Garmin wraps it in. It is a
// different field name from the activity API's, which is why typeKeyOf does not do.
func sportKeyOf(raw json.RawMessage) *string {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil
	}
	var nested struct {
		SportTypeKey *string `json:"sportTypeKey"`
	}
	if err := json.Unmarshal(raw, &nested); err == nil && nested.SportTypeKey != nil {
		return nested.SportTypeKey
	}
	return typeKeyOf(raw)
}

// decodeRaw renders a retained raw sub-object as plain JSON data, so the published
// output schema describes it as an open value rather than as a byte array.
func decodeRaw(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	return decoded
}

// downloadWorkoutInput is the argument set of the FIT download.
type downloadWorkoutInput struct {
	WorkoutID int64 `json:"workout_id" jsonschema:"the Garmin workout identifier"`
}

func downloadWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDownloadWorkout,
			Title: "Download a workout as FIT",
			Description: "download one workout's FIT encoding and return it as an embedded " +
				"MCP resource. Nothing is written to this server's filesystem and no path is " +
				"accepted; a file over this server's bound is refused rather than truncated",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(workoutIDIntegerProperty()),
	}
}

func registerDownloadWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in downloadWorkoutInput) (
		*mcp.CallToolResult, DownloadedFile, error,
	) {
		id, session, err := svc.resolveWorkoutRead(ctx, in.WorkoutID)
		if err != nil {
			return nil, DownloadedFile{}, err
		}

		sink := newBoundedSink(svc.bounds.MaxDownloadBytes)
		_, transferErr := svc.workouts.Download(ctx, session, id, sink)
		// The sink is asked first: it aborts the copy, so its own refusal is the
		// cause of the transfer error and is the one worth reporting.
		if err := sink.err(); err != nil {
			return nil, DownloadedFile{}, err
		}
		if transferErr != nil {
			return nil, DownloadedFile{}, fail(transferErr)
		}

		uri := workoutResourceStart + id.String() + ".fit"
		file := newDownloadedFile(id, "fit", workoutFITMediaType, uri, sink.len())
		return blobResult(uri, workoutFITMediaType, sink.bytes()), file, nil
	}
	return mcpserver.AddTool(registry, downloadWorkoutContract().Registration(), handler)
}

// resolveWorkoutRead validates the workout identifier and then resolves the session.
func (s *service) resolveWorkoutRead(ctx context.Context, raw any) (
	client.ID, client.Session, error,
) {
	id, err := parseIdentifier(argNameWorkoutID, raw)
	if err != nil {
		return client.ID{}, client.Session{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return client.ID{}, client.Session{}, err
	}
	return id, session, nil
}

// resolveActivityRead validates the activity identifier and then resolves the
// session, which is the shape every single-activity read shares.
func (s *service) resolveActivityRead(ctx context.Context, raw any) (
	client.ID, client.Session, error,
) {
	id, err := parseActivityIdentifier(raw)
	if err != nil {
		return client.ID{}, client.Session{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return client.ID{}, client.Session{}, err
	}
	return id, session, nil
}
