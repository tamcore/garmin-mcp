package tools

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Window bounds for the day-by-day trend tools.
//
// Each one is upstream's own MAX_DAYS constant for that tool at the pinned Taxuspt
// commit, which is also the maximum the pinned manifest's description states. They are
// not invented: a trend walks the window one Garmin request per day, so the bound is
// what stops a single MCP call from becoming an unbounded burst of Garmin reads.
const (
	// MaxHRVTrendDays bounds get_hrv_trend. Source: MAX_DAYS = 30 in get_hrv_trend,
	// and "Maximum: 30 days" in its description.
	MaxHRVTrendDays = 30

	// MaxRespirationTrendDays bounds get_respiration_trend. Source: MAX_DAYS = 30
	// in get_respiration_trend, and "Maximum: 30 days" in its description.
	MaxRespirationTrendDays = 30

	// MaxTrainingLoadTrendDays bounds get_training_load_trend. Source: MAX_DAYS =
	// 90 in get_training_load_trend, and "Maximum: 90 days" in its description.
	MaxTrainingLoadTrendDays = 90

	// MaxVO2MaxTrendDays bounds get_vo2max_trend. Source: MAX_DAYS = 90 in
	// get_vo2max_trend, and "Maximum: 90 days" in its description.
	MaxVO2MaxTrendDays = 90
)

// A TrendDayFailure is one day of a trend window that could not be read.
//
// It carries the day and the same authored, sanitized advice a single-day call would
// have returned. It never carries a status, a payload or a Garmin message.
type TrendDayFailure struct {
	Date   string `json:"date" jsonschema:"the calendar day that failed, YYYY-MM-DD"`
	Advice string `json:"advice" jsonschema:"why this day could not be read"`
}

// TrendCoverage is how complete a trend result is.
//
// It exists because a trend walks its window one day at a time, so a shorter list is
// ambiguous on its own: it can mean the account has no data for those days, or that
// the reads failed. Coverage separates the two and marks the result partial, so a
// caller never sees a truncated trend that looks complete.
type TrendCoverage struct {
	DaysRequested   int  `json:"days_requested" jsonschema:"how many calendar days the window spans"`
	DaysWithData    int  `json:"days_with_data" jsonschema:"how many days yielded a reading"`
	DaysWithoutData int  `json:"days_without_data" jsonschema:"how many days Garmin held nothing for"`
	DaysFailed      int  `json:"days_failed" jsonschema:"how many days could not be read"`
	Complete        bool `json:"complete" jsonschema:"whether every day of the window was read"`
	StoppedEarly    bool `json:"stopped_early" jsonschema:"whether the walk stopped before the window ended"`

	StopReason string            `json:"stop_reason,omitempty" jsonschema:"why the walk stopped early"`
	Failures   []TrendDayFailure `json:"failures,omitempty" jsonschema:"one entry per day that could not be read"`
}

// LogValue reports the coverage counts, which are shape and never a reading.
func (c TrendCoverage) LogValue() slog.Value {
	return shape("trendCoverage",
		slog.Int("daysRequested", c.DaysRequested),
		slog.Int("daysWithData", c.DaysWithData),
		slog.Int("daysFailed", c.DaysFailed),
		slog.Bool("complete", c.Complete),
	)
}

// trendWindow is a validated trend request: the window and the principal's session.
type trendWindow struct {
	span    client.DateRange
	session client.Session
}

// resolveTrendWindow validates the window before anything is dispatched, so a bad
// window costs no Garmin call at all, and only then resolves the session.
//
// maxDays is the tool's own upstream bound. The request layer's configured window
// bound is applied as well, so an operator who narrows it narrows these tools too.
func (s *service) resolveTrendWindow(
	ctx context.Context, startValue, endValue string, maxDays int,
) (trendWindow, error) {
	span, err := parseWindow(startValue, endValue, s.limits)
	if err != nil {
		return trendWindow{}, err
	}
	if span.Days() > maxDays {
		return trendWindow{}, invalidArgument(
			"the date window must not exceed " + strconv.Itoa(maxDays) +
				" days, because this tool reads Garmin once per day")
	}
	session, err := s.session(ctx)
	if err != nil {
		return trendWindow{}, err
	}
	return trendWindow{span: span, session: session}, nil
}

// trends returns the training-trend domain client over the shared request layer.
func (s *service) trends() *api.TrainingTrends { return s.wellness.TrainingTrends() }

// dayOutcome classifies what one day of a walk produced.
type dayOutcome int

