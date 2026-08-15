package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetStressData is the upstream compatibility name of the stress series tool.
const ToolGetStressData = "get_stress_data"

// maxStressSamples bounds the returned stress series. Garmin samples stress every few
// minutes, so a full day fits comfortably; the bound exists so a drifted or unusually
// dense day cannot fill a model's context. A cut series is reported as truncated
// rather than passed off as the whole day.
const maxStressSamples = 1440

// StressSample is one point of the day's stress series. Both fields are optional:
// Garmin marks a gap and an activity window with a negative level, and a sample it did
// not record carries neither a timestamp nor a level.
type StressSample struct {
	Timestamp *int64 `json:"timestamp,omitempty" jsonschema:"the sample time as a UTC epoch in milliseconds"`
	Level     *int   `json:"level,omitempty" jsonschema:"the stress level, or a negative marker for a gap"`
}

// StressData is one calendar day of stress, series included.
//
// It is health data — never log it, never cache it. Upstream returns the raw document,
// which also carries the day's body battery; that lives in get_body_battery here, and
// the series is bounded rather than returned whole.
type StressData struct {
	// Date is the calendar day that was requested.
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	// HasData reports whether Garmin held anything for the day. A day the watch was
	// not worn is a normal state, not a failure.
	HasData bool `json:"has_data" jsonschema:"whether Garmin held stress data for the day"`

	MaxStressLevel     *int `json:"max_stress_level,omitempty" jsonschema:"the day's highest stress level"`
	AverageStressLevel *int `json:"average_stress_level,omitempty" jsonschema:"the day's average stress level"`

	SampleCount int  `json:"sample_count" jsonschema:"how many samples this result carries"`
	Truncated   bool `json:"truncated" jsonschema:"whether the series was cut at this server's bound"`

	Samples []StressSample `json:"samples" jsonschema:"the day's stress series, oldest first"`
}

// LogValue reports the shape of the day, never a reading.
func (s StressData) LogValue() slog.Value {
	return shape("stressData",
		slog.Bool("hasData", s.HasData),
		slog.Int("samples", len(s.Samples)),
		slog.Bool("truncated", s.Truncated),
		slog.String("maxLevel", presence(s.MaxStressLevel != nil)),
		slog.String("averageLevel", presence(s.AverageStressLevel != nil)),
	)
}

// getStressDataInput is the strict argument set: one calendar day.
type getStressDataInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getStressDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetStressData,
			Title: "Get stress data",
			Description: "read one calendar day of the account's stress: the highest and " +
				"average level, and the measured series. The series is bounded by this " +
				"server and the result says whether it was cut. For the distribution " +
				"without the series, call get_stress_summary",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetStressData registers the tool.
func registerGetStressData(registry *mcpserver.Registry, svc *service) error {
	stress, err := stressClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getStressDataInput) (
		*mcp.CallToolResult, StressData, error,
	) {
		day, session, err := svc.resolveStressDay(ctx, in.Date)
		if err != nil {
			return nil, StressData{}, err
		}
		read, err := stress.DailyStress(ctx, session, day, api.StressViewFull)
		if err != nil {
			return nil, StressData{}, fail(err)
		}
		return nil, newStressData(day.String(), read), nil
	}
	return mcpserver.AddTool(registry, getStressDataContract().Registration(), handler)
}

// newStressData maps the domain model onto the bounded result. The date is the day
// that was asked for, not the one the payload echoes, so a caller always knows what it
// got.
func newStressData(date string, day api.DailyStress) StressData {
	out := StressData{
		Date:               date,
		HasData:            !day.IsEmpty(),
		MaxStressLevel:     optionalInt(day.MaxStressLevel),
		AverageStressLevel: optionalInt(day.AvgStressLevel),
		Samples:            []StressSample{},
	}

	samples := day.Values.Items()
	if len(samples) > maxStressSamples {
		samples = samples[:maxStressSamples]
		out.Truncated = true
	}
	for _, sample := range samples {
		out.Samples = append(out.Samples, StressSample{
			Timestamp: optionalInt64(sample.Timestamp),
			Level:     optionalInt(sample.Level),
		})
	}
	out.SampleCount = len(out.Samples)
	return out
}

// stressClient builds the stress domain client beside the wellness client the service
// already holds, which is where the shared request layer lives.
func stressClient(svc *service) (*api.WellnessStress, error) {
	if svc == nil {
		return nil, fail(ErrMissingDependency)
	}
	stress, err := api.NewWellnessStressFrom(svc.wellness)
	if err != nil {
		return nil, fail(err)
	}
	return stress, nil
}

// resolveStressDay validates the date argument first, so a malformed date costs no
// Garmin call at all, and only then resolves the session.
//
// Unlike the sleep and summary reads, none of the stress, body-battery or readiness
// paths carries the account's display name, so no profile read is needed and none is
// made: the principal alone identifies the account.
func (s *service) resolveStressDay(
	ctx context.Context, date string,
) (client.Date, client.Session, error) {
	day, err := parseCalendarDate("date", date)
	if err != nil {
		return client.Date{}, client.Session{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return client.Date{}, client.Session{}, err
	}
	return day, session, nil
}
