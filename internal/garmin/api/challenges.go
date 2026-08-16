package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Challenges reads the account's earned badges, goals and race-time
// predictions, plus — in challengeslist.go — the five paginated challenge
// listings.
//
// Source: python-garminconnect 0.3.10's get_earned_badges, get_goals and
// get_race_predictions, and the badge-service, goal-service and metrics-service
// URLs their constructor assigns. Field spellings additionally cite
// Taxuspt/garmin_mcp's src/garmin_mcp/challenges.py at the commit
// docs/upstream-pins.md names, which curates these same three reads and is
// the only one of the two pinned sources that names an individual field.
//
// Every document here ties a badge, a goal or a predicted race time to the
// account, so it is health data, identity material, or both at once. Never log
// one; see challengessensitive.go. No model here decodes userProfilePK or any
// account identifier beyond what a caller must already hold, and no method
// takes one: the principal comes from the session.
type Challenges struct {
	req requester
}

// NewChallenges returns a challenges client over the request layer.
func NewChallenges(rc *client.Client) (*Challenges, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Challenges{req: req}, nil
}

// EarnedBadge is one badge the account has earned.
//
// Field names are cited from Taxuspt/garmin_mcp's src/garmin_mcp/challenges.py
// get_earned_badges tool (challenges.py:296-360), the only one of this
// project's two pinned sources that names an individual field on this read:
// python-garminconnect's own get_earned_badges forwards Garmin's response
// untouched, typed only as list[dict[str, Any]] (__init__.py:1769), and
// compat/tools.json records no field shape either. Every field stays optional
// regardless, because no fixture here is captured against a real account: an
// unrecognized spelling costs nothing but that one field, never the whole
// decode.
//
// DisplayName, FullName and UserProfileID are not read by challenges.py's own
// curation and stay unevidenced by it; they are kept because the badge-earned
// endpoint is documented elsewhere in this package (Profile) as carrying a
// principal's own identity alongside the badge, and dropping them would lose
// data challenges.py simply never needed for its own output.
//
// badgeId and badgeEarnedNumber are not read anywhere in challenges.py's
// curation of this endpoint (challenges.py:296-360) and no other pinned
// source names them either, so both are omitted rather than guessed: nothing
// is lost by the omission, and a field can be added here once a real
// spelling is evidenced.
type EarnedBadge struct {
	BadgeName       client.Text   `json:"badgeName"`
	BadgePoints     client.Number `json:"badgePoints"`
	BadgeEarnedDate client.Text   `json:"badgeEarnedDate"`

	// BadgeCategoryID, BadgeDifficultyID and BadgeUnitID drive
	// challenges.py's BADGE_CATEGORY_MAPPING, BADGE_DIFFICULTY_MAPPING and
	// BADGE_UNIT_MAPPING (challenges.py:14-38, read at challenges.py:307-309).
	// Those three mappings are the tool layer's presentation concern — they
	// translate a numeric id to a display label and are not ported here — but
	// the ids themselves must decode so a caller above this layer has
	// something to map.
	BadgeCategoryID   client.Number `json:"badgeCategoryId"`
	BadgeDifficultyID client.Number `json:"badgeDifficultyId"`
	BadgeUnitID       client.Number `json:"badgeUnitId"`

	// BadgeProgressValue and BadgeTargetValue are read together at
	// challenges.py:312-313 to decide whether a badge carries progress at all
	// (challenges.py:330: both must be non-nil).
	BadgeProgressValue client.Number `json:"badgeProgressValue"`
	BadgeTargetValue   client.Number `json:"badgeTargetValue"`

	// BadgeStartDate and BadgeEndDate are read at challenges.py:335-336 to
	// build the badge's challenge_period, when both are present.
	BadgeStartDate client.Text `json:"badgeStartDate"`
	BadgeEndDate   client.Text `json:"badgeEndDate"`

	// BadgeAssocType and BadgeAssocDataID are read at challenges.py:341-344:
	// an activity-linked badge carries BadgeAssocType == "activityId" and
	// BadgeAssocDataID holding the activity's id.
	BadgeAssocType   client.Text   `json:"badgeAssocType"`
	BadgeAssocDataID client.Number `json:"badgeAssocDataId"`

	// BadgeSeriesID is read at challenges.py:347-348 to report a badge series.
	BadgeSeriesID client.Number `json:"badgeSeriesId"`

	UserProfileID client.Number `json:"userProfileId"`
	DisplayName   *string       `json:"displayName"`
	FullName      *string       `json:"fullName"`
}

