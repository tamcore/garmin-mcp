package api

import (
	"log/slog"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// HRVBaseline is the personal baseline band of the HRV summary.
//
// Source: the balancedLow, balancedUpper and lowUpper upstream reads off
// hrvSummary["baseline"].
type HRVBaseline struct {
	BalancedLow   client.Number `json:"balancedLow"`
	BalancedUpper client.Number `json:"balancedUpper"`
	LowUpper      client.Number `json:"lowUpper"`
}

// HRVSummary is the summary section of the HRV document.
//
// LastNightAvg and LastNight are both decoded because upstream reads both spellings:
// get_hrv_data reads lastNightAvg and get_hrv_trend reads lastNight. Neither is
// assumed present; NightAverage prefers the documented lastNightAvg and falls back.
type HRVSummary struct {
	CalendarDate      *string       `json:"calendarDate"`
	LastNightAvg      client.Number `json:"lastNightAvg"`
	LastNight         client.Number `json:"lastNight"`
	LastNight5MinHigh client.Number `json:"lastNight5MinHigh"`
	WeeklyAvg         client.Number `json:"weeklyAvg"`
	Status            client.Text   `json:"status"`
	FeedbackPhrase    client.Text   `json:"feedbackPhrase"`
	Baseline          *HRVBaseline  `json:"baseline"`
}

// NightAverage is last night's average, from whichever spelling arrived.
func (s HRVSummary) NightAverage() client.Number {
	if s.LastNightAvg.IsSet() {
		return s.LastNightAvg
	}
	return s.LastNight
}

// HRVReading is one five-minute reading of the intraday series.
type HRVReading struct {
	ReadingTimeLocal client.Text   `json:"readingTimeLocal"`
	ReadingTimeGMT   client.Text   `json:"readingTimeGMT"`
	HRVValue         client.Number `json:"hrvValue"`
}

// HRVDay is one day of heart-rate variability.
//
// userProfilePK is deliberately not decoded: it is an account identifier. The sleep
// window timestamps arrive without a zone and are carried as text, never parsed.
type HRVDay struct {
	Summary                  *HRVSummary  `json:"hrvSummary"`
	SleepStartTimestampLocal client.Text  `json:"sleepStartTimestampLocal"`
	SleepEndTimestampLocal   client.Text  `json:"sleepEndTimestampLocal"`
	SleepStartTimestampGMT   client.Text  `json:"sleepStartTimestampGMT"`
	SleepEndTimestampGMT     client.Text  `json:"sleepEndTimestampGMT"`
	Readings                 []HRVReading `json:"hrvReadings"`

	raw client.Payload
}

// Payload is the retained raw response.
func (d HRVDay) Payload() client.Payload { return d.raw }

// LogValue reports the shape of the day and never a reading.
func (d HRVDay) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "hrvDay"),
		slog.String("summary", presence(d.Summary != nil)),
		slog.Int("readings", len(d.Readings)),
		slog.Any("payload", d.raw),
	)
}
