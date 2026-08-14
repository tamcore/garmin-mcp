// Package api holds the Garmin Connect domain clients, split by domain rather than
// gathered into one client.
//
// Every client is a thin, immutable value over internal/garmin/client: that package
// owns URL construction, bounds, classification, retry pacing and tolerant decoding,
// and this one owns the endpoints, the typed models and the domain-specific
// validation. Nothing here sees a credential — a client.Session names the principal
// and carries the caller that attaches its token.
//
// Every method takes a context and a client.Session, returns a typed tolerant model,
// and reports every failure as a *client.APIError, so errors.Is reaches
// client.ErrNotFound, client.ErrAuthentication, client.ErrRateLimited,
// client.ErrValidation, client.ErrMalformedPayload and the rest.
//
// # Endpoints in this slice
//
// Read-only, all of them. Source: python-garminconnect 0.3.10.
//
//	Profile.Social               /userprofile-service/socialProfile                flat object
//	Profile.Settings             /userprofile-service/userprofile/user-settings     nested object
//	Profile.DisplayName          Social, validated for path use                     identity
//	Activities.List              /activitylist-service/activities/search/activities paginated array
//	Activities.ListByDate        same, with startDate and endDate                   paginated array
//	Wellness.DailySleep          /wellness-service/wellness/dailySleepData/{name}   date-keyed object
//	Wellness.DailySleepRange     same, one call per day, bounded fan-out            date-keyed objects
//	Wellness.UserSummary         /usersummary-service/usersummary/daily/{name}      date-keyed object
//	Devices.List                 /device-service/deviceregistration/devices         plain array
//	ActivityDetails.TypedSplits  /activity-service/activity/{id}/typedsplits        union-shaped
//	ActivityDetails.ExerciseSets /activity-service/activity/{id}/exerciseSets       nested unions
//
// A date is always Garmin's YYYY-MM-DD calendar form, validated as a client.Date
// before it reaches a query string. A display name is a validated client.DisplayName,
// percent-escaped into exactly one path segment, because upstream's
// _require_display_name exists precisely to stop an unset or hostile name from
// reaching a URL path.
//
// # Bounds enforced here
//
//   - a page size is checked against the configured maximum before dispatch;
//   - a date window is checked against the configured maximum before dispatch;
//   - Activities.ListByDate walks at most Limits.MaxPages pages and reports
//     client.ErrPaginationExhausted rather than truncating a health history, which is
//     upstream's MAX_PAGINATED_REQUESTS decision;
//   - Wellness.DailySleepRange fans out at most Limits.MaxConcurrency requests at a
//     time, so a wide window cannot become a burst that trips the account's rate
//     limit.
//
// # Tolerant models
//
// Optional fields are pointers, volatile sub-objects keep json.RawMessage, and a
// measurement that arrives as a number on one device and a numeric string on another
// is a client.Number. A collection Garmin sends sometimes as an array and sometimes as
// a single object is a client.List, and TypedSplits adds its own union decoder for the
// four shapes that endpoint answers with. An unknown field never fails a response.
// Each model retains its raw payload for diagnostics through Payload().
//
// # Sensitivity
//
// Every model here can hold health data, location or identity. None of them may be
// logged: each implements slog.LogValuer to report shape instead of content, and the
// retained payload and the display name are sealed types from the client package with
// their own leak tests. They stay JSON-marshalable, because the tool layer returns
// them to an authorized caller.
//
// # Documented gaps
//
//   - Writes and destructive endpoints. Only reads are implemented in this slice.
//     set_activity_name, the workout and gear endpoints, the nutrition and
//     body-composition writes and every delete stay for the write slice.
//   - Downloads. FIT, GPX, TCX and CSV activity files, and their own size bounds,
//     belong to the download slice.
//   - The remaining 0.3.10 read endpoints — body battery, HRV, stress, training
//     readiness, badges, challenges, gear, goals, workouts, courses, nutrition — which
//     the parity backlog tracks. The four payload styles they use are all covered by
//     the five clients here, so adding one is an endpoint plus a model, not new
//     machinery.
//   - Upstream's chunked sleep-stats range endpoint (get_sleep_daily, 28-day chunks).
//     DailySleepRange reads the per-day endpoint with bounded fan-out instead, which
//     keeps one bound per request rather than a server-defined chunk size.
package api
