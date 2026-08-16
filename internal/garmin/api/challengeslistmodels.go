package api

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The item models and tolerant envelope decoders for the five paginated
// challenge listings. See challengeslist.go for the reads themselves and the
// evidence for why each family gets its own vocabulary and its own envelope.

// BadgeChallengeItem is one badge challenge: joined, available or
// non-completed, all curated through the one shared function
// _curate_badge_challenge (challenges.py:176-207).
//
// Field spellings are cited from that function, which is the only pinned
// source that names an individual field for this family:
// python-garminconnect's own get_badge_challenges, get_available_badge_challenges
// and get_non_completed_badge_challenges each forward Garmin's response
// untouched and compat/tools.json records no field shape for the tools built on
// them.
//
// It is health data and identity material together — a progress figure and an
// earned date both ride along — so it must never be logged; see
// challengessensitive.go.
// id is deliberately not decoded here: it is not read anywhere in
// challenges.py's curation of this family (challenges.py:176-207), and no
// other pinned source names it either, so a guessed spelling would decode
// silently to nothing. Nothing is lost by the omission — the retained
// Payload keeps it — and a field can be added once a real spelling is
// evidenced.
type BadgeChallengeItem struct {
	UUID   client.Text   `json:"uuid"`
	Name   client.Text   `json:"badgeChallengeName"`
	Points client.Number `json:"badgePoints"`

	// CategoryID and StatusID are challengeCategoryId and
	// badgeChallengeStatusId (challenges.py:178-179), fed into
	// CHALLENGE_CATEGORY_MAPPING and CHALLENGE_STATUS_MAPPING
	// (challenges.py:41-57). That mapping is the tool layer's presentation
	// concern and is not ported here; only the ids decode.
	CategoryID client.Number `json:"challengeCategoryId"`
	StatusID   client.Number `json:"badgeChallengeStatusId"`

	// UnitID, ProgressValue and TargetValue (challenges.py:180-183) drive
	// _format_badge_value and _calculate_progress_percent
	// (challenges.py:146-173), read together only when TargetValue is set and
	// positive (challenges.py:197).
	UnitID        client.Number `json:"badgeUnitId"`
	ProgressValue client.Number `json:"badgeProgressValue"`
	TargetValue   client.Number `json:"badgeTargetValue"`

	StartDate client.Text `json:"startDate"`
	EndDate   client.Text `json:"endDate"`

	// UserJoined is userJoined (challenges.py:193), defaulted to false there
	// when absent; a nil pointer here means the same thing to a caller that
	// checks presence first.
	UserJoined *bool `json:"userJoined"`

	// EarnedDate is badgeEarnedDate (challenges.py:203), added to the curated
	// output only when non-empty.
	EarnedDate client.Text `json:"badgeEarnedDate"`

	// Joinable is read only for the available-challenges tool
	// (challenges.py:431: `challenge.get("joinable", True)`), never for the
	// joined or non-completed families. It decodes here regardless: an absent
	// field on the other two families costs nothing.
	Joinable *bool `json:"joinable"`
}

// Title reports the challenge's badge-challenge name.
func (b BadgeChallengeItem) Title() (string, bool) { return b.Name.Value() }

// AdhocChallengeItem is one user-created social challenge.
//
// Field spellings are cited from get_adhoc_challenges's curation
// (challenges.py:362-409), the only pinned source that names a field for this
// endpoint. Its vocabulary is its own: unlike BadgeChallengeItem, no field
// here carries a "badge" prefix.
//
// It is health data and identity material together, so it must never be
// logged; see challengessensitive.go.
type AdhocChallengeItem struct {
	Name        client.Text `json:"adHocChallengeName"`
	Description client.Text `json:"adHocChallengeDesc"`
	UUID        client.Text `json:"uuid"`

	// ActivityTypeID and StatusID are socialChallengeActivityTypeId
	// (challenges.py:381) and socialChallengeStatusId (challenges.py:380),
	// fed into ADHOC_ACTIVITY_TYPE_MAPPING and CHALLENGE_STATUS_MAPPING
	// (challenges.py:59-66, 51-57). The mapping is a tool-layer presentation
	// concern and is not ported here.
	ActivityTypeID client.Number `json:"socialChallengeActivityTypeId"`
	StatusID       client.Number `json:"socialChallengeStatusId"`

	StartDate client.Text `json:"startDate"`
	EndDate   client.Text `json:"endDate"`

	// UserRanking and PlayerCount are userRanking and playerCount
	// (challenges.py:393-394).
	UserRanking client.Number `json:"userRanking"`
	PlayerCount client.Number `json:"playerCount"`
}

// Title reports the adhoc challenge's name.
func (a AdhocChallengeItem) Title() (string, bool) { return a.Name.Value() }

// VirtualChallengeItem is one in-progress virtual/expedition challenge.
//
// get_inprogress_virtual_challenges's own curation (challenges.py:551-623) is
// the least certain of the five about this family's field vocabulary: it
// tries three spellings for the name and three each for progress and target,
// preferring the badge* spelling first (challenges.py:579-581, 588-599).
// VirtualChallengeItem decodes all three of each rather than picking one, and
// Title, Progress and Target report the same preference order the Python
// curation applies.
type VirtualChallengeItem struct {
	BadgeChallengeName client.Text `json:"badgeChallengeName"`
	Name               client.Text `json:"name"`
	ChallengeName      client.Text `json:"challengeName"`
	UUID               client.Text `json:"uuid"`
	StartDate          client.Text `json:"startDate"`
	EndDate            client.Text `json:"endDate"`

	BadgeProgressValue client.Number `json:"badgeProgressValue"`
	PlainProgress      client.Number `json:"progress"`
	ProgressValue      client.Number `json:"progressValue"`

	BadgeTargetValue client.Number `json:"badgeTargetValue"`
	PlainTarget      client.Number `json:"target"`
	TargetValue      client.Number `json:"targetValue"`

	// UnitID is badgeUnitId (challenges.py:601), read only once a progress and
	// a positive target are both present.
	UnitID client.Number `json:"badgeUnitId"`
}

