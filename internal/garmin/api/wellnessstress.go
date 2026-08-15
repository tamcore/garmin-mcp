package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// MaxStressWeeks bounds the week count the weekly stress aggregate accepts. Source:
// get_weekly_stress, whose upstream MCP caller clamps weeks to 52.
const MaxStressWeeks = 52

// InputContextAfterWakeupReset is the training-readiness snapshot Garmin records after
// waking. Source: get_morning_training_readiness, which selects that inputContext.
const InputContextAfterWakeupReset = "AFTER_WAKEUP_RESET"

// WellnessStress reads the stress, body-battery and training-readiness endpoints.
// Every read answers with health data, and every field is optional: a new account, a
// day the watch was not worn and a night with no sleep all produce an empty or partial
// document, which is a normal state and never an error.
type WellnessStress struct {
	req requester
}

// NewWellnessStress returns a stress client over the request layer.
func NewWellnessStress(rc *client.Client) (*WellnessStress, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &WellnessStress{req: req}, nil
}

// NewWellnessStressFrom returns a stress client sharing the request layer of an
// existing wellness client, for a caller holding a domain client rather than a layer.
func NewWellnessStressFrom(w *Wellness) (*WellnessStress, error) {
	if w == nil {
		return nil, fmt.Errorf("garmin api: stress client needs a wellness client: %w",
			client.ErrNotConfigured)
	}
	return &WellnessStress{req: w.req}, nil
}

// StressView names which caller is asking for the daily stress document. Source:
// get_stress_data, get_stress_summary and get_all_day_stress build the identical URL
// and differ only in what they keep. One read serves all three, and the view selects
// only the operation label a log line carries.
type StressView int

// The three views of the one daily stress read.
const (
	StressViewFull StressView = iota
	StressViewSummary
	StressViewAllDay
)

// op is the sanitized operation label.
func (v StressView) op() (client.Op, bool) {
	switch v {
	case StressViewFull:
		return client.OpGetStressData, true
	case StressViewSummary:
		return client.OpGetStressSummary, true
	case StressViewAllDay:
		return client.OpGetAllDayStress, true
	default:
		return "", false
	}
}

// StressSample is one point of the day's stress series. Source: the stressValuesArray
// pairs get_stress_summary reads, a timestamp then a level. Both are optional, and a
// drifted element shape decodes to an absent sample rather than failing the whole day.
type StressSample struct {
	Timestamp client.Number
	Level     client.Number
}

// UnmarshalJSON accepts the two-element array Garmin sends and treats anything else —
// null, a shorter array, an object — as an absent sample.
func (s *StressSample) UnmarshalJSON(data []byte) error {
	*s = StressSample{}

	var pair []client.Number
	if err := json.Unmarshal(data, &pair); err != nil {
		return nil
	}
	if len(pair) > 0 {
		s.Timestamp = pair[0]
	}
	if len(pair) > 1 {
		s.Level = pair[1]
	}
	return nil
}

// DailyStress is one calendar day of stress. It is health data: never log it.
//
// Source: the fields get_stress_summary reads out of the daily stress document —
// calendarDate, maxStressLevel, avgStressLevel and stressValuesArray.
type DailyStress struct {
	CalendarDate   *string                   `json:"calendarDate"`
	MaxStressLevel client.Number             `json:"maxStressLevel"`
	AvgStressLevel client.Number             `json:"avgStressLevel"`
	Values         client.List[StressSample] `json:"stressValuesArray"`
}

// IsEmpty reports a day Garmin holds nothing for.
func (s DailyStress) IsEmpty() bool {
	return s.CalendarDate == nil && !s.MaxStressLevel.IsSet() && s.Values.Len() == 0 &&
		!s.AvgStressLevel.IsSet()
}

// StressDistribution counts the day's readings per Garmin's four stress bands. Source:
// the thresholds get_stress_summary applies — rest under 26, low 26 to 50, medium 51 to
// 75, high 76 and above — over the strictly positive readings. A negative level marks a
// gap or an activity window, so it counts as unusable, not as a reading.
type StressDistribution struct {
	Rest, Low, Medium, High int
	Valid, Samples          int
}

// Distribution computes the day's stress distribution.
func (s DailyStress) Distribution() StressDistribution {
	out := StressDistribution{}
	for _, sample := range s.Values.Items() {
		out.Samples++
		level, ok := sample.Level.Float64()
		if !ok || level <= 0 {
			continue
		}
		out.Valid++
		switch {
		case level < 26:
			out.Rest++
		case level < 51:
			out.Low++
		case level < 76:
			out.Medium++
		default:
			out.High++
		}
	}
	return out
}