// EarnedBadges reads every badge the account has earned. Source:
// get_earned_badges, which takes no parameter and pages nothing itself.
//
// This read carries no domain-level item cap of its own. It is a single,
// unpaginated response, so it is bounded the same way every other
// single-response decode in this package is: by the request layer's
// Limits.MaxResponseBytes and Limits.MaxDecompressedBytes (see
// internal/garmin/client's doc.go and limits.go), which every read this
// package makes already passes through before a byte reaches
// client.List[EarnedBadge]. That convention already covers a real account: a
// live sample returned 486 badges in one response, comfortably inside the
// default 8 MiB wire / 32 MiB decompressed bound with room to spare. A
// package-specific cap on top would either sit far above that bound and
// protect nothing, or sit near it and risk truncating a real account's
// earned history for no gain over the byte cap already in force. The one
// place in this file that needs its own cap is the accumulating pagination
// walk in Goals, because that walk can retain results across many requests
// rather than one; see maxGoalWalkItems below.
func (c *Challenges) EarnedBadges(ctx context.Context, session client.Session) ([]EarnedBadge, error) {
	req := readRequest(client.OpGetEarnedBadges, client.EndpointEarnedBadges, client.PathEarnedBadges, nil)

	var badges client.List[EarnedBadge]
	if _, err := c.req.read(ctx, session, req, &badges); err != nil {
		return nil, err
	}
	return badges.Items(), nil
}

// GoalStatus is the validated lifecycle filter get_goals takes.
//
// It is a strict request model: only a validated status reaches the query
// string.
type GoalStatus string

// The three statuses get_goals accepts. Source: the valid_statuses set of
// get_goals, checked by exact string equality with no case folding.
const (
	GoalStatusActive GoalStatus = "active"
	GoalStatusFuture GoalStatus = "future"
	GoalStatusPast   GoalStatus = "past"
)

// ParseGoalStatus validates a goal status against the exact set get_goals
// accepts.
func ParseGoalStatus(value string) (GoalStatus, error) {
	switch GoalStatus(value) {
	case GoalStatusActive, GoalStatusFuture, GoalStatusPast:
		return GoalStatus(value), nil
	default:
		return "", fmt.Errorf("%w: goal status must be %q, %q or %q",
			client.ErrValidation, GoalStatusActive, GoalStatusFuture, GoalStatusPast)
	}
}

// Goal is one lifecycle-filtered goal, kept as Garmin's own raw JSON object.
//
// Source: get_goals, whose return type is list[dict[str, Any]] with no field
// curation in the pinned client. Taxuspt/garmin_mcp's get_goals tool
// (challenges.py:236-249) does not narrow this either: it calls
// json.dumps(goals, indent=2) on the raw result and reads no individual
// field, so — unlike EarnedBadge, RacePredictionSet and the
// challengeslist.go families elsewhere in this package — neither pinned
// source names a single field spelling for a goal. A typed struct here would
// therefore be nothing but guessed tags, so a Goal is instead kept verbatim
// as the element's own JSON, the same discipline BodyComposition.DateWeightList
// and WellnessStress's raw-document reads already apply to a shape neither
// pinned source establishes: nothing is lost, and a typed field can be added
// once a real spelling is evidenced.
type Goal = json.RawMessage

// GoalResult is the goals matching one status.
//
// Truncated reports that maxGoalWalkItems was reached before Garmin returned
// an empty page, so a caller can tell a bounded result from a complete one
// rather than mistaking the former for the account's whole goal history.
type GoalResult struct {
	Goals     []Goal
	Truncated bool
}

// maxGoalWalkItems bounds the total goals one Goals call will accumulate
// across pages.
//
// Limits.MaxPages already bounds the page *count*, but not the size of an
// individual page or the total the walk retains across all of them: a
// server that ignores the requested limit and keeps answering full, never-
// empty pages would otherwise let the walk's in-memory aggregate grow with
// every one of up to Limits.MaxPages requests before the pagination bound
// finally errors. maxGoalWalkItems catches that case earlier and reports it
// as a truncation rather than a pagination failure, because the walk did
// make useful progress — it just stopped short of a server that would not
// stop being generous. It is a hard ceiling regardless of how permissively
// an operator configures Limits.MaxPages and Limits.MaxPageSize, generous
// headroom over any real account's goal list.
const maxGoalWalkItems = 5000

// Goals reads every goal matching status, walking pages until Garmin returns an
// empty page.
//
// Source: get_goals's own loop, which pages at its default limit of 30,
// starting from 0, and stops on the first empty page — not a short one, unlike
// Activities.ListByDate, where upstream's get_activities_by_date stops on a
// short page instead. The two upstream loops differ and this keeps the
// difference rather than unifying it. The tool built on get_goals exposes no
// page-size argument, so the walk here uses the configured Limits.MaxPageSize
// rather than upstream's literal 30, the same substitution pageSizeOrDefault
// already makes for Activities.ListByDate, so a stricter operator bound is
// honored instead of silently rejected. Upstream bounds the walk at
// MAX_PAGINATED_REQUESTS (2000); this port bounds page count at the
// configured Limits.MaxPages and fails loudly with ErrPaginationExhausted
// when that bound is reached without an empty page, the same choice
// ListByDate already makes. It additionally bounds the accumulated total at
// maxGoalWalkItems, reported through GoalResult.Truncated rather than an
// error, because a server that ignores limit is not the endless-pagination
// failure ErrPaginationExhausted names — it is a server that kept answering,
// just past the point this walk is willing to keep retaining.
func (c *Challenges) Goals(
	ctx context.Context, session client.Session, status GoalStatus,
) (GoalResult, error) {
	req := readRequest(client.OpGetGoals, client.EndpointGoals, client.PathGoals, nil)
	if status == "" {
		return GoalResult{}, invalid(req, fmt.Errorf("%w: a goal status is required", client.ErrValidation))
	}

	limits := c.req.limits()
	page, err := client.NewPage(0, limits.MaxPageSize)
	if err != nil {
		return GoalResult{}, invalid(req, err)
	}
	if err := limits.ValidatePage(page); err != nil {
		return GoalResult{}, invalid(req, err)
	}
	return c.walkGoals(ctx, session, status, page, limits)
}

