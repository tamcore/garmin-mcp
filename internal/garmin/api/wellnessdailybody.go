package api

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// BodyComposition is the body-composition envelope for a date window. It is health
// data.
//
// The envelope carries two time encodings at once: StartDate and EndDate are calendar
// strings echoing the request, while the nested average is keyed by epoch
// milliseconds. Neither convention is assumed for the other.
type BodyComposition struct {
	StartDate *string `json:"startDate"`
	EndDate   *string `json:"endDate"`

	// DateWeightList is nil when the key was absent or null, and non-nil with no
	// items for the empty array Garmin sends an account with no weigh-in, so the
	// two states stay apart. The element shape of a populated list has never been
	// observed, so entries are kept verbatim rather than typed.
	DateWeightList *client.List[json.RawMessage] `json:"dateWeightList"`

	// TotalAverage arrives present even when the account records nothing, with
	// every metric null. Absence is therefore modelled per metric, not by a
	// missing object.
	TotalAverage *BodyCompositionAverage `json:"totalAverage"`
}

// BodyCompositionAverage is the averaged body composition over the window.
//
// From and Until are epoch milliseconds — Until is the last millisecond of the
// window — and are the only two fields here whose type is confirmed.
//
// The ten metrics are **unconfirmed**: the sampled account records no weight, so
// every one of them came back null and nothing has demonstrated what a populated
// value looks like. They are held verbatim rather than typed, because a plausible
// guess that later proves wrong would fail the whole read, and a fixture asserting
// the guess would hide it. Type them when a populated account is sampled.
type BodyCompositionAverage struct {
	From  client.Number `json:"from"`
	Until client.Number `json:"until"`

	Weight         json.RawMessage `json:"weight"`
	BMI            json.RawMessage `json:"bmi"`
	BodyFat        json.RawMessage `json:"bodyFat"`
	BodyWater      json.RawMessage `json:"bodyWater"`
	BoneMass       json.RawMessage `json:"boneMass"`
	MuscleMass     json.RawMessage `json:"muscleMass"`
	PhysiqueRating json.RawMessage `json:"physiqueRating"`
	VisceralFat    json.RawMessage `json:"visceralFat"`
	MetabolicAge   json.RawMessage `json:"metabolicAge"`
	Trend          json.RawMessage `json:"trend"`
}

// BodyComposition reads the body-composition envelope for an inclusive date window.
func (w *WellnessDaily) BodyComposition(
	ctx context.Context, session client.Session, span client.DateRange,
) (BodyComposition, error) {
	return w.bodyComposition(ctx, session, span, client.OpGetBodyComposition)
}

// bodyComposition is BodyComposition with a caller-owned operation label.
func (w *WellnessDaily) bodyComposition(
	ctx context.Context, session client.Session, span client.DateRange, op client.Op,
) (BodyComposition, error) {
	query := url.Values{}
	query.Set(client.QueryStartDate, span.Start().String())
	query.Set(client.QueryEndDate, span.End().String())
	req := readRequest(op, client.EndpointBodyComposition, client.PathBodyComposition, query)

	if err := w.requireWindow(req, span); err != nil {
		return BodyComposition{}, err
	}

	var composition BodyComposition
	if _, err := w.req.read(ctx, session, req, &composition); err != nil {
		return BodyComposition{}, err
	}
	return composition, nil
}

// StatsAndBody reads the day's totals and the same day's body-composition average,
// which is the pair get_stats_and_body merges.
func (w *WellnessDaily) StatsAndBody(
	ctx context.Context, session client.Session, name client.DisplayName, date client.Date,
) (DailyStats, BodyComposition, error) {
	stats, err := w.stats(ctx, session, name, date, client.OpGetStatsAndBody)
	if err != nil {
		return DailyStats{}, BodyComposition{}, err
	}
	span, err := client.NewDateRange(date, date)
	if err != nil {
		return DailyStats{}, BodyComposition{}, invalid(
			readRequest(client.OpGetStatsAndBody, client.EndpointBodyComposition,
				client.PathBodyComposition, nil), err)
	}
	body, err := w.bodyComposition(ctx, session, span, client.OpGetStatsAndBody)
	if err != nil {
		return DailyStats{}, BodyComposition{}, err
	}
	return stats, body, nil
}
