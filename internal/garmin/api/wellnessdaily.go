package api

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// DailyStepsChunkDays is the widest window Garmin's daily step aggregate answers in
// one request. Source: get_daily_steps, which splits anything wider into chunks.
const DailyStepsChunkDays = 28

// WellnessDaily reads the daily-summary half of the health and wellness domain: the
// day's totals, body composition, the step series and the weekly aggregates. Every
// model here is health data.
type WellnessDaily struct {
	req requester
}

// NewWellnessDaily returns a daily-wellness client over the request layer.
func NewWellnessDaily(rc *client.Client) (*WellnessDaily, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &WellnessDaily{req: req}, nil
}

// Daily returns the daily-summary client over the same request layer, so a caller
// holding a Wellness needs no second construction.
func (w *Wellness) Daily() *WellnessDaily { return &WellnessDaily{req: w.req} }

// DailyStats is one day of the account's curated totals. It is health data: never
// log it.
//
// Every field is a union decoder or an optional pointer, because Garmin omits or
// nulls most of them for a day the account wore nothing. Source of every wire name:
// the field set get_stats reads out of the daily user summary upstream.
type DailyStats struct {
	CalendarDate *string `json:"calendarDate"`

	TotalSteps          client.Number `json:"totalSteps"`
	DailyStepGoal       client.Number `json:"dailyStepGoal"`
	TotalDistanceMeters client.Number `json:"totalDistanceMeters"`
	FloorsAscended      client.Number `json:"floorsAscended"`
	FloorsDescended     client.Number `json:"floorsDescended"`

	TotalKilocalories  client.Number `json:"totalKilocalories"`
	ActiveKilocalories client.Number `json:"activeKilocalories"`
	BMRKilocalories    client.Number `json:"bmrKilocalories"`

	HighlyActiveSeconds client.Number `json:"highlyActiveSeconds"`
	ActiveSeconds       client.Number `json:"activeSeconds"`
	SedentarySeconds    client.Number `json:"sedentarySeconds"`
	SleepingSeconds     client.Number `json:"sleepingSeconds"`

	ModerateIntensityMinutes client.Number `json:"moderateIntensityMinutes"`
	VigorousIntensityMinutes client.Number `json:"vigorousIntensityMinutes"`
	IntensityMinutesGoal     client.Number `json:"intensityMinutesGoal"`

	MinHeartRate                     client.Number `json:"minHeartRate"`
	MaxHeartRate                     client.Number `json:"maxHeartRate"`
	RestingHeartRate                 client.Number `json:"restingHeartRate"`
	LastSevenDaysAvgRestingHeartRate client.Number `json:"lastSevenDaysAvgRestingHeartRate"`

	AverageStressLevel client.Number `json:"averageStressLevel"`
	MaxStressLevel     client.Number `json:"maxStressLevel"`
	StressQualifier    client.Text   `json:"stressQualifier"`

	BodyBatteryCharged    client.Number `json:"bodyBatteryChargedValue"`
	BodyBatteryDrained    client.Number `json:"bodyBatteryDrainedValue"`
	BodyBatteryHighest    client.Number `json:"bodyBatteryHighestValue"`
	BodyBatteryLowest     client.Number `json:"bodyBatteryLowestValue"`
	BodyBatteryMostRecent client.Number `json:"bodyBatteryMostRecentValue"`

	AverageSpo2 client.Number `json:"averageSpo2"`
	LowestSpo2  client.Number `json:"lowestSpo2"`

	AvgWakingRespiration client.Number `json:"avgWakingRespirationValue"`
	HighestRespiration   client.Number `json:"highestRespirationValue"`
	LowestRespiration    client.Number `json:"lowestRespirationValue"`

	// PrivacyProtected reports that Garmin withheld the data from this session.
	PrivacyProtected *bool `json:"privacyProtected"`
}

// StepInterval is one bucket of the intraday step chart, 15 minutes wide on the
// observed shape. It is health data.
//
// Every field is optional: the sample carried no null, but that is one day of one
// account, and pushes exists-and-is-zero on a walking account, so absent must stay
// distinguishable from zero. PrimaryActivityLevel is an open enum — four values were
// observed — so it stays a string and an unknown value is never refused. The two
// timestamps are GMT with no local pair, unlike the heart-rate document, and carry
// Garmin's own 2006-01-02T15:04:05.0 layout, which is not reparsed here.
type StepInterval struct {
	StartGMT              client.Text   `json:"startGMT"`
	EndGMT                client.Text   `json:"endGMT"`
	Steps                 client.Number `json:"steps"`
	Pushes                client.Number `json:"pushes"`
	PrimaryActivityLevel  client.Text   `json:"primaryActivityLevel"`
	ActivityLevelConstant *bool         `json:"activityLevelConstant"`
}