const (
	// dayRead is a day the read answered for. Whether it carried a reading is the
	// reader's own answer.
	dayRead dayOutcome = iota
	// dayEmpty is a day Garmin holds nothing for. It is a normal state, not a
	// failure: a night the device was not worn produces one.
	dayEmpty
	// dayFailed is a day that could not be read. The walk continues.
	dayFailed
	// dayStop is a failure that makes the rest of the walk pointless or harmful —
	// a rate limit or a rejected session — so the walk stops and reports what it
	// already has.
	dayStop
	// dayAbort ends the call with an error rather than with a partial result.
	dayAbort
)

// classifyTrendDay maps one day's failure onto what the walk should do next.
func classifyTrendDay(err error) dayOutcome {
	switch {
	case err == nil:
		return dayRead
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return dayAbort
	case errors.Is(err, client.ErrNotFound):
		return dayEmpty
	case errors.Is(err, client.ErrRateLimited), errors.Is(err, client.ErrAuthentication):
		return dayStop
	default:
		return dayFailed
	}
}

// readDay reads one day of a trend and reports whether it yielded a reading.
type readDay func(ctx context.Context, day client.Date) (bool, error)

// walkTrendDays reads every day of span and reports how complete the result is.
//
// A day Garmin holds nothing for is counted, not dropped. A day that fails is counted
// and named in the coverage, and the walk continues, because one bad day is a poor
// reason to discard four weeks of readings. A rate limit or a rejected session stops
// the walk instead of repeating the same refusal once per remaining day. A call that
// produced no reading at all returns the failure rather than an empty result, which
// would read as "the account has no data".
func walkTrendDays(
	ctx context.Context, span client.DateRange, read readDay,
) (TrendCoverage, error) {
	coverage := TrendCoverage{DaysRequested: span.Days()}
	var firstErr error

	for day := span.Start(); !day.Time().After(span.End().Time()); day = day.AddDays(1) {
		hasData, err := read(ctx, day)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		outcome := classifyTrendDay(err)
		if outcome == dayAbort {
			return TrendCoverage{}, fail(err)
		}
		if stop := coverage.record(day, outcome, hasData, err); stop {
			break
		}
	}

	coverage.Complete = coverage.DaysFailed == 0 && !coverage.StoppedEarly
	if coverage.DaysWithData == 0 && firstErr != nil {
		return TrendCoverage{}, fail(firstErr)
	}
	return coverage, nil
}

// record accounts for one day and reports whether the walk must stop.
func (c *TrendCoverage) record(
	day client.Date, outcome dayOutcome, hasData bool, err error,
) bool {
	switch outcome {
	case dayRead:
		if hasData {
			c.DaysWithData++
			return false
		}
		c.DaysWithoutData++
	case dayEmpty:
		c.DaysWithoutData++
	case dayFailed:
		c.DaysFailed++
		c.Failures = append(c.Failures,
			TrendDayFailure{Date: day.String(), Advice: fail(err).Error()})
	case dayStop, dayAbort:
		c.StoppedEarly = true
		c.StopReason = fail(err).Error()
		return true
	}
	return false
}

// textOrEmpty renders a union-decoded string, or "" when it was absent. The trend
// results omit an empty phrase rather than reporting a null.
func textOrEmpty(text client.Text) string {
	value, ok := text.Value()
	if !ok {
		return ""
	}
	return value
}

// sortedStatKeys returns the keys of a Garmin-keyed map in a stable order, so two
// identical calls report the same list. Garmin keys these by activity type or by
// device identifier, and Go map iteration order is deliberately random.
func sortedStatKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// meanOf averages the collected values, or reports nothing for an empty series.
func meanOf(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	mean := total / float64(len(values))
	return &mean
}

// trendWindowProperties declares the start and end arguments every trend tool takes,
// naming the tool's own maximum in the description so the bound is visible to a client
// before it builds the call.
func trendWindowProperties(maxDays int) []Property {
	limit := ", in YYYY-MM-DD form. The window must not exceed " + strconv.Itoa(maxDays) +
		" days, because this tool reads Garmin once per day"
	return []Property{
		trendDateProperty("start_date", "the first calendar day of the window"+limit),
		trendDateProperty("end_date", "the last calendar day of the window, inclusive"+limit),
	}
}

// trendDateProperty is dateProperty with the description passed through verbatim, so a
// trend can state its own window bound after the date form.
func trendDateProperty(name, description string) Property {
	return Property{
		Name:        name,
		Types:       []string{typeString},
		Description: description,
		Format:      formatDate,
		Pattern:     patternCalendarDate,
		MaxLength:   new(maxDateArgumentLen),
		Required:    true,
	}
}