// walkGoals fetches successive goal pages until one is empty, the page bound
// is reached, or maxGoalWalkItems is reached.
func (c *Challenges) walkGoals(
	ctx context.Context, session client.Session, status GoalStatus, page client.Page, limits client.Limits,
) (GoalResult, error) {
	var all []Goal

	for range limits.MaxPages {
		req := readRequest(client.OpGetGoals, client.EndpointGoals, client.PathGoals, goalQuery(status, page))

		var goals client.List[Goal]
		if _, err := c.req.read(ctx, session, req, &goals); err != nil {
			return GoalResult{}, err
		}
		items := goals.Items()
		if len(items) == 0 {
			return GoalResult{Goals: all}, nil
		}

		room := maxGoalWalkItems - len(all)
		if room <= 0 {
			return GoalResult{Goals: all, Truncated: true}, nil
		}
		if len(items) > room {
			all = append(all, items[:room]...)
			return GoalResult{Goals: all, Truncated: true}, nil
		}
		all = append(all, items...)
		page = page.Next()
	}

	req := readRequest(client.OpGetGoals, client.EndpointGoals, client.PathGoals, nil)
	return GoalResult{}, unexpected(req, fmt.Errorf("%w after %d pages", client.ErrPaginationExhausted, limits.MaxPages))
}

// goalQuery builds the query parameters get_goals sends: status, start, limit
// and a sortOrder Garmin always fixes to ascending.
func goalQuery(status GoalStatus, page client.Page) url.Values {
	query := url.Values{}
	query.Set(client.QueryStatus, string(status))
	query.Set(client.QueryStart, strconv.Itoa(page.Start()))
	query.Set(client.QueryLimit, strconv.Itoa(page.Limit()))
	query.Set(client.QuerySortOrder, client.GoalSortAscending)
	return query
}

// RacePredictionSet is the predicted 5K, 10K, half-marathon and marathon times
// for one day.
//
// Source: the zero-parameter branch of get_race_predictions, which reads
// f"{garmin_connect_race_predictor_url}/latest/{display_name}" and takes no
// date parameters. Field names are confirmed field-for-field by
// Taxuspt/garmin_mcp's get_race_predictions tool (challenges.py:513-549):
// calendarDate, time5K, time10K, timeHalfMarathon and timeMarathon are each
// read by exactly that spelling, with no other field consulted.
type RacePredictionSet struct {
	CalendarDate     *string       `json:"calendarDate"`
	Time5K           client.Number `json:"time5K"`
	Time10K          client.Number `json:"time10K"`
	TimeHalfMarathon client.Number `json:"timeHalfMarathon"`
	TimeMarathon     client.Number `json:"timeMarathon"`

	raw client.Payload
}

// Payload is the retained raw response.
func (r RacePredictionSet) Payload() client.Payload { return r.raw }

// RacePredictions reads the account's latest race-time predictions.
//
// The display name is a path segment, so it is a validated client.DisplayName
// escaped into exactly one segment, the same discipline Profile.PersonalRecords
// follows. Source: get_race_predictions's zero-parameter branch, whose
// _require_display_name exists precisely to stop an unset or hostile name from
// reaching a URL path. The tool built on this read exposes no startdate,
// enddate or _type argument, so the dated and ranged branches of
// get_race_predictions have no caller and are not ported here.
func (c *Challenges) RacePredictions(
	ctx context.Context, session client.Session, name client.DisplayName,
) (RacePredictionSet, error) {
	req := readRequest(client.OpGetRacePredictions, client.EndpointRacePredictions,
		racePredictionsLatestPath(name), nil)
	if err := requireDisplayName(req, name); err != nil {
		return RacePredictionSet{}, err
	}

	var predictions RacePredictionSet
	payload, err := c.req.read(ctx, session, req, &predictions)
	if err != nil {
		return RacePredictionSet{}, err
	}
	predictions.raw = payload
	return predictions, nil
}

// racePredictionsLatestPath appends the escaped display name to the "latest"
// branch of the race-predictions path.
func racePredictionsLatestPath(name client.DisplayName) string {
	return displayNamePath(client.PathRacePredictionsPrefix+"/latest", name)
}