// DailyStepsEntry is one calendar day of the step aggregate. It is health data.
//
// It is deliberately not StepInterval: get_steps_data returns fifteen-minute buckets
// with startGMT, endGMT, pushes and primaryActivityLevel, while this document is a
// flat per-day record. The two tools differ by one word and share no shape, so they
// share no type either.
//
// Every field is optional. All four were present across the sampled days, which is
// three days of one account and not a guarantee. CalendarDate is what identifies the
// record: a day the watch was not worn may be missing from the array altogether, and
// nothing promises the order, so no caller may key a day by its position.
type DailyStepsEntry struct {
	CalendarDate *string       `json:"calendarDate"`
	TotalSteps   client.Number `json:"totalSteps"`
	// TotalDistance carries no unit: neither the document nor upstream states one.
	// It is presumably metres and is passed on unlabelled rather than renamed to a
	// unit this project has never been told.
	TotalDistance client.Number `json:"totalDistance"`
	StepGoal      client.Number `json:"stepGoal"`
}

// WeeklyStepsEntry is one week of the step aggregate. Source of the wire names:
// get_weekly_steps upstream, which reads calendarDate off the entry and the totals
// out of the nested values.
type WeeklyStepsEntry struct {
	CalendarDate *string            `json:"calendarDate"`
	Values       *WeeklyStepsValues `json:"values"`
}

// WeeklyStepsValues is the nested aggregate of one week.
type WeeklyStepsValues struct {
	TotalSteps            client.Number `json:"totalSteps"`
	AverageSteps          client.Number `json:"averageSteps"`
	TotalDistance         client.Number `json:"totalDistance"`
	AverageDistance       client.Number `json:"averageDistance"`
	WellnessDataDaysCount client.Number `json:"wellnessDataDaysCount"`
}

// WeeklyIntensityEntry is one week of the intensity-minutes aggregate. Source of the
// wire names: get_weekly_intensity_minutes upstream.
type WeeklyIntensityEntry struct {
	CalendarDate  *string       `json:"calendarDate"`
	WeeklyGoal    client.Number `json:"weeklyGoal"`
	ModerateValue client.Number `json:"moderateValue"`
	VigorousValue client.Number `json:"vigorousValue"`
}

// Stats reads one day of the account's totals. A privacy-protected payload is an
// authentication failure and an empty body an unexpected response, matching
// get_stats, which delegates to get_user_summary.
func (w *WellnessDaily) Stats(
	ctx context.Context, session client.Session, name client.DisplayName, date client.Date,
) (DailyStats, error) {
	return w.stats(ctx, session, name, date, client.OpGetStats)
}

// stats is Stats with a caller-owned operation label, so the composed
// get_stats_and_body read is not logged as a plain get_stats.
func (w *WellnessDaily) stats(
	ctx context.Context, session client.Session, name client.DisplayName,
	date client.Date, op client.Op,
) (DailyStats, error) {
	query := url.Values{}
	query.Set(client.QueryCalendarDate, date.String())
	req := readRequest(op, client.EndpointUserSummary,
		displayNamePath(client.PathUserSummaryPrefix, name), query)

	if err := requireDisplayName(req, name); err != nil {
		return DailyStats{}, err
	}
	if err := requireDate(req, date); err != nil {
		return DailyStats{}, err
	}

	var stats DailyStats
	payload, err := w.req.read(ctx, session, req, &stats)
	if err != nil {
		return DailyStats{}, err
	}
	if payload.NoContent() {
		return DailyStats{}, unexpected(req, fmt.Errorf(
			"%w: the daily stats response carried no data", client.ErrUnexpectedResponse))
	}
	if stats.PrivacyProtected != nil && *stats.PrivacyProtected {
		return DailyStats{}, rejected(req, fmt.Errorf(
			"%w: Garmin withheld the daily stats from this session", client.ErrAuthentication))
	}
	return stats, nil
}

