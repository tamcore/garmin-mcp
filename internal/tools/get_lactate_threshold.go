package tools

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetLactateThreshold is the upstream compatibility name of the lactate-threshold
// read.
const ToolGetLactateThreshold = "get_lactate_threshold"

// The two shapes this tool answers in, named on the wire so a caller never has to
// guess which set of fields it is reading.
const (
	lactateModeLatest = "latest"
	lactateModeRange  = "range"
)

// The part names of the read. The latest reading asks two endpoints, a window asks
// three, and each one is reported separately because any of them can fail on its own.
const (
	lactatePartSpeedAndHeartRate = "speed_and_heart_rate"
	lactatePartPower             = "power"
	lactatePartSpeed             = "speed"
	lactatePartHeartRate         = "heart_rate"
)

// A ThresholdPart is the outcome of one of the endpoints this read spans.
//
// It exists so a partial answer is stated rather than implied: a missing field can
// mean Garmin holds nothing, or it can mean that one endpoint failed while the others
// answered, and those are not the same fact.
type ThresholdPart struct {
	Name      string  `json:"name" jsonschema:"which part of the read this is"`
	Available bool    `json:"available" jsonschema:"whether this part was read successfully"`
	Note      *string `json:"note,omitempty" jsonschema:"why the part is unavailable, when it is"`
}

// A ThresholdPoint is one dated sample of a threshold series.
//
// Series is the sport the sample belongs to, carried through as Garmin spelled it.
// The value set is open: a value this server has never seen is passed on unchanged
// rather than refused or mapped onto a known one.
//
// The samples keep Garmin's own order, and nothing here assumes that order means
// anything, that the dates are unique, or that two samples differ.
type ThresholdPoint struct {
	Date   *string  `json:"date,omitempty" jsonschema:"the day the sample is dated, as Garmin reported it"`
	Value  *float64 `json:"value,omitempty" jsonschema:"the sample's value, in the unit of its series"`
	Series *string  `json:"series,omitempty" jsonschema:"the sport the sample belongs to"`
}

// LactateThreshold is the account's running lactate threshold, either the latest
// reading or a window of them. It is health data — never log it, never cache it.
type LactateThreshold struct {
	Mode      string  `json:"mode" jsonschema:"latest for the current reading, range for a window"`
	StartDate *string `json:"start_date,omitempty" jsonschema:"the inclusive first day of the window"`
	EndDate   *string `json:"end_date,omitempty" jsonschema:"the inclusive last day of the window"`

	SpeedMPS            *float64 `json:"lactate_threshold_speed_mps,omitempty" jsonschema:"the speed, metres a second"`
	HeartRateBPM        *int     `json:"lactate_threshold_heart_rate_bpm,omitempty" jsonschema:"the heart rate, bpm"`
	HeartRateCyclingBPM *int     `json:"heart_rate_cycling_bpm,omitempty" jsonschema:"the cycling threshold heart rate"`
	SpeedHeartRateDate  *string  `json:"speed_hr_date,omitempty" jsonschema:"the day the speed is dated"`

	FTPWatts      *float64 `json:"functional_threshold_power_watts,omitempty" jsonschema:"the power in watts"`
	Weight        *float64 `json:"weight,omitempty" jsonschema:"the weight, as Garmin reports it"`
	PowerToWeight *float64 `json:"power_to_weight,omitempty" jsonschema:"the power-to-weight ratio"`
	Sport         *string  `json:"sport,omitempty" jsonschema:"the Garmin sport key the power record belongs to"`
	PowerDate     *string  `json:"power_date,omitempty" jsonschema:"the day the power record is dated"`
	IsStale       *bool    `json:"is_stale,omitempty" jsonschema:"whether the power record is stale"`

	SpeedHistory     []ThresholdPoint `json:"speed_history,omitempty" jsonschema:"the threshold speed over the window"`
	HeartRateHistory []ThresholdPoint `json:"heart_rate_history,omitempty" jsonschema:"the heart rate series"`
	PowerHistory     []ThresholdPoint `json:"power_history,omitempty" jsonschema:"the threshold power over the window"`

	Truncated bool            `json:"truncated" jsonschema:"whether a series was cut at this server's bound"`
	Complete  bool            `json:"complete" jsonschema:"whether every part of the read answered"`
	Parts     []ThresholdPart `json:"parts" jsonschema:"one entry per endpoint, with its outcome"`
}

