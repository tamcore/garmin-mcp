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
// Source: python-garminconnect 0.3.10, plus the two unmerged upstream proposals
// named below. The reads are first; the writes follow with their declared
// effects.
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
//	ActivityDetails.Summary      /activity-service/activity/{id}                    one activity
//	ActivityDetails.Splits       /activity-service/activity/{id}/splits             union-shaped
//	ActivityDetails.SplitSummaries  .../split_summaries                             array or object
//	ActivityDetails.Weather      /activity-service/activity/{id}/weather            location
//	ActivityDetails.HRInZones    /activity-service/activity/{id}/hrTimeInZones      health
//	ActivityDetails.PowerInZones /activity-service/activity/{id}/powerTimeInZones   health
//	ActivityDetails.Types        /activity-service/activity/activityTypes           catalog
//	ActivityDetails.EventTypes   /activity-service/activity/eventTypes              catalog
//	Profile.ProfileSettings      /userprofile-service/userprofile/settings          identity
//	Profile.UnitSystem           derived from Settings                              preference
//	Profile.FullName             derived from Social                                identity
//	Profile.PersonalRecords      /personalrecord-service/personalrecord/prs/{name}  health
//	Gear.ForActivity             /gear-service/gear/filterGear?activityId           device
//	Workouts.List                /workout-service/workouts                          paginated
//	Workouts.Get                 /workout-service/workout/{id}                      one workout
//	ActivityFiles.Download       /download-service/...                              streamed
//	Workouts.Download            /workout-service/workout/FIT/{id}                  streamed
//
// The writes, each with the effect the retry predicate reads:
//
//	ActivityWrites.SetName            PUT  /activity-service/activity/{id}   idempotent
//	ActivityWrites.SetType            PUT  same                              idempotent
//	ActivityWrites.SetEventType       PUT  same                              idempotent
//	ActivityWrites.SetDescription     PUT  same                              idempotent
//	ActivityWrites.SetFeel            PUT  same, summaryDTO                  idempotent
//	ActivityWrites.SetPerceivedEffort PUT  same, summaryDTO                  idempotent
//	ActivityWrites.Delete             DEL  same                              delete
//	ActivityWrites.CreateManual       POST /activity-service/activity        unsafe
//	Gear.Add, Gear.Remove             PUT  /gear-service/gear/{link,unlink}  idempotent
//	Workouts.Upload                   POST /workout-service/workout          unsafe
//	Workouts.Update                   PUT  /workout-service/workout/{id}     idempotent
//	Workouts.Delete                   DEL  same                              delete
//	Workouts.Schedule                 POST /workout-service/schedule/{id}    unsafe
//	Workouts.Unschedule               DEL  same, scheduled id                delete
//	StrengthWrites.ReplaceSets        PUT  .../exerciseSets, then verified   idempotent
//	StrengthWrites.Create             POST create, sets, then verified       unsafe
//
// # Writes
//
// A write takes a strict typed request model validated at the boundary, and a
// read keeps the tolerant decoding: the two directions are not symmetric, because
// a caller's mistake must be refused before it reaches Garmin while Garmin's
// schema drift must not fail an otherwise useful response.
//
// Two writes verify what they saved, which is the behavior the upstream
// proposals describe. ReplaceSets re-reads the set list and compares it set by
// set, and Create re-reads both the set list and the activity's own identifier. A
// mismatch is an error: reporting success for a session Garmin did not store the
// way it was written is the failure the check exists to prevent.
//
// ExerciseTypes is a compiled-in catalog rather than a fetched one, because
// Garmin publishes it on the web tier this package does not address. It is a
// documented subset of the FIT enum: the category set is closed and validated,
// the name set is not mirrored and an unlisted name is accepted after a lexical
// check.
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
// # Not ported from upstream
//
//   - The download tool's output_dir argument, the persisted download directory
//     and the "{activity_id}.{ext}" file it writes. A tool argument must not
//     choose a filesystem location, so a download streams into a sink the caller
//     supplies and this package opens no file.
//   - Upstream's whole-body buffering of a download. The bytes are streamed under
//     both the wire and the decompressed bound instead.
//   - The _is_already_scheduled pre-check upstream's MCP layer runs before
//     scheduling a workout. It fails open, so it is duplicate avoidance and not
//     idempotency; Schedule is therefore an unsafe write that is never retried,
//     and the pre-check belongs to the tool layer that can report what it found.
//   - Upstream's unbounded reads. Every list here is paginated or bounded, and a
//     caller-supplied body has a size bound of its own.
//
// # Documented gaps
//
//   - The nutrition, body-composition and hydration writes, the course endpoints
//     and the GraphQL-backed calendar reads (get_scheduled_workouts,
//     get_training_plan_workouts, schedule_week), which need a GraphQL request
//     shape this package does not build.
//   - Activity and course file upload, which needs multipart encoding.
//   - The remaining 0.3.10 read endpoints — body battery, HRV, stress, training
//     readiness, badges, challenges, gear, goals, workouts, courses, nutrition — which
//     the parity backlog tracks. The four payload styles they use are all covered by
//     the five clients here, so adding one is an endpoint plus a model, not new
//     machinery.
//   - Upstream's chunked sleep-stats range endpoint (get_sleep_daily, 28-day chunks).
//     DailySleepRange reads the per-day endpoint with bounded fan-out instead, which
//     keeps one bound per request rather than a server-defined chunk size.
package api
