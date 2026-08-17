package tools

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolAddHydrationData is the upstream compatibility name of the tool.
const ToolAddHydrationData = "add_hydration_data"

// maxHydrationMLArgument bounds value_in_ml in either direction. Source:
// garminconnect/__init__.py:41, MAX_HYDRATION_ML = 10000.
const maxHydrationMLArgument = 10000

// hydrationTimestampLayout is the caller-facing timestamp form
// add_hydration_data's own manifest documents: "YYYY-MM-DDThh:mm:ss.sss".
// Source: data_management.py's add_hydration_data docstring, "timestamp:
// Timestamp in YYYY-MM-DDThh:mm:ss.sss format".
const hydrationTimestampLayout = "2006-01-02T15:04:05.000"

// maxHydrationTimestampLen bounds the timestamp argument: exactly 23
// characters in hydrationTimestampLayout.
const maxHydrationTimestampLen = 23

// Argument names, named once so the schema declaration, the struct tags
// below and the parse calls cannot drift to different spellings.
const (
	argValueInML     = "value_in_ml"
	argCDate         = "cdate"
	argHydrationTime = "timestamp"
)

// AddHydrationDataResult is what add_hydration_data reports. Upstream's own
// outputShape carries no static top-level keys, so this echoes the
// caller's own entry back, plus the HTTP status, matching every other
// otherwise-uncurated write in this package.
type AddHydrationDataResult struct {
	ValueInML int64  `json:"value_in_ml" jsonschema:"the amount of liquid recorded, in milliliters"`
	CDate     string `json:"cdate" jsonschema:"the calendar day the entry was recorded for"`
	Timestamp string `json:"timestamp" jsonschema:"the timestamp the entry was recorded at"`
	Status    int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
	Message   string `json:"message" jsonschema:"a human-readable confirmation"`
}

// LogValue reports that a write happened, never the volume, the date or the
// timestamp.
func (r AddHydrationDataResult) LogValue() slog.Value {
	return shape("addHydrationDataResult", slog.Int("status", r.Status))
}

// addHydrationDataInput is the strict argument set. All three arguments are
// required, matching data_management.py's own wrapper signature rather than
// the more permissive optional parameters the underlying library method
// itself accepts.
type addHydrationDataInput struct {
	// ValueInML is positive to add or negative to subtract.
	ValueInML int64  `json:"value_in_ml" jsonschema:"the amount of liquid in milliliters"`
	CDate     string `json:"cdate" jsonschema:"the calendar day of the entry, YYYY-MM-DD"`
	Timestamp string `json:"timestamp" jsonschema:"the timestamp, YYYY-MM-DDThh:mm:ss.sss"`
}

func addHydrationDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolAddHydrationData,
			Title: "Add hydration data",
			Description: "record a hydration entry: a volume in milliliters at a caller-given " +
				"timestamp, whose date must agree with cdate. Creates a new entry every time " +
				"it is called; repeats accumulate rather than replace",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			Property{
				Name: argValueInML, Types: []string{typeInteger},
				Description: "the amount of liquid in milliliters, positive to add or negative to subtract",
				Minimum:     bound(-maxHydrationMLArgument), Maximum: bound(maxHydrationMLArgument),
				Required: true,
			},
			dateProperty(argCDate, "the calendar day of the entry"),
			Property{
				Name:        argHydrationTime,
				Types:       []string{typeString},
				Description: "the timestamp of the entry, YYYY-MM-DDThh:mm:ss.sss",
				MaxLength:   new(maxHydrationTimestampLen),
				Required:    true,
			},
		),
	}
}

// registerAddHydrationData registers the tool.
func registerAddHydrationData(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in addHydrationDataInput) (
		*mcp.CallToolResult, AddHydrationDataResult, error,
	) {
		out, err := svc.addHydrationData(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, addHydrationDataContract().Registration(), handler)
}

// parseHydrationTimestamp validates the timestamp argument against its own
// documented layout.
func parseHydrationTimestamp(value string) (time.Time, error) {
	if len(value) > maxHydrationTimestampLen {
		return time.Time{}, invalidArgument(
			"timestamp must be at most twenty-three characters, YYYY-MM-DDThh:mm:ss.sss")
	}
	parsed, err := time.Parse(hydrationTimestampLayout, value)
	if err != nil {
		return time.Time{}, invalidArgument(
			"timestamp must be a real instant in YYYY-MM-DDThh:mm:ss.sss form")
	}
	return parsed, nil
}

// addHydrationData performs the write behind the tool.
func (s *service) addHydrationData(
	ctx context.Context, in addHydrationDataInput,
) (AddHydrationDataResult, error) {
	date, err := parseCalendarDate(argCDate, in.CDate)
	if err != nil {
		return AddHydrationDataResult{}, err
	}
	at, err := parseHydrationTimestamp(in.Timestamp)
	if err != nil {
		return AddHydrationDataResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return AddHydrationDataResult{}, err
	}

	result, err := s.dataManagement.AddHydrationData(ctx, session, api.HydrationEntry{
		ValueInML: float64(in.ValueInML), Date: date, At: at,
	})
	if err != nil {
		return AddHydrationDataResult{}, fail(err)
	}
	return AddHydrationDataResult{
		ValueInML: in.ValueInML, CDate: date.String(), Timestamp: at.Format(hydrationTimestampLayout),
		Status: result.Status, Message: "Hydration data added successfully.",
	}, nil
}