// LogValue reports the shape of the answer, never a reading.
func (l LactateThreshold) LogValue() slog.Value {
	return shape("lactateThreshold",
		slog.String("mode", l.Mode),
		slog.Bool("complete", l.Complete),
		slog.Int("speedSamples", len(l.SpeedHistory)),
		slog.Int("heartRateSamples", len(l.HeartRateHistory)),
		slog.Int("powerSamples", len(l.PowerHistory)),
		slog.Bool("truncated", l.Truncated),
	)
}

// lactateThresholdInput is the strict argument set: an optional inclusive window.
type lactateThresholdInput struct {
	StartDate string `json:"start_date" jsonschema:"inclusive first day, YYYY-MM-DD; omit for the latest reading"`
	EndDate   string `json:"end_date" jsonschema:"inclusive last day, YYYY-MM-DD; omit for the latest reading"`
}

func getLactateThresholdContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetLactateThreshold,
			Title: "Get lactate threshold",
			Description: "read the account's running lactate threshold: the threshold " +
				"speed, heart rate and power. With no arguments it returns the latest " +
				"reading; with both dates it returns the daily series over that window. " +
				"The read spans several Garmin endpoints, and the parts list states " +
				"which of them answered, so a partial result is never mistaken for an " +
				"account that holds nothing",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			optionalDateProperty("start_date", "inclusive first day of the window"),
			optionalDateProperty("end_date", "inclusive last day of the window"),
		),
	}
}

// registerGetLactateThreshold registers the tool.
func registerGetLactateThreshold(registry *mcpserver.Registry, svc *service) error {
	scores, err := trainingScoresClient(svc)
	if err != nil {
		return err
	}
	return mcpserver.AddTool(registry, getLactateThresholdContract().Registration(),
		lactateThresholdHandler(svc, scores))
}

// lactateThresholdHandler builds the handler, so the registration stays one statement.
func lactateThresholdHandler(
	svc *service, scores *api.TrainingScores,
) mcp.ToolHandlerFor[lactateThresholdInput, LactateThreshold] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in lactateThresholdInput) (
		*mcp.CallToolResult, LactateThreshold, error,
	) {
		wantWindow, err := lactateWindowRequested(in)
		if err != nil {
			return nil, LactateThreshold{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, LactateThreshold{}, err
		}

		if !wantWindow {
			out, err := readLatestLactateThreshold(ctx, session, scores, svc.now)
			return nil, out, err
		}
		span, err := parseWindow(in.StartDate, in.EndDate, svc.limits)
		if err != nil {
			return nil, LactateThreshold{}, err
		}
		out, err := readLactateThresholdWindow(
			ctx, session, scores, span, svc.limits.MaxDateRangeDays)
		return nil, out, err
	}
}

// lactateWindowRequested decides which of the two reads was asked for.
//
// Both dates select the window and neither selects the latest reading. Exactly one is
// refused rather than quietly answered with the latest reading, because a caller who
// supplied a date asked a question about that date.
func lactateWindowRequested(in lactateThresholdInput) (bool, error) {
	switch {
	case in.StartDate == "" && in.EndDate == "":
		return false, nil
	case in.StartDate != "" && in.EndDate != "":
		return true, nil
	default:
		return false, invalidArgument(
			"start_date and end_date must be given together, or both omitted for the latest reading")
	}
}

// readLatestLactateThreshold reads the two endpoints the latest reading spans.
//
// The power-to-weight path is keyed by today's date, which is what upstream sends.
//
// The day is the one the process is standing in, as upstream's date.today() is,
// because a Garmin calendar date is the account's own day rather than a UTC one.
// client.NewDate would take the UTC day, so just after local midnight in a zone ahead
// of UTC it would ask for yesterday; NewCalendarDay is the constructor that does not.
func readLatestLactateThreshold(
	ctx context.Context, session client.Session, scores *api.TrainingScores,
	now func() time.Time,
) (LactateThreshold, error) {
	out := LactateThreshold{Mode: lactateModeLatest, Parts: []ThresholdPart{}}

	entries, err := scores.LatestLactateThreshold(ctx, session)
	if fatal := recordThresholdPart(&out, lactatePartSpeedAndHeartRate, err); fatal != nil {
		return LactateThreshold{}, fatal
	}
	applyLatestSpeedAndHeartRate(&out, entries)

	power, err := scores.LatestPowerToWeight(ctx, session, client.NewCalendarDay(now()))
	if fatal := recordThresholdPart(&out, lactatePartPower, err); fatal != nil {
		return LactateThreshold{}, fatal
	}
	if record, ok := latestThresholdPower(power); ok {
		out.FTPWatts = optionalFloat(record.FunctionalThresholdPower)
		out.Weight = optionalFloat(record.Weight)
		out.PowerToWeight = optionalFloat(record.PowerToWeight)
		out.Sport = optionalText(record.Sport)
		out.PowerDate = record.CalendarDate
		out.IsStale = record.IsStale
	}

	out.Complete = everyPartAvailable(out.Parts)
	return out, nil
}