// Title reports the virtual challenge's name, preferring BadgeChallengeName,
// then Name, then ChallengeName — the exact order the Python `or` chain
// challenges.py:579-581 applies:
//
//	challenge.get("badgeChallengeName") or challenge.get("name") or challenge.get("challengeName")
//
// Python's `or` treats an empty string as falsy the same way it treats None,
// so a present-but-empty BadgeChallengeName or Name falls through to the
// next candidate exactly as an absent one would; only the check on the
// final candidate is skipped, matching `or` returning its last operand
// unconditionally.
func (v VirtualChallengeItem) Title() (string, bool) {
	if value, ok := v.BadgeChallengeName.Value(); ok && value != "" {
		return value, true
	}
	if value, ok := v.Name.Value(); ok && value != "" {
		return value, true
	}
	return v.ChallengeName.Value()
}

// Progress reports the virtual challenge's progress value, preferring
// BadgeProgressValue, then PlainProgress, then ProgressValue — the order
// challenges.py's _first_non_none call at challenges.py:588-593 applies.
func (v VirtualChallengeItem) Progress() (float64, bool) {
	return firstSetNumber(v.BadgeProgressValue, v.PlainProgress, v.ProgressValue)
}

// Target reports the virtual challenge's target value, preferring
// BadgeTargetValue, then PlainTarget, then TargetValue — the order
// challenges.py's _first_non_none call at challenges.py:594-599 applies.
func (v VirtualChallengeItem) Target() (float64, bool) {
	return firstSetNumber(v.BadgeTargetValue, v.PlainTarget, v.TargetValue)
}

// firstSetNumber returns the first set value among candidates, in order.
func firstSetNumber(candidates ...client.Number) (float64, bool) {
	for _, candidate := range candidates {
		if value, ok := candidate.Float64(); ok {
			return value, true
		}
	}
	return 0, false
}

// challengeArray decodes a bare JSON array of T, tolerating null and an empty
// object as zero items, and rejecting anything else.
//
// Source: see the challengeslist.go package doc comment — challenges.py's own
// iteration over these four families' results, `for challenge in challenges:
// ... challenge.get(...)` with no isinstance guard, is the evidence both for
// what this decoder tolerates and for what it must not: iterating an empty
// dict yields zero results with no exception, which is why an empty object
// decodes the same as null or an empty array, but iterating a *non-empty*
// dict yields its string keys, and `challenge.get(...)` on a string raises —
// the shipped tool's own try/except turns that into a reported error rather
// than a silently empty result. A response this decoder cannot place as
// either shape is therefore a decode failure, not a tolerated empty page:
// treating it as empty would mask exactly the kind of upstream drift — a
// gateway starting to wrap these four bare arrays — that a caller must be
// able to see rather than mistake for "no challenges".
type challengeArray[T any] struct {
	items []T
}

// UnmarshalJSON implements the decode challengeArray documents.
func (a *challengeArray[T]) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == jsonNull {
		*a = challengeArray[T]{}
		return nil
	}
	if trimmed[0] == '[' {
		var items []T
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return err
		}
		*a = challengeArray[T]{items: items}
		return nil
	}
	if trimmed[0] == '{' {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &fields); err != nil {
			return err
		}
		if len(fields) == 0 {
			*a = challengeArray[T]{}
			return nil
		}
		return fmt.Errorf("garmin api: challenge list expected a bare JSON array, got a non-empty object")
	}
	return fmt.Errorf("garmin api: challenge list expected a bare JSON array")
}

// virtualChallengeEnvelope decodes get_inprogress_virtual_challenges's
// response.
//
// Source: challenges.py:570-574 — the only one of the five list endpoints
// whose own tool layer does not trust a bare array. It branches on
// isinstance(challenges, dict): a dict yields challenges.get("challenges",
// [challenges]), so a "challenges" key wins when present, and the whole dict
// becomes a single one-item list when it is absent. A non-dict, non-list value
// yields an empty list (challenges.py:574). This type ports that exact
// fallback chain rather than a different, more convenient one.
type virtualChallengeEnvelope struct {
	items []VirtualChallengeItem
}

// UnmarshalJSON implements the tolerant decode virtualChallengeEnvelope
// documents.
func (e *virtualChallengeEnvelope) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == jsonNull {
		*e = virtualChallengeEnvelope{}
		return nil
	}
	if trimmed[0] == '[' {
		var items []VirtualChallengeItem
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return err
		}
		*e = virtualChallengeEnvelope{items: items}
		return nil
	}
	if trimmed[0] != '{' {
		*e = virtualChallengeEnvelope{}
		return nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return err
	}
	if raw, ok := fields["challenges"]; ok {
		var items []VirtualChallengeItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		*e = virtualChallengeEnvelope{items: items}
		return nil
	}

	// No "challenges" key: python's challenges.get("challenges", [challenges])
	// falls back to the whole dict as a single item.
	var single VirtualChallengeItem
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return err
	}
	*e = virtualChallengeEnvelope{items: []VirtualChallengeItem{single}}
	return nil
}
