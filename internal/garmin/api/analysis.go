package api

import (
	"context"
	"encoding/json"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The analysis reads of one activity. Source: get_activity,
// get_activity_splits, get_activity_split_summaries, get_activity_weather,
// get_activity_hr_in_timezones, get_activity_power_in_timezones and
// get_activity_types in python-garminconnect 0.3.10. Every one of them is a
// read, so every request here carries client.EffectRead.

// ActivitySummary is one activity's own record, which is richer than the list
// entry: it carries the summary measurements and the type and event objects.
//
// It is sensitive: health measurements and the start coordinates of a real
// outing. The volatile sub-objects keep their raw shape, and an unknown field
// never fails the response.
type ActivitySummary struct {
	ActivityID   client.Number   `json:"activityId"`
	ActivityName *string         `json:"activityName"`
	Description  *string         `json:"description"`
	ActivityType json.RawMessage `json:"activityTypeDTO"`
	EventType    json.RawMessage `json:"eventTypeDTO"`
	Summary      json.RawMessage `json:"summaryDTO"`
	Metadata     json.RawMessage `json:"metadataDTO"`
	AccessRule   json.RawMessage `json:"accessControlRuleDTO"`
	TimeZoneUnit json.RawMessage `json:"timeZoneUnitDTO"`

	raw client.Payload
}

// Payload is the retained raw response.
func (a ActivitySummary) Payload() client.Payload { return a.raw }

// Summary reads one activity's own record.
func (a *ActivityDetails) Summary(
	ctx context.Context, session client.Session, id client.ID,
) (ActivitySummary, error) {
	req := readRequest(client.OpGetActivity, client.EndpointActivity, activityPath(id), nil)
	if err := requireID(req, id); err != nil {
		return ActivitySummary{}, err
	}

	var summary ActivitySummary
	payload, err := a.req.read(ctx, session, req, &summary)
	if err != nil {
		return ActivitySummary{}, err
	}
	summary.raw = payload
	return summary, nil
}

// Splits reads the untyped splits of one activity.
//
// The payload has the same four shapes the typed-splits endpoint answers with —
// an object keyed "splits", an object keyed "lapDTOs", a bare array, or a single
// bare object — so it decodes through the same union decoder.
func (a *ActivityDetails) Splits(
	ctx context.Context, session client.Session, id client.ID,
) (TypedSplits, error) {
	req := readRequest(client.OpGetActivitySplits, client.EndpointActivitySplits,
		activitySegmentPath(id, client.SegmentSplits), nil)
	if err := requireID(req, id); err != nil {
		return TypedSplits{}, err
	}

	var splits TypedSplits
	payload, err := a.req.read(ctx, session, req, &splits)
	if err != nil {
		return TypedSplits{}, err
	}
	splits.raw = payload
	return splits, nil
}

// SplitSummary is one aggregated split group, for example every climbing split
// of a bouldering session. Garmin's field set varies by activity type, so the
// measurements are union decoders.
type SplitSummary struct {
	SplitType        client.Text   `json:"splitType"`
	NoOfSplits       client.Number `json:"noOfSplits"`
	TotalAscent      client.Number `json:"totalAscent"`
	Duration         client.Number `json:"duration"`
	Distance         client.Number `json:"distance"`
	MaxDistance      client.Number `json:"maxDistance"`
	MaxElevationGain client.Number `json:"maxElevationGain"`
	AverageSpeed     client.Number `json:"averageSpeed"`
	MaxSpeed         client.Number `json:"maxSpeed"`
	Calories         client.Number `json:"calories"`
}

// SplitSummaries is the split-summary collection. Garmin sends it as a bare
// array and, for some activity types, wrapped in an object.
type SplitSummaries struct {
	Summaries client.List[SplitSummary] `json:"splitSummaries"`

	raw client.Payload
}

// Payload is the retained raw response.
func (s SplitSummaries) Payload() client.Payload { return s.raw }

// UnmarshalJSON accepts the bare array and the wrapping object.
func (s *SplitSummaries) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '[' {
		var items client.List[SplitSummary]
		if err := json.Unmarshal(data, &items); err != nil {
			return err
		}
		*s = SplitSummaries{Summaries: items}
		return nil
	}

	type wrapper SplitSummaries
	var decoded wrapper
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*s = SplitSummaries(decoded)
	return nil
}

// SplitSummaries reads the split summaries of one activity.
func (a *ActivityDetails) SplitSummaries(
	ctx context.Context, session client.Session, id client.ID,
) (SplitSummaries, error) {
	req := readRequest(client.OpGetActivitySplitSummaries, client.EndpointActivitySplitSummaries,
		activitySegmentPath(id, client.SegmentSplitSummaries), nil)
	if err := requireID(req, id); err != nil {
		return SplitSummaries{}, err
	}

	var summaries SplitSummaries
	payload, err := a.req.read(ctx, session, req, &summaries)
	if err != nil {
		return SplitSummaries{}, err
	}
	summaries.raw = payload
	return summaries, nil
}

