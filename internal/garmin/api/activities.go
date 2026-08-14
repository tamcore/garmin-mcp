package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Activities reads the activity list.
//
// Source: get_activities and get_activities_by_date, both over
// garmin_connect_activities ("/activitylist-service/activities/search/activities").
type Activities struct {
	req requester
}

// NewActivities returns an activity client over the request layer.
func NewActivities(rc *client.Client) (*Activities, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Activities{req: req}, nil
}

// ActivityType is a validated Garmin activity-type filter, for example "running" or
// "fitness_equipment". The zero value means unfiltered.
//
// It is a strict request model: only a validated token reaches the query string.
type ActivityType struct {
	value string
}

// ParseActivityType validates an activity-type key. An empty value is the
// unfiltered zero value rather than an error, matching upstream's optional
// activitytype parameter.
func ParseActivityType(value string) (ActivityType, error) {
	token, present, err := parseLowerToken(value, "activity type")
	if err != nil {
		return ActivityType{}, err
	}
	if !present {
		return ActivityType{}, nil
	}
	return ActivityType{value: token}, nil
}

// IsZero reports whether the filter is unset.
func (t ActivityType) IsZero() bool { return t.value == "" }

// String is the validated activity-type key, or "".
func (t ActivityType) String() string { return t.value }

// SortOrder is the validated sort direction. Source: the sortorder parameter of
// get_activities_by_date, where Garmin defaults to descending by startLocal.
type SortOrder string

// Sort directions. The zero value leaves Garmin's default in place.
const (
	SortDefault    SortOrder = ""
	SortAscending  SortOrder = "asc"
	SortDescending SortOrder = "desc"
)

// ParseSortOrder validates a sort direction.
func ParseSortOrder(value string) (SortOrder, error) {
	switch SortOrder(value) {
	case SortDefault:
		return SortDefault, nil
	case SortAscending:
		return SortAscending, nil
	case SortDescending:
		return SortDescending, nil
	default:
		return SortDefault, fmt.Errorf("%w: sort order must be %q or %q",
			client.ErrValidation, SortAscending, SortDescending)
	}
}

// Activity is one activity summary.
//
// It is sensitive: it carries health measurements and the start coordinates of a
// real outing, so it must not be logged. Fields Garmin renames or drops between
// releases are optional pointers or union decoders, and activityType keeps its raw
// shape because upstream sends both an object and a bare key.
type Activity struct {
	ActivityID     *int64          `json:"activityId"`
	ActivityName   *string         `json:"activityName"`
	Description    *string         `json:"description"`
	StartTimeLocal *string         `json:"startTimeLocal"`
	StartTimeGMT   *string         `json:"startTimeGMT"`
	ActivityType   json.RawMessage `json:"activityType"`
	EventType      json.RawMessage `json:"eventType"`
	Distance       client.Number   `json:"distance"`
	Duration       client.Number   `json:"duration"`
	ElapsedTime    client.Number   `json:"elapsedDuration"`
	MovingTime     client.Number   `json:"movingDuration"`
	Calories       client.Number   `json:"calories"`
	AverageHR      client.Number   `json:"averageHR"`
	MaxHR          client.Number   `json:"maxHR"`
	StartLatitude  client.Number   `json:"startLatitude"`
	StartLongitude client.Number   `json:"startLongitude"`
	OwnerID        client.Number   `json:"ownerId"`
	Favorite       *bool           `json:"favorite"`
}

// activityEnvelope decodes both shapes the search endpoint answers with: a bare
// array, and an object that carries the array under a key. Upstream only ever sees
// the array, but a deployment behind a gateway has been observed wrapping it, and a
// wrapper must not turn a useful response into a decode failure.
type activityEnvelope struct {
	activities []Activity
}