// DailyStress reads one day of stress for the view that asked.
func (w *WellnessStress) DailyStress(
	ctx context.Context, session client.Session, date client.Date, view StressView,
) (DailyStress, error) {
	op, ok := view.op()
	if !ok {
		return DailyStress{}, invalid(
			readRequest(client.OpGetStressData, client.EndpointDailyStress, "", nil),
			fmt.Errorf("%w: unknown stress view", client.ErrValidation))
	}
	req := readRequest(op, client.EndpointDailyStress,
		w.datedPath(client.PathDailyStressPrefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return DailyStress{}, err
	}

	var stress DailyStress
	if _, err := w.req.read(ctx, session, req, &stress); err != nil {
		return DailyStress{}, err
	}
	return stress, nil
}

// WeeklyStress is one week of the stress aggregate. Source: the documented answer of
// get_weekly_stress — a value and the week's start date.
type WeeklyStress struct {
	CalendarDate *string       `json:"calendarDate"`
	Value        client.Number `json:"value"`
}

// WeeklyStress reads the weekly stress aggregate for the weeks ending at end. An empty
// answer is no weeks rather than an error: a new account is a normal state.
func (w *WellnessStress) WeeklyStress(
	ctx context.Context, session client.Session, end client.Date, weeks int,
) ([]WeeklyStress, error) {
	req := readRequest(client.OpGetWeeklyStress, client.EndpointWeeklyStressStats,
		w.datedPath(client.PathWeeklyStressStatsPrefix, end)+"/"+strconv.Itoa(weeks), nil)
	if err := requireDate(req, end); err != nil {
		return nil, err
	}
	if weeks < 1 || weeks > MaxStressWeeks {
		return nil, invalid(req, fmt.Errorf("%w: weeks must be 1 to %d",
			client.ErrValidation, MaxStressWeeks))
	}

	var out client.List[WeeklyStress]
	if _, err := w.req.read(ctx, session, req, &out); err != nil {
		return nil, err
	}
	return out.Items(), nil
}

// BodyBatteryEvent is one event of a body-battery day. Source: the
// bodyBatteryActivityEvent fields get_body_battery's upstream caller reads.
type BodyBatteryEvent struct {
	EventType      client.Text   `json:"eventType"`
	StartTimeGMT   client.Text   `json:"eventStartTimeGmt"`
	DurationMillis client.Number `json:"durationInMilliseconds"`
	Impact         client.Number `json:"bodyBatteryImpact"`
	ShortFeedback  client.Text   `json:"shortFeedback"`
}

// BodyBatteryFeedback is the day's dynamic feedback block. Source: the
// bodyBatteryDynamicFeedbackEvent fields the same caller reads.
type BodyBatteryFeedback struct {
	FeedbackShortType client.Text   `json:"feedbackShortType"`
	BodyBatteryLevel  client.Number `json:"bodyBatteryLevel"`
}

// BodyBatteryDay is one day of the body-battery report. It is health data: never log
// it.
type BodyBatteryDay struct {
	Date            *string                       `json:"date"`
	Charged         client.Number                 `json:"charged"`
	Drained         client.Number                 `json:"drained"`
	ActivityEvents  client.List[BodyBatteryEvent] `json:"bodyBatteryActivityEvent"`
	DynamicFeedback *BodyBatteryFeedback          `json:"bodyBatteryDynamicFeedbackEvent"`
}

// BodyBattery reads the body-battery day report over an inclusive window. Source:
// get_body_battery, which filters the daily report by startDate and endDate.
func (w *WellnessStress) BodyBattery(
	ctx context.Context, session client.Session, span client.DateRange,
) ([]BodyBatteryDay, error) {
	query := url.Values{}
	query.Set(client.QueryStartDate, span.Start().String())
	query.Set(client.QueryEndDate, span.End().String())

	req := readRequest(client.OpGetBodyBattery, client.EndpointBodyBatteryDaily,
		client.PathBodyBatteryDaily, query)
	if span.IsZero() {
		return nil, invalid(req, fmt.Errorf("%w: a date window is required for this endpoint",
			client.ErrValidation))
	}
	if err := w.req.limits().ValidateDateRange(span); err != nil {
		return nil, invalid(req, err)
	}

	var out client.List[BodyBatteryDay]
	if _, err := w.req.read(ctx, session, req, &out); err != nil {
		return nil, err
	}
	return out.Items(), nil
}

// BodyBatteryEvents reads one day of body-battery events. Upstream returns the
// document unchanged and curates nothing, so the per-event field names are not
// established by the pinned source: each event is retained whole rather than mapped
// onto invented names, and the caller bounds how many it renders.
func (w *WellnessStress) BodyBatteryEvents(
	ctx context.Context, session client.Session, date client.Date,
) ([]json.RawMessage, error) {
	req := readRequest(client.OpGetBodyBatteryEvents, client.EndpointBodyBatteryEvents,
		w.datedPath(client.PathBodyBatteryEventsPrefix, date), nil)
	return w.rawDocuments(ctx, session, req, date)
}

// AllDayEvents reads one day of wellness events. Upstream returns this document
// unchanged too, so the event shape is not established and each event is retained
// whole. Source: get_all_day_events, which keys the read by the calendarDate
// parameter rather than by a path segment.
func (w *WellnessStress) AllDayEvents(
	ctx context.Context, session client.Session, date client.Date,
) ([]json.RawMessage, error) {
	query := url.Values{}
	query.Set(client.QueryCalendarDate, date.String())

	req := readRequest(client.OpGetAllDayEvents, client.EndpointDailyEvents,
		client.PathDailyEvents, query)
	return w.rawDocuments(ctx, session, req, date)
}

// rawDocuments performs a date-keyed read whose element shape upstream does not
// establish, tolerating an array, a single object, null and an empty body.
func (w *WellnessStress) rawDocuments(
	ctx context.Context, session client.Session, req client.Request, date client.Date,
) ([]json.RawMessage, error) {
	if err := requireDate(req, date); err != nil {
		return nil, err
	}

	var out client.List[json.RawMessage]
	if _, err := w.req.read(ctx, session, req, &out); err != nil {
		return nil, err
	}
	return out.Items(), nil
}

// ReadinessView names which caller is asking for the training-readiness document.
// Source: get_morning_training_readiness, which calls get_training_readiness and keeps
// one entry. One read serves both, and the view selects the operation label.
type ReadinessView int

// The two views of the one training-readiness read.
const (
	ReadinessViewAll ReadinessView = iota
	ReadinessViewMorning
)

// op is the sanitized operation label.
func (v ReadinessView) op() (client.Op, bool) {
	switch v {
	case ReadinessViewAll:
		return client.OpGetTrainingReadiness, true
	case ReadinessViewMorning:
		return client.OpGetMorningTrainingReadiness, true
	default:
		return "", false
	}
}

// Readiness is one training-readiness snapshot. It is health data: never log it.
// Source: the fields get_training_readiness's upstream caller curates. Every one is
// optional, because the set a device populates varies by model and by firmware.
type Readiness struct {
	CalendarDate               *string       `json:"calendarDate"`
	TimestampLocal             client.Text   `json:"timestampLocal"`
	InputContext               client.Text   `json:"inputContext"`
	Level                      client.Text   `json:"level"`
	Score                      client.Number `json:"score"`
	FeedbackShort              client.Text   `json:"feedbackShort"`
	SleepScore                 client.Number `json:"sleepScore"`
	SleepScoreFactorPercent    client.Number `json:"sleepScoreFactorPercent"`
	SleepScoreFactorFeed       client.Text   `json:"sleepScoreFactorFeedback"`
	RecoveryTime               client.Number `json:"recoveryTime"`
	RecoveryTimeFactorPercent  client.Number `json:"recoveryTimeFactorPercent"`
	RecoveryTimeFactorFeed     client.Text   `json:"recoveryTimeFactorFeedback"`
	ACWRFactorPercent          client.Number `json:"acwrFactorPercent"`
	ACWRFactorFeed             client.Text   `json:"acwrFactorFeedback"`
	AcuteLoad                  client.Number `json:"acuteLoad"`
	HRVFactorPercent           client.Number `json:"hrvFactorPercent"`
	HRVFactorFeed              client.Text   `json:"hrvFactorFeedback"`
	HRVWeeklyAverage           client.Number `json:"hrvWeeklyAverage"`
	StressHistoryFactorPercent client.Number `json:"stressHistoryFactorPercent"`
	StressHistoryFactorFeed    client.Text   `json:"stressHistoryFactorFeedback"`
	SleepHistoryFactorPercent  client.Number `json:"sleepHistoryFactorPercent"`
	SleepHistoryFactorFeed     client.Text   `json:"sleepHistoryFactorFeedback"`
}

// TrainingReadiness reads one day of readiness snapshots for the view that asked. An
// empty answer is no snapshots rather than an error.
func (w *WellnessStress) TrainingReadiness(
	ctx context.Context, session client.Session, date client.Date, view ReadinessView,
) ([]Readiness, error) {
	op, ok := view.op()
	if !ok {
		return nil, invalid(
			readRequest(client.OpGetTrainingReadiness, client.EndpointTrainingReadiness, "", nil),
			fmt.Errorf("%w: unknown readiness view", client.ErrValidation))
	}
	req := readRequest(op, client.EndpointTrainingReadiness,
		w.datedPath(client.PathTrainingReadinessPrefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return nil, err
	}

	var out client.List[Readiness]
	if _, err := w.req.read(ctx, session, req, &out); err != nil {
		return nil, err
	}
	return out.Items(), nil
}

// MorningReadiness selects the snapshot taken after waking. Source:
// get_morning_training_readiness, which takes the first entry whose
// inputContext is AFTER_WAKEUP_RESET and, when no entry carries that context, falls
// back to the first entry — not every device populates the field. The results are the
// entry, whether the context matched, and whether there was an entry at all.
func MorningReadiness(entries []Readiness) (Readiness, bool, bool) {
	for _, entry := range entries {
		if value, ok := entry.InputContext.Value(); ok && value == InputContextAfterWakeupReset {
			return entry, true, true
		}
	}
	if len(entries) == 0 {
		return Readiness{}, false, false
	}
	return entries[0], false, true
}

// datedPath appends a validated calendar date as one path segment. A client.Date is
// parsed from YYYY-MM-DD, so it can carry no separator and no traversal segment.
func (w *WellnessStress) datedPath(prefix string, date client.Date) string {
	if date.IsZero() {
		return prefix
	}
	return prefix + "/" + date.String()
}
