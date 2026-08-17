package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolSetBloodPressure is the upstream compatibility name of the tool.
const ToolSetBloodPressure = "set_blood_pressure"

// Blood pressure argument bounds. Source: garminconnect/__init__.py:1397-1403,
// the same range set_blood_pressure itself enforces before dispatch.
const (
	minSystolicArgument, maxSystolicArgument   = 70, 260
	minDiastolicArgument, maxDiastolicArgument = 40, 150
	minPulseArgument, maxPulseArgument         = 20, 250

	maxBloodPressureNotesLen = api.MaxTextLen
)

// Argument names, named once so the schema declaration and the struct tags
// below cannot drift to different spellings.
const (
	argSystolic  = "systolic"
	argDiastolic = "diastolic"
	argPulse     = "pulse"
)

// SetBloodPressureResult is what set_blood_pressure reports. Upstream's own
// outputShape carries no static top-level keys, so this echoes the
// caller's own reading back, plus the HTTP status, matching every other
// otherwise-uncurated write in this package.
type SetBloodPressureResult struct {
	Systolic  int    `json:"systolic" jsonschema:"the systolic pressure that was recorded"`
	Diastolic int    `json:"diastolic" jsonschema:"the diastolic pressure that was recorded"`
	Pulse     int    `json:"pulse" jsonschema:"the pulse rate that was recorded"`
	Status    int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
	Message   string `json:"message" jsonschema:"a human-readable confirmation"`
}

// LogValue reports that a write happened, never the reading or the notes.
func (r SetBloodPressureResult) LogValue() slog.Value {
	return shape("setBloodPressureResult", slog.Int("status", r.Status))
}

// setBloodPressureInput is the strict argument set.
type setBloodPressureInput struct {
	Systolic  int64   `json:"systolic" jsonschema:"the systolic pressure (top number)"`
	Diastolic int64   `json:"diastolic" jsonschema:"the diastolic pressure (bottom number)"`
	Pulse     int64   `json:"pulse" jsonschema:"the pulse rate"`
	Notes     *string `json:"notes,omitempty" jsonschema:"optional notes"`
}

func setBloodPressureContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetBloodPressure,
			Title: "Set blood pressure",
			Description: "record a blood-pressure reading: systolic, diastolic and pulse, " +
				"with an optional note. Creates a new reading every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			Property{
				Name: argSystolic, Types: []string{typeInteger},
				Description: "the systolic pressure (top number)",
				Minimum:     bound(minSystolicArgument), Maximum: bound(maxSystolicArgument),
				Required: true,
			},
			Property{
				Name: argDiastolic, Types: []string{typeInteger},
				Description: "the diastolic pressure (bottom number)",
				Minimum:     bound(minDiastolicArgument), Maximum: bound(maxDiastolicArgument),
				Required: true,
			},
			Property{
				Name: argPulse, Types: []string{typeInteger},
				Description: "the pulse rate",
				Minimum:     bound(minPulseArgument), Maximum: bound(maxPulseArgument),
				Required: true,
			},
			Property{
				Name:        "notes",
				Types:       []string{typeString},
				Description: "optional notes",
				MaxLength:   new(maxBloodPressureNotesLen),
				Nullable:    true,
			},
		),
	}
}

// registerSetBloodPressure registers the tool.
func registerSetBloodPressure(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setBloodPressureInput) (
		*mcp.CallToolResult, SetBloodPressureResult, error,
	) {
		out, err := svc.setBloodPressure(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, setBloodPressureContract().Registration(), handler)
}

// setBloodPressure performs the write behind the tool. The reading is
// recorded at the current instant, matching set_blood_pressure's own
// `timestamp: str = ""` default (garminconnect/__init__.py:1380,1385).
func (s *service) setBloodPressure(
	ctx context.Context, in setBloodPressureInput,
) (SetBloodPressureResult, error) {
	notes := ""
	if in.Notes != nil {
		trimmed, err := parseText("notes", *in.Notes, maxBloodPressureNotesLen)
		if err != nil {
			return SetBloodPressureResult{}, err
		}
		notes = trimmed
	}
	session, err := s.session(ctx)
	if err != nil {
		return SetBloodPressureResult{}, err
	}

	result, err := s.dataManagement.SetBloodPressure(ctx, session, api.BloodPressureEntry{
		Systolic: int(in.Systolic), Diastolic: int(in.Diastolic), Pulse: int(in.Pulse),
		Notes: notes, At: s.now(),
	})
	if err != nil {
		return SetBloodPressureResult{}, fail(err)
	}
	return SetBloodPressureResult{
		Systolic: int(in.Systolic), Diastolic: int(in.Diastolic), Pulse: int(in.Pulse),
		Status: result.Status, Message: "Blood pressure recorded successfully.",
	}, nil
}
