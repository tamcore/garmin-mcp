package api

import (
	"encoding/json"
	"log/slog"
	"slices"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The two documents only the VO2 max trend reads: the max-metrics series, which covers
// a whole window in one request, and the account settings document it falls back to.
// The aggregated training status its per-day fallback reads lives in trainingstatus.go.

// MaxMetricsDay is one day of the max-metrics series.
type MaxMetricsDay struct {
	CalendarDate *string      `json:"calendarDate"`
	Generic      *VO2MaxEntry `json:"generic"`
	Cycling      *VO2MaxEntry `json:"cycling"`
}

// Day reports the calendar date of the entry, preferring the per-sport section's own
// date. Source: upstream's _extract_dated_vo2_measurements, which reads the section's
// calendarDate first and falls back to the entry's.
func (d MaxMetricsDay) Day() (string, bool) {
	for _, section := range []*VO2MaxEntry{d.Generic, d.Cycling} {
		if section != nil && section.CalendarDate != nil && *section.CalendarDate != "" {
			return *section.CalendarDate, true
		}
	}
	if d.CalendarDate != nil && *d.CalendarDate != "" {
		return *d.CalendarDate, true
	}
	return "", false
}

// MaxMetrics is the max-metrics range response, which arrives as a list.
type MaxMetrics struct {
	days []MaxMetricsDay

	raw client.Payload
}

// UnmarshalJSON accepts the list form and the single-object form.
func (m *MaxMetrics) UnmarshalJSON(data []byte) error {
	var list []MaxMetricsDay
	if err := json.Unmarshal(data, &list); err == nil {
		m.days = list
		return nil
	}
	var single MaxMetricsDay
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	m.days = []MaxMetricsDay{single}
	return nil
}

// Payload is the retained raw response.
func (m MaxMetrics) Payload() client.Payload { return m.raw }

// Days returns a copy of the decoded entries.
func (m MaxMetrics) Days() []MaxMetricsDay { return slices.Clone(m.days) }

// LogValue reports the shape of the series, never a measurement.
func (m MaxMetrics) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "maxMetrics"),
		slog.Int("days", len(m.days)),
		slog.Any("payload", m.raw),
	)
}

// profileVO2MaxData is the userData section of the account settings document.
type profileVO2MaxData struct {
	VO2MaxRunning client.Number `json:"vo2MaxRunning"`
	VO2MaxCycling client.Number `json:"vo2MaxCycling"`
}

// ProfileVO2Max is the VO2 max the account settings document carries.
//
// Source: upstream's get_user_profile fallback, which reads the user-settings document
// and whose candidate paths include userData.vo2MaxRunning and userData.vo2MaxCycling.
type ProfileVO2Max struct {
	UserData *profileVO2MaxData `json:"userData"`

	raw client.Payload
}

// Payload is the retained raw response.
func (p ProfileVO2Max) Payload() client.Payload { return p.raw }

// LogValue reports whether the settings document carried the section.
func (p ProfileVO2Max) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "profileVO2Max"),
		slog.String("userData", presence(p.UserData != nil)),
		slog.Any("payload", p.raw),
	)
}

// Running is the profile's running estimate, when it carries one.
func (p ProfileVO2Max) Running() (client.Number, bool) {
	if p.UserData == nil || !p.UserData.VO2MaxRunning.IsSet() {
		return client.Number{}, false
	}
	return p.UserData.VO2MaxRunning, true
}

// Cycling is the profile's cycling estimate, when it carries one.
func (p ProfileVO2Max) Cycling() (client.Number, bool) {
	if p.UserData == nil || !p.UserData.VO2MaxCycling.IsSet() {
		return client.Number{}, false
	}
	return p.UserData.VO2MaxCycling, true
}
