package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"sync"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// NonSleepBufferMinutes is the buffer Garmin's own client sends with every daily
// sleep read. Source: the params {"date": cdate, "nonSleepBufferMinutes": 60} in
// get_sleep_data.
const NonSleepBufferMinutes = 60

// Wellness reads the date-keyed wellness endpoints: one nested object per calendar
// day.
//
// Source: garmin_connect_daily_sleep_url and garmin_connect_daily_summary_url, both
// of which take the account's display name as a path segment and the date as a query
// parameter.
type Wellness struct {
	req requester
}

// NewWellness returns a wellness client over the request layer.
func NewWellness(rc *client.Client) (*Wellness, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Wellness{req: req}, nil
}

// DailySleep is one day of sleep data.
//
// It is health data: never log it. The nested summary is optional because Garmin
// omits it for a day with no wearable data, and the stage detail keeps its raw shape
// because it varies by device generation.
type DailySleep struct {
	Summary         *SleepSummary   `json:"dailySleepDTO"`
	SleepLevels     json.RawMessage `json:"sleepLevels"`
	SleepMovement   json.RawMessage `json:"sleepMovement"`
	RestlessMoments json.RawMessage `json:"restlessMomentsTimeline"`
	SpO2Summary     json.RawMessage `json:"wellnessSpO2SleepSummaryDTO"`

	raw client.Payload
}

// Payload is the retained raw response.
func (s DailySleep) Payload() client.Payload { return s.raw }

// SleepSummary is the nested per-day sleep summary.
//
// Upstream documents that the local timestamps can be double-offset for some
// accounts and advises the GMT fields; both are kept so a caller can choose.
type SleepSummary struct {
	ID                       client.Number   `json:"id"`
	CalendarDate             *string         `json:"calendarDate"`
	SleepTimeSeconds         client.Number   `json:"sleepTimeSeconds"`
	DeepSleepSeconds         client.Number   `json:"deepSleepSeconds"`
	LightSleepSeconds        client.Number   `json:"lightSleepSeconds"`
	RemSleepSeconds          client.Number   `json:"remSleepSeconds"`
	AwakeSleepSeconds        client.Number   `json:"awakeSleepSeconds"`
	SleepStartTimestampGMT   client.Number   `json:"sleepStartTimestampGMT"`
	SleepEndTimestampGMT     client.Number   `json:"sleepEndTimestampGMT"`
	SleepStartTimestampLocal client.Number   `json:"sleepStartTimestampLocal"`
	SleepEndTimestampLocal   client.Number   `json:"sleepEndTimestampLocal"`
	AverageRespirationValue  client.Number   `json:"averageRespirationValue"`
	AverageSpO2Value         client.Number   `json:"averageSpO2Value"`
	SleepScores              json.RawMessage `json:"sleepScores"`
	SleepQualityTypeName     client.Text     `json:"sleepQualityTypeName"`
	SleepResultTypeName      client.Text     `json:"sleepResultTypeName"`
}

// UserSummary is one day of activity and wellness totals.
//
// It is health data: never log it.
type UserSummary struct {
	UserProfileID       client.Number `json:"userProfileId"`
	CalendarDate        *string       `json:"calendarDate"`
	TotalSteps          client.Number `json:"totalSteps"`
	TotalDistanceMeters client.Number `json:"totalDistanceMeters"`
	TotalKilocalories   client.Number `json:"totalKilocalories"`
	ActiveKilocalories  client.Number `json:"activeKilocalories"`
	RestingHeartRate    client.Number `json:"restingHeartRate"`
	MinHeartRate        client.Number `json:"minHeartRate"`
	MaxHeartRate        client.Number `json:"maxHeartRate"`
	AverageStressLevel  client.Number `json:"averageStressLevel"`
	BodyBatteryHighest  client.Number `json:"bodyBatteryHighestValue"`
	BodyBatteryLowest   client.Number `json:"bodyBatteryLowestValue"`
	FloorsAscended      client.Number `json:"floorsAscended"`
	// PrivacyProtected reports that Garmin withheld the data from this session.
	PrivacyProtected *bool `json:"privacyProtected"`

	raw client.Payload
}

