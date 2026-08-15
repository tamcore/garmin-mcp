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

// ToolGetActivityFITData is the upstream compatibility name of the FIT analysis.
const ToolGetActivityFITData = "get_activity_fit_data"

// Result bounds of the FIT tools. Each one is explicit because a FIT file is binary,
// arrives compressed, and expands into a per-second stream: every stage between the
// wire and the result has its own ceiling.
const (
	// maxFITFileBytes bounds the FIT file after the archive is expanded. The archive
	// itself is bounded before that by Bounds.MaxDownloadBytes, which is what the
	// transfer streams into.
	maxFITFileBytes = 12 << 20

	// maxFITMessages bounds how many data messages one file may carry.
	maxFITMessages = 400_000

	// maxFITSamples bounds how many records are retained from one file.
	maxFITSamples = 60_000

	// maxFITSeriesRecords bounds the per-second series returned when the caller asks
	// for it. Records are the largest thing this tool can return, so the bound is
	// deliberately far below the retained sample count.
	maxFITSeriesRecords = 3000

	// maxFITLaps bounds the returned lap summaries.
	maxFITLaps = 200

	// maxFITSessions bounds the returned session summaries.
	maxFITSessions = 20

	// maxFITShiftEvents bounds the returned gear changes.
	maxFITShiftEvents = 200
)

// fitLimits are the decode bounds these tools apply.
//
// The span bounds are the *render* bounds, deliberately. Every session and lap the
// decode retains is summarized against the whole retained sample stream, so a span
// this tool would never show still costs a pass over sixty thousand records. Decoding
// exactly what can be rendered is the only span count worth paying for, and tying the
// two together here is what stops them drifting apart.
func fitLimits() api.FITLimits {
	return api.FITLimits{
		MaxBytes:    maxFITFileBytes,
		MaxMessages: maxFITMessages,
		MaxRecords:  maxFITSamples,
		MaxSessions: maxFITSessions,
		MaxLaps:     maxFITLaps,
	}
}

// activityFITInput is the FIT analysis argument set.
type activityFITInput struct {
	ActivityID     any   `json:"activity_id" jsonschema:"the Garmin activity identifier, number or string"`
	IncludeRecords *bool `json:"include_records" jsonschema:"include the per-second series"`
}

// FITData is the analysis of one activity file.
type FITData struct {
	ActivityID       int64               `json:"activity_id" jsonschema:"the activity the file belongs to"`
	Sport            *string             `json:"sport,omitempty" jsonschema:"the recorded sport, when named"`
	FileBytes        int                 `json:"file_bytes" jsonschema:"how many bytes the download carried"`
	Overall          FITSegmentView      `json:"overall" jsonschema:"the whole-activity summary"`
	Sessions         []FITSegmentView    `json:"sessions" jsonschema:"the per-session summaries"`
	Laps             []FITSegmentView    `json:"laps" jsonschema:"the per-lap summaries"`
	LapsTruncated    bool                `json:"laps_truncated" jsonschema:"whether the lap list was cut at a bound"`
	Curve            []FITPowerBestView  `json:"power_duration_curve" jsonschema:"best mean power per duration"`
	Climbs           []FITClimbView      `json:"climbs" jsonschema:"the detected sustained ascents"`
	GradeBands       []FITGradeBandView  `json:"grade_bands" jsonschema:"time and averages per terrain band"`
	Temperature      *FITTemperatureView `json:"temperature,omitempty" jsonschema:"warmest against coolest quarter"`
	Drift            *FITDriftView       `json:"heart_rate_drift,omitempty" jsonschema:"the aerobic decoupling"`
	Shifts           FITShiftView        `json:"shifts" jsonschema:"the electronic gear changes"`
	Records          []FITRecordView     `json:"records,omitempty" jsonschema:"the per-second series, when asked for"`
	RecordsIncluded  bool                `json:"records_included" jsonschema:"whether the series was asked for"`
	RecordsTruncated bool                `json:"records_truncated" jsonschema:"whether the series was cut at a bound"`
	SamplesTruncated bool                `json:"samples_truncated" jsonschema:"whether the decode stopped collecting"`
}

// LogValue reports the shape of the analysis, never a reading.
//
// Not one reading of the analysis is logged. What is logged is how much of the result
// was assembled and whether anything was cut, which is what a truncation or a bound
// has to be diagnosed from.
//
// The shift count is not among them, and the reason is worth stating because it was
// once logged: the returned shift list is bounded at two hundred entries and a real
// ride stays under that, so its length *is* the number of times the rider changed
// gear, exactly. A count of what a person did is a reading. It is therefore logged as
// presence, which says whether the file carried electronic shifting at all and nothing
// about the ride.
func (d FITData) LogValue() slog.Value {
	return shape("fitData",
		slog.Int("sessions", len(d.Sessions)),
		slog.Int("laps", len(d.Laps)),
		slog.Int("climbs", len(d.Climbs)),
		slog.Bool("shifts", len(d.Shifts.Events) > 0),
		slog.Int("records", len(d.Records)),
		slog.Bool("truncated", d.RecordsTruncated || d.LapsTruncated ||
			d.SamplesTruncated || d.Shifts.Truncated),
	)
}

func getActivityFITDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivityFITData,
			Title: "Get activity FIT data",
			Description: "download one activity's device FIT file and analyse what the REST " +
				"API does not carry: electronic shift events with the cadence and gradient " +
				"they happened at, cycling dynamics per session and lap, normalized power and " +
				"variability index, detected climbs with vertical ascent rate, power cadence " +
				"and heart rate by terrain steepness, aerobic decoupling for rides of an hour " +
				"or more, a hot-against-cool comparison, and the power duration curve at 5s, " +
				"30s, 1min, 5min, 10min, 20min and 60min. The per-second series comes back " +
				"only when include_records is set, and it is bounded. No coordinates are " +
				"returned, and power per kilogram is not reported because this server does not " +
				"read body composition",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(activityIDProperty(), Property{
			Name:        "include_records",
			Types:       []string{typeBoolean},
			Description: "include the per-second series, which is large for a long activity",
			Default:     false,
		}),
	}
}

// registerGetActivityFITData registers the tool.
func registerGetActivityFITData(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityFITInput) (
		*mcp.CallToolResult, FITData, error,
	) {
		data, err := svc.activityFITData(ctx, in)
		if err != nil {
			return nil, FITData{}, err
		}
		return nil, data, nil
	}
	return mcpserver.AddTool(registry, getActivityFITDataContract().Registration(), handler)
}

// activityFITData downloads one activity file and renders its analysis.
func (s *service) activityFITData(ctx context.Context, in activityFITInput) (FITData, error) {
	id, session, err := s.resolveActivityRead(ctx, in.ActivityID)
	if err != nil {
		return FITData{}, err
	}

	activity, size, err := s.downloadFITActivity(ctx, session, id)
	if err != nil {
		return FITData{}, err
	}
	include := in.IncludeRecords != nil && *in.IncludeRecords
	return newFITData(id.Int64(), size, activity, include), nil
}

// downloadFITActivity streams one activity's FIT file into memory under the download
// bound and decodes it under the decode bounds.
func (s *service) downloadFITActivity(
	ctx context.Context, session client.Session, id client.ID,
) (api.FITActivity, int, error) {
	sink := newBoundedSink(s.bounds.MaxDownloadBytes)
	_, transferErr := s.files.Download(ctx, session, id, api.FormatOriginal, sink)
	// The sink is asked first: it aborts the copy, so its own refusal is the cause of
	// the transfer error and is the one worth reporting.
	if err := sink.err(); err != nil {
		return api.FITActivity{}, 0, err
	}
	if transferErr != nil {
		return api.FITActivity{}, 0, fail(transferErr)
	}

	activity, err := api.ParseFITActivity(ctx, sink.bytes(), fitLimits())
	if err != nil {
		return api.FITActivity{}, 0, fail(err)
	}
	return activity, sink.len(), nil
}

// newFITData renders the bounded result of one analysed file.
func newFITData(activityID int64, size int, activity api.FITActivity, records bool) FITData {
	summary := api.AnalyzeFIT(activity)
	sessions, _ := boundedSegments(summary.Sessions, maxFITSessions)
	laps, lapsTruncated := boundedSegments(summary.Laps, maxFITLaps)

	data := FITData{
		ActivityID:       activityID,
		FileBytes:        size,
		Overall:          newFITSegmentView(summary.Overall),
		Sessions:         sessions,
		Laps:             laps,
		LapsTruncated:    lapsTruncated,
		Curve:            newFITPowerBestViews(summary.Curve),
		Climbs:           newFITClimbViews(summary.Climbs),
		GradeBands:       newFITGradeBandViews(summary.GradeBands),
		Temperature:      newFITTemperatureView(summary.Temperature),
		Drift:            newFITDriftView(summary.Drift),
		Shifts:           newFITShiftView(summary.Shifts, maxFITShiftEvents),
		RecordsIncluded:  records,
		SamplesTruncated: activity.RecordsTruncated || activity.SpansTruncated,
	}
	if summary.Sport != "" {
		sport := summary.Sport
		data.Sport = &sport
	}
	if records {
		data.Records, data.RecordsTruncated = newFITRecordViews(activity.Records, maxFITSeriesRecords)
	}
	return data
}

// boundedSegments renders a session or lap list under its bound.
func boundedSegments(segments []api.FITSegment, limit int) ([]FITSegmentView, bool) {
	truncated := len(segments) > limit
	if truncated {
		segments = segments[:limit]
	}

	out := make([]FITSegmentView, 0, len(segments))
	for _, segment := range segments {
		out = append(out, newFITSegmentView(segment))
	}
	return out, truncated
}