// StepsIntervals reads the intraday step chart for one day. A null response is no
// data rather than a failure, matching get_steps_data.
func (w *WellnessDaily) StepsIntervals(
	ctx context.Context, session client.Session, name client.DisplayName, date client.Date,
) ([]StepInterval, error) {
	query := url.Values{}
	query.Set(client.QueryDate, date.String())
	req := readRequest(client.OpGetStepsData, client.EndpointDailySummaryChart,
		displayNamePath(client.PathDailySummaryChartPrefix, name), query)

	if err := requireDisplayName(req, name); err != nil {
		return nil, err
	}
	if err := requireDate(req, date); err != nil {
		return nil, err
	}

	var list client.List[StepInterval]
	if _, err := w.req.read(ctx, session, req, &list); err != nil {
		return nil, err
	}
	return list.Items(), nil
}

// DailySteps reads the daily step aggregate for an inclusive window. Garmin answers
// at most DailyStepsChunkDays days per request, so a wider window is split and the
// chunks are concatenated in date order.
func (w *WellnessDaily) DailySteps(
	ctx context.Context, session client.Session, span client.DateRange,
) ([]DailyStepsEntry, error) {
	probe := w.rangeRequest(client.OpGetDailySteps, client.EndpointDailyStepsStats,
		client.PathDailyStepsStatsPrefix, span)
	if err := w.requireWindow(probe, span); err != nil {
		return nil, err
	}

	var all []DailyStepsEntry
	for start := span.Start(); !start.Time().After(span.End().Time()); {
		end := start.AddDays(DailyStepsChunkDays - 1)
		if end.Time().After(span.End().Time()) {
			end = span.End()
		}
		chunk, err := client.NewDateRange(start, end)
		if err != nil {
			return nil, invalid(probe, err)
		}
		var list client.List[DailyStepsEntry]
		if _, err := w.req.read(ctx, session, w.rangeRequest(
			client.OpGetDailySteps, client.EndpointDailyStepsStats,
			client.PathDailyStepsStatsPrefix, chunk), &list); err != nil {
			return nil, err
		}
		all = append(all, list.Items()...)
		start = end.AddDays(1)
	}
	return all, nil
}

// WeeklySteps reads the weekly step aggregate for the given number of weeks ending
// at end.
func (w *WellnessDaily) WeeklySteps(
	ctx context.Context, session client.Session, end client.Date, weeks int,
) ([]WeeklyStepsEntry, error) {
	req := readRequest(client.OpGetWeeklySteps, client.EndpointWeeklyStepsStats,
		client.PathWeeklyStepsStatsPrefix+"/"+end.String()+"/"+strconv.Itoa(weeks), nil)

	if err := requireDate(req, end); err != nil {
		return nil, err
	}
	if weeks < 1 {
		return nil, invalid(req, fmt.Errorf("%w: the week count must be positive",
			client.ErrValidation))
	}

	var list client.List[WeeklyStepsEntry]
	if _, err := w.req.read(ctx, session, req, &list); err != nil {
		return nil, err
	}
	return list.Items(), nil
}

// WeeklyIntensityMinutes reads the weekly intensity-minutes aggregate for an
// inclusive window. Unlike the weekly step path this one takes a date range.
func (w *WellnessDaily) WeeklyIntensityMinutes(
	ctx context.Context, session client.Session, span client.DateRange,
) ([]WeeklyIntensityEntry, error) {
	req := w.rangeRequest(client.OpGetWeeklyIntensityMinutes,
		client.EndpointWeeklyIntensityMinutesStats,
		client.PathWeeklyIntensityMinutesStatsPrefix, span)
	if err := w.requireWindow(req, span); err != nil {
		return nil, err
	}

	var list client.List[WeeklyIntensityEntry]
	if _, err := w.req.read(ctx, session, req, &list); err != nil {
		return nil, err
	}
	return list.Items(), nil
}

// rangeRequest builds a read whose window is two path segments, each from a
// validated client.Date, so neither can carry a separator.
func (w *WellnessDaily) rangeRequest(
	op client.Op, endpoint client.Endpoint, prefix string, span client.DateRange,
) client.Request {
	return readRequest(op, endpoint,
		prefix+"/"+span.Start().String()+"/"+span.End().String(), nil)
}

// requireWindow refuses an unset or oversized window before anything is dispatched.
func (w *WellnessDaily) requireWindow(req client.Request, span client.DateRange) error {
	if span.IsZero() {
		return invalid(req, fmt.Errorf("%w: a date window is required for this endpoint",
			client.ErrValidation))
	}
	if err := w.req.limits().ValidateDateRange(span); err != nil {
		return invalid(req, err)
	}
	return nil
}