// Payload is the retained raw response.
func (s UserSummary) Payload() client.Payload { return s.raw }

// DailySleep reads one day of sleep data for the account's display name.
func (w *Wellness) DailySleep(
	ctx context.Context, session client.Session, name client.DisplayName, date client.Date,
) (DailySleep, error) {
	req := w.sleepRequest(name, date)
	if err := requireDisplayName(req, name); err != nil {
		return DailySleep{}, err
	}
	if err := requireDate(req, date); err != nil {
		return DailySleep{}, err
	}

	var sleep DailySleep
	payload, err := w.req.read(ctx, session, req, &sleep)
	if err != nil {
		return DailySleep{}, err
	}
	sleep.raw = payload
	return sleep, nil
}

// DailySleepRange reads one day of sleep data per day of the window, with bounded
// concurrency.
//
// The window itself is bounded, and the fan-out is bounded by
// Limits.MaxConcurrency, so a wide range cannot turn one tool call into a burst that
// trips the account's rate limit. Upstream chunks the same kind of range for its
// sleep-stats endpoint; this method keeps the per-day endpoint and bounds the
// parallelism instead.
//
// The results are returned in date order regardless of completion order.
func (w *Wellness) DailySleepRange(
	ctx context.Context, session client.Session, name client.DisplayName, span client.DateRange,
) ([]DailySleep, error) {
	req := w.sleepRequest(name, span.Start())
	if err := requireDisplayName(req, name); err != nil {
		return nil, err
	}
	if err := w.req.limits().ValidateDateRange(span); err != nil {
		return nil, invalid(req, err)
	}

	days := span.Days()
	results := make([]DailySleep, days)
	var mu sync.Mutex

	err := w.req.rc.FanOut(ctx, days, func(taskCtx context.Context, index int) error {
		day, dayErr := w.DailySleep(taskCtx, session, name, span.Start().AddDays(index))
		if dayErr != nil {
			return dayErr
		}
		mu.Lock()
		results[index] = day
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// UserSummary reads one day of activity and wellness totals.
//
// A privacy-protected payload is reported as an authentication failure and an empty
// body as an unexpected response, both matching get_user_summary, which refuses to
// present either as a valid summary.
func (w *Wellness) UserSummary(
	ctx context.Context, session client.Session, name client.DisplayName, date client.Date,
) (UserSummary, error) {
	req := w.summaryRequest(name, date)
	if err := requireDisplayName(req, name); err != nil {
		return UserSummary{}, err
	}
	if err := requireDate(req, date); err != nil {
		return UserSummary{}, err
	}

	var summary UserSummary
	payload, err := w.req.read(ctx, session, req, &summary)
	if err != nil {
		return UserSummary{}, err
	}
	if payload.NoContent() {
		return UserSummary{}, unexpected(req, fmt.Errorf(
			"%w: the daily summary response carried no data", client.ErrUnexpectedResponse))
	}
	if summary.PrivacyProtected != nil && *summary.PrivacyProtected {
		return UserSummary{}, rejected(req, fmt.Errorf(
			"%w: Garmin withheld the daily summary from this session", client.ErrAuthentication))
	}
	summary.raw = payload
	return summary, nil
}

// sleepRequest builds the daily sleep read.
func (w *Wellness) sleepRequest(name client.DisplayName, date client.Date) client.Request {
	query := url.Values{}
	query.Set(client.QueryDate, date.String())
	query.Set(client.QueryNonSleepBufferMinutes, strconv.Itoa(NonSleepBufferMinutes))

	return readRequest(client.OpGetDailySleep, client.EndpointDailySleep,
		displayNamePath(client.PathDailySleepPrefix, name), query)
}

// summaryRequest builds the daily summary read.
func (w *Wellness) summaryRequest(name client.DisplayName, date client.Date) client.Request {
	query := url.Values{}
	query.Set(client.QueryCalendarDate, date.String())

	return readRequest(client.OpGetUserSummary, client.EndpointUserSummary,
		displayNamePath(client.PathUserSummaryPrefix, name), query)
}