// applyLatestSpeedAndHeartRate folds the entries Garmin answers with into one reading.
//
// Garmin sends several nearly identical entries, one carrying the speed and another
// the heart rate, and the heart rate has been seen under a misspelled key. Both
// spellings are read, exactly as upstream reads them.
func applyLatestSpeedAndHeartRate(out *LactateThreshold, entries []api.LactateThresholdEntry) {
	for _, entry := range entries {
		if speed := optionalFloat(entry.Speed); speed != nil {
			out.SpeedMPS = speed
			out.SpeedHeartRateDate = entry.CalendarDate
		}
		if rate := optionalInt(entry.HeartRate); rate != nil {
			out.HeartRateBPM = rate
		} else if rate := optionalInt(entry.HeartRateTypo); rate != nil {
			out.HeartRateBPM = rate
		}
		if rate := optionalInt(entry.HeartRateCycling); rate != nil {
			out.HeartRateCyclingBPM = rate
		}
	}
}

// readLactateThresholdWindow reads the three series a window spans.
func readLactateThresholdWindow(
	ctx context.Context, session client.Session, scores *api.TrainingScores,
	span client.DateRange, maxSamples int,
) (LactateThreshold, error) {
	start, end := span.Start().String(), span.End().String()
	out := LactateThreshold{
		Mode:      lactateModeRange,
		StartDate: &start,
		EndDate:   &end,
		Parts:     []ThresholdPart{},
	}

	series := []struct {
		name string
		read func() ([]api.ThresholdSample, error)
		into *[]ThresholdPoint
	}{
		{lactatePartSpeed, func() ([]api.ThresholdSample, error) {
			return scores.LactateThresholdSpeedRange(ctx, session, span)
		}, &out.SpeedHistory},
		{lactatePartHeartRate, func() ([]api.ThresholdSample, error) {
			return scores.LactateThresholdHeartRateRange(ctx, session, span)
		}, &out.HeartRateHistory},
		{lactatePartPower, func() ([]api.ThresholdSample, error) {
			return scores.FunctionalThresholdPowerRange(ctx, session, span)
		}, &out.PowerHistory},
	}
	for _, part := range series {
		samples, err := part.read()
		if fatal := recordThresholdPart(&out, part.name, err); fatal != nil {
			return LactateThreshold{}, fatal
		}
		points, truncated := thresholdPoints(samples, maxSamples)
		*part.into = points
		out.Truncated = out.Truncated || truncated
	}

	out.Complete = everyPartAvailable(out.Parts)
	return out, nil
}

// thresholdPoints maps one series, bounded by the request layer's own date-window
// bound: the series is aggregated daily, so a validated window holds at most one
// sample per day and anything past that is drift rather than data.
func thresholdPoints(samples []api.ThresholdSample, limit int) ([]ThresholdPoint, bool) {
	truncated := false
	if len(samples) > limit {
		samples = samples[:limit]
		truncated = true
	}

	out := make([]ThresholdPoint, 0, len(samples))
	for _, sample := range samples {
		out = append(out, ThresholdPoint{
			Date:   optionalText(sample.From),
			Value:  optionalFloat(sample.Value),
			Series: optionalText(sample.Series),
		})
	}
	return out, truncated
}

// recordThresholdPart records one part's outcome and reports the failures that must
// end the whole call.
//
// A failure Garmin decided about the session — a rejected token, a rate limit — or one
// the caller decided — a cancellation, a deadline — says nothing about this part and
// everything about the call, so it is returned rather than recorded. Anything else
// leaves the other parts answerable and is reported as an unavailable part.
func recordThresholdPart(out *LactateThreshold, name string, err error) error {
	if err == nil {
		out.Parts = append(out.Parts, ThresholdPart{Name: name, Available: true})
		return nil
	}
	if fatalThresholdError(err) {
		return fail(err)
	}
	note := advise(err)
	out.Parts = append(out.Parts, ThresholdPart{Name: name, Note: &note})
	return nil
}

// fatalThresholdError reports the failures that must stop the whole read.
func fatalThresholdError(err error) bool {
	return errors.Is(err, client.ErrAuthentication) ||
		errors.Is(err, client.ErrRateLimited) ||
		errors.Is(err, client.ErrMissingPrincipal) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

// everyPartAvailable reports whether every part of the read answered.
func everyPartAvailable(parts []ThresholdPart) bool {
	for _, part := range parts {
		if !part.Available {
			return false
		}
	}
	return true
}