// Weather is the weather Garmin recorded for one activity.
//
// It is location material: a weather station and a coordinate pair place a
// person, so it is never logged. Garmin reports the temperature in Fahrenheit
// from the station source regardless of account settings; converting it to the
// account's display unit belongs to the tool layer, which knows the unit system.
type Weather struct {
	Temp           client.Number   `json:"temp"`
	ApparentTemp   client.Number   `json:"apparentTemp"`
	DewPoint       client.Number   `json:"dewPoint"`
	RelativeHum    client.Number   `json:"relativeHumidity"`
	WindDirection  client.Number   `json:"windDirection"`
	WindSpeed      client.Number   `json:"windSpeed"`
	Latitude       client.Number   `json:"latitude"`
	Longitude      client.Number   `json:"longitude"`
	IssueDate      *string         `json:"issueDate"`
	WeatherType    json.RawMessage `json:"weatherTypeDTO"`
	WeatherStation json.RawMessage `json:"weatherStationDTO"`

	raw client.Payload
}

// Payload is the retained raw response.
func (w Weather) Payload() client.Payload { return w.raw }

// Weather reads the weather of one activity.
func (a *ActivityDetails) Weather(
	ctx context.Context, session client.Session, id client.ID,
) (Weather, error) {
	req := readRequest(client.OpGetActivityWeather, client.EndpointActivityWeather,
		activitySegmentPath(id, client.SegmentWeather), nil)
	if err := requireID(req, id); err != nil {
		return Weather{}, err
	}

	var weather Weather
	payload, err := a.req.read(ctx, session, req, &weather)
	if err != nil {
		return Weather{}, err
	}
	weather.raw = payload
	return weather, nil
}

// ZoneBucket is one heart-rate or power zone with the time spent in it.
//
// It is health data. Every measurement is a union decoder because Garmin sends
// them as numbers on one endpoint and as numeric strings on the other.
type ZoneBucket struct {
	ZoneNumber     client.Number `json:"zoneNumber"`
	SecsInZone     client.Number `json:"secsInZone"`
	ZoneLowBound   client.Number `json:"zoneLowBoundary"`
	ZoneHighBound  client.Number `json:"zoneHighBoundary"`
	SecsInZonePace client.Number `json:"secsInZonePace"`
}

// HRInZones reads the heart-rate time-in-zones of one activity.
func (a *ActivityDetails) HRInZones(
	ctx context.Context, session client.Session, id client.ID,
) ([]ZoneBucket, error) {
	req := readRequest(client.OpGetActivityHRInZones, client.EndpointActivityHRInZones,
		activitySegmentPath(id, client.SegmentHRInZones), nil)
	return a.readZones(ctx, session, req, id)
}

// PowerInZones reads the power time-in-zones of one activity. An activity
// recorded without a power meter answers with no zones, which is a normal state
// rather than a failure.
func (a *ActivityDetails) PowerInZones(
	ctx context.Context, session client.Session, id client.ID,
) ([]ZoneBucket, error) {
	req := readRequest(client.OpGetActivityPowerInZones, client.EndpointActivityPowerInZones,
		activitySegmentPath(id, client.SegmentPowerInZones), nil)
	return a.readZones(ctx, session, req, id)
}

// readZones performs a time-in-zones read for either zone endpoint.
func (a *ActivityDetails) readZones(
	ctx context.Context, session client.Session, req client.Request, id client.ID,
) ([]ZoneBucket, error) {
	if err := requireID(req, id); err != nil {
		return nil, err
	}

	var zones client.List[ZoneBucket]
	if _, err := a.req.read(ctx, session, req, &zones); err != nil {
		return nil, err
	}
	return zones.Items(), nil
}

// CatalogEntry is one row of Garmin's activity-type or event-type catalog. The
// triple is what an activity-type change needs, which is why the catalog is read
// before that write. Source: get_activity_types.
type CatalogEntry struct {
	TypeID       client.Number `json:"typeId"`
	TypeKey      client.Text   `json:"typeKey"`
	ParentTypeID client.Number `json:"parentTypeId"`
	IsHidden     *bool         `json:"isHidden"`
	SortOrder    client.Number `json:"sortOrder"`
}

// Types reads Garmin's activity-type catalog.
func (a *ActivityDetails) Types(
	ctx context.Context, session client.Session,
) ([]CatalogEntry, error) {
	req := readRequest(client.OpGetActivityTypes, client.EndpointActivityTypes,
		client.PathActivityTypes, nil)
	return a.readCatalog(ctx, session, req)
}

// EventTypes reads Garmin's event-type catalog, which resolves an event-type key
// to the numeric id an event-type change may carry.
func (a *ActivityDetails) EventTypes(
	ctx context.Context, session client.Session,
) ([]CatalogEntry, error) {
	req := readRequest(client.OpGetActivityEventTypes, client.EndpointActivityEventTypes,
		client.PathActivityEventTypes, nil)
	return a.readCatalog(ctx, session, req)
}

// readCatalog performs a catalog read for either type endpoint.
func (a *ActivityDetails) readCatalog(
	ctx context.Context, session client.Session, req client.Request,
) ([]CatalogEntry, error) {
	var entries client.List[CatalogEntry]
	if _, err := a.req.read(ctx, session, req, &entries); err != nil {
		return nil, err
	}
	return entries.Items(), nil
}
