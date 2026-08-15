package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetBloodPressure is the upstream compatibility name of the blood-pressure tool.
const ToolGetBloodPressure = "get_blood_pressure"

// DefaultMaxBloodPressureReadings bounds the returned reading list. Readings are
// entered by hand, so a window that holds more than this is a window to narrow rather
// than one to return whole.
const DefaultMaxBloodPressureReadings = 500

// A BloodPressureReading is one measurement.
//
// The notes field is the account holder's own free text. It is returned because it is
// the account's own data and is what upstream returns, and it is never logged.
type BloodPressureReading struct {
	SystolicMMHG  *float64 `json:"systolic_mmhg,omitempty" jsonschema:"the systolic pressure in mmHg"`
	DiastolicMMHG *float64 `json:"diastolic_mmhg,omitempty" jsonschema:"the diastolic pressure in mmHg"`
	PulseBPM      *float64 `json:"pulse_bpm,omitempty" jsonschema:"the pulse in beats per minute"`

	MeasuredGMT   *string `json:"measured_gmt,omitempty" jsonschema:"when the reading was taken, as Garmin renders it"`
	MeasuredLocal *string `json:"measured_local,omitempty" jsonschema:"the local time of the reading"`
	SourceType    *string `json:"source_type,omitempty" jsonschema:"how the reading was recorded, for example MANUAL"`
	Notes         *string `json:"notes,omitempty" jsonschema:"the note the account holder attached to the reading"`
}

// BloodPressure is every reading in an inclusive date window.
//
// It is health data: never log it, never cache it.
type BloodPressure struct {
	StartDate string `json:"start_date" jsonschema:"the first day of the window that was requested, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the last day of the window that was requested, YYYY-MM-DD"`

	Readings  []BloodPressureReading `json:"readings" jsonschema:"the readings in the window, in payload order"`
	Count     int                    `json:"count" jsonschema:"how many readings this result carries"`
	Truncated bool                   `json:"truncated" jsonschema:"whether the list was cut at this server's bound"`
}

// LogValue reports the shape of the window and never a reading.
func (b BloodPressure) LogValue() slog.Value {
	return shape("bloodPressure",
		slog.Int("readings", b.Count),
		slog.Bool("truncated", b.Truncated),
	)
}

// getBloodPressureInput is the strict argument set: one inclusive date window.
type getBloodPressureInput struct {
	StartDate string `json:"start_date" jsonschema:"the first day of the window, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the last day of the window, YYYY-MM-DD"`
}

func getBloodPressureContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetBloodPressure,
			Title: "Get blood pressure",
			Description: "read the account's blood-pressure readings over an inclusive " +
				"date window: systolic, diastolic, pulse, when each was taken, how it was " +
				"recorded and the note attached to it. The window and the list are bounded",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("start_date", "the first day of the window"),
			dateProperty("end_date", "the last day of the window"),
		),
	}
}

// registerGetBloodPressure registers the tool.
func registerGetBloodPressure(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getBloodPressureInput) (
		*mcp.CallToolResult, BloodPressure, error,
	) {
		out, err := svc.readBloodPressure(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getBloodPressureContract().Registration(), handler)
}

// readBloodPressure performs the read behind the tool.
//
// The window is validated before the session is resolved, so a window this server
// refuses costs no Garmin call at all.
func (s *service) readBloodPressure(
	ctx context.Context, in getBloodPressureInput,
) (BloodPressure, error) {
	span, err := parseWindow(in.StartDate, in.EndDate, s.limits)
	if err != nil {
		return BloodPressure{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return BloodPressure{}, err
	}
	window, err := s.wellness.Cardio().BloodPressure(ctx, session, span)
	if err != nil {
		return BloodPressure{}, fail(err)
	}
	return newBloodPressure(span.Start().String(), span.End().String(), window), nil
}

// newBloodPressure maps the domain model onto the curated result.
func newBloodPressure(start, end string, window api.BloodPressureRange) BloodPressure {
	measurements := window.Measurements()
	truncated := len(measurements) > DefaultMaxBloodPressureReadings
	if truncated {
		measurements = measurements[:DefaultMaxBloodPressureReadings]
	}

	readings := make([]BloodPressureReading, 0, len(measurements))
	for _, measurement := range measurements {
		readings = append(readings, BloodPressureReading{
			SystolicMMHG:  optionalFloat(measurement.Systolic),
			DiastolicMMHG: optionalFloat(measurement.Diastolic),
			PulseBPM:      optionalFloat(measurement.Pulse),
			MeasuredGMT:   optionalText(measurement.MeasurementTimestampGMT),
			MeasuredLocal: optionalText(measurement.MeasurementTimestampLocal),
			SourceType:    optionalText(measurement.SourceType),
			Notes:         optionalText(measurement.Notes),
		})
	}
	return BloodPressure{
		StartDate: start,
		EndDate:   end,
		Readings:  readings,
		Count:     len(readings),
		Truncated: truncated,
	}
}