// UnmarshalJSON accepts an array, an object with activityList or activities, and
// null.
func (e *activityEnvelope) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*e = activityEnvelope{}
		return nil
	}
	if trimmed[0] == '[' {
		var items []Activity
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return err
		}
		*e = activityEnvelope{activities: items}
		return nil
	}

	var wrapper struct {
		ActivityList []Activity `json:"activityList"`
		Activities   []Activity `json:"activities"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err != nil {
		return err
	}
	items := wrapper.ActivityList
	if items == nil {
		items = wrapper.Activities
	}
	*e = activityEnvelope{activities: items}
	return nil
}

// ActivityPage is one page of activities plus its retained raw payload.
//
// It is sensitive for the same reason Activity is.
type ActivityPage struct {
	Activities []Activity `json:"activities"`

	raw client.Payload
}

// Payload is the retained raw response.
func (p ActivityPage) Payload() client.Payload { return p.raw }

// ListQuery is a validated request for one page of activities.
type ListQuery struct {
	// Page is the pagination window. It must be a validated client.Page.
	Page client.Page
	// Type filters by activity type. The zero value is unfiltered.
	Type ActivityType
}

// List reads one page of activities.
func (a *Activities) List(
	ctx context.Context, session client.Session, query ListQuery,
) (ActivityPage, error) {
	req := readRequest(client.OpListActivities, client.EndpointActivitySearch,
		client.PathActivitySearch, activityQuery(query.Page, query.Type, client.DateRange{}, SortDefault))

	if err := a.req.limits().ValidatePage(query.Page); err != nil {
		return ActivityPage{}, invalid(req, err)
	}

	var envelope activityEnvelope
	payload, err := a.req.read(ctx, session, req, &envelope)
	if err != nil {
		return ActivityPage{}, err
	}
	return ActivityPage{Activities: envelope.activities, raw: payload}, nil
}

// DateQuery is a validated request for every activity inside a date window.
type DateQuery struct {
	// Span is the inclusive date window. It must be a validated client.DateRange.
	Span client.DateRange
	// Type filters by activity type. The zero value is unfiltered.
	Type ActivityType
	// Sort selects the direction. The zero value leaves Garmin's default.
	Sort SortOrder
	// PageSize is the page size to walk with. Zero means the configured default.
	PageSize int
}

// ListByDate reads every activity inside the query's date window, walking the pages
// Garmin serves.
//
// Source: get_activities_by_date, which pages 20 at a time until Garmin returns an
// empty page. Three bounds apply that the caller cannot lift: the date window, the
// page size, and the page count. A server that never returns a short or empty page
// hits the page bound and the read fails with ErrPaginationExhausted, because
// silently truncating a health history is worse than an actionable error — the same
// decision upstream made with MAX_PAGINATED_REQUESTS.
func (a *Activities) ListByDate(
	ctx context.Context, session client.Session, query DateQuery,
) ([]Activity, error) {
	limits := a.req.limits()
	req := readRequest(client.OpListActivitiesByDate, client.EndpointActivitySearch,
		client.PathActivitySearch, nil)

	if err := limits.ValidateDateRange(query.Span); err != nil {
		return nil, invalid(req, err)
	}
	page, err := client.NewPage(0, pageSizeOrDefault(query.PageSize, limits))
	if err != nil {
		return nil, invalid(req, err)
	}
	if err := limits.ValidatePage(page); err != nil {
		return nil, invalid(req, err)
	}
	return a.walkPages(ctx, session, query, page, limits)
}

// walkPages fetches successive pages until one is short or empty, or the page bound
// is reached.
func (a *Activities) walkPages(
	ctx context.Context, session client.Session, query DateQuery, page client.Page, limits client.Limits,
) ([]Activity, error) {
	var all []Activity

	for range limits.MaxPages {
		req := readRequest(client.OpListActivitiesByDate, client.EndpointActivitySearch,
			client.PathActivitySearch, activityQuery(page, query.Type, query.Span, query.Sort))

		var envelope activityEnvelope
		if _, err := a.req.read(ctx, session, req, &envelope); err != nil {
			return nil, err
		}
		all = append(all, envelope.activities...)
		if len(envelope.activities) < page.Limit() {
			return all, nil
		}
		page = page.Next()
	}

	req := readRequest(client.OpListActivitiesByDate, client.EndpointActivitySearch,
		client.PathActivitySearch, nil)
	return nil, unexpected(req, fmt.Errorf("%w after %d pages",
		client.ErrPaginationExhausted, limits.MaxPages))
}

// pageSizeOrDefault resolves a requested page size against the configured bound.
func pageSizeOrDefault(requested int, limits client.Limits) int {
	if requested <= 0 {
		return limits.MaxPageSize
	}
	return requested
}

// activityQuery builds the query parameters for a search request.
func activityQuery(
	page client.Page, activityType ActivityType, span client.DateRange, sort SortOrder,
) url.Values {
	query := url.Values{}
	query.Set(client.QueryStart, strconv.Itoa(page.Start()))
	query.Set(client.QueryLimit, strconv.Itoa(page.Limit()))
	if !activityType.IsZero() {
		query.Set(client.QueryActivityType, activityType.String())
	}
	if !span.IsZero() {
		query.Set(client.QueryStartDate, span.Start().String())
		query.Set(client.QueryEndDate, span.End().String())
	}
	if sort != SortDefault {
		query.Set(client.QuerySortOrder, string(sort))
	}
	return query
}
