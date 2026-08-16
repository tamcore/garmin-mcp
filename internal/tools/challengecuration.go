package tools

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// dateKey renders an optional date pointer as a sort key: an absent date sorts as
// the empty string, matching every `x.get("...") or ""` sort key challenges.py uses
// (start_date at challenges.py:400-402, 435, 466-468; end_date at 503; earned_date
// at 353). It is shared by every date-ordered sort in this package, whichever date
// field a caller passes.
func dateKey(date *string) string {
	if date == nil {
		return ""
	}
	return *date
}

// boundChallengePage caps a decoded, already-sorted challenge page at limit, since
// Garmin does not reliably honor the limit it was asked for, and reports whether the
// cap cut anything.
func boundChallengePage[T any](items []T, limit int) ([]T, bool) {
	if limit > 0 && len(items) > limit {
		return items[:limit], true
	}
	return items, false
}

// The curation and pagination helpers every challenge tool in this package shares:
// the three badge-challenge listings in badgechallengelists.go, get_adhoc_challenges
// (get_adhoc_challenges.go) and get_inprogress_virtual_challenges
// (get_inprogress_virtual_challenges.go) all call into this file rather than
// duplicating the id-to-label mappings and the badge-value formatting
// challenges.py shares across those five tools.
//
// Source: Taxuspt/garmin_mcp's src/garmin_mcp/challenges.py at the commit
// docs/upstream-pins.md names.

// maxChallengePageSize is the page-size ceiling every challenge-list tool applies.
// Source: each tool's own `min(limit, 100)` (challenges.py:374, 423, 456, 491, 565).
// Unlike upstream, an out-of-range limit is refused rather than silently capped,
// matching this package's convention for get_activities and get_activities_by_date.
const maxChallengePageSize = 100

// defaultChallengeLimit is the manifest default limit every challenge-list tool in
// this package declares.
const defaultChallengeLimit = 20

// challengeCategoryLabels maps a challenge's category id onto its display label.
// Source: CHALLENGE_CATEGORY_MAPPING (challenges.py:41-49).
func challengeCategoryLabels() map[int64]string {
	return map[int64]string{
		1: challengeLabelRunning, 2: challengeLabelCycling, 3: "Fitness", 4: challengeLabelSteps,
		5: "Walking", 6: "Yoga/Mindfulness", 9: "Multi-Activity",
	}
}

// challengeStatusLabels maps a challenge's status id onto its display label.
// Source: CHALLENGE_STATUS_MAPPING (challenges.py:52-57), shared by the badge
// challenge and adhoc challenge families alike.
func challengeStatusLabels() map[int64]string {
	return map[int64]string{1: "Not Started", 2: "In Progress", 3: "Completed", 4: "Ended"}
}

// badgeUnitCount is the badge/challenge unit value for a plain count, which
// BADGE_UNIT_MAPPING assigns to two different unit ids.
const badgeUnitCount = "count"

// badgeUnitKinds maps a badge/challenge unit id onto the value it formats. Source:
// BADGE_UNIT_MAPPING (challenges.py:32-38); only the value_type half is ported, the
// name half is presentation the JSON result does not need.
func badgeUnitKinds() map[int64]string {
	return map[int64]string{
		1: "distance", 2: "elevation", 3: badgeUnitCount, 5: badgeUnitCount, 7: keyTime,
	}
}

// mappedLabel resolves id through labels, falling back to prefix_<id> the way every
// mapping in challenges.py does when it meets a code it does not recognize
// (challenges.py:188-189, 320-324, 390).
func mappedLabel(labels map[int64]string, id int64, prefix string) string {
	if label, ok := labels[id]; ok {
		return label
	}
	return fmt.Sprintf("%s_%d", prefix, id)
}

// formatBadgeValue formats a raw progress or target value under unitID. Source:
// _format_badge_value (challenges.py:146-165). An unrecognized or absent unit
// renders the plain number, matching `return str(value)`.
func formatBadgeValue(value float64, unitID client.Number) string {
	id, ok := unitID.Int64()
	kind := ""
	if ok {
		kind = badgeUnitKinds()[id]
	}
	switch kind {
	case keyTime:
		return formatClockDuration(value)
	case "distance":
		return formatBadgeDistance(value)
	case "elevation":
		return fmt.Sprintf("%.0f m", value)
	case badgeUnitCount:
		return formatThousands(int64(value))
	default:
		return formatPlainNumber(value)
	}
}

// formatClockDuration ports _format_time (challenges.py:98-109).
func formatClockDuration(seconds float64) string {
	total := int64(seconds)
	hours := total / 3600
	minutes := (total % 3600) / 60
	secs := total % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

// formatBadgeDistance ports _format_distance (challenges.py:112-118).
func formatBadgeDistance(meters float64) string {
	if meters >= 1000 {
		return fmt.Sprintf("%.2f km", meters/1000)
	}
	return fmt.Sprintf("%.0f m", meters)
}

// formatThousands ports the count branch of _format_badge_value, `f"{int(value):,}"`.
func formatThousands(value int64) string {
	text := strconv.FormatInt(value, 10)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")

	var grouped strings.Builder
	for i, digit := range []byte(text) {
		if i > 0 && (len(text)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteByte(digit)
	}
	if negative {
		return "-" + grouped.String()
	}
	return grouped.String()
}

// formatPlainNumber renders a value with no recognized unit, matching Python's
// str(value): an integral float renders without a fractional part.
func formatPlainNumber(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// calculateProgressPercent ports _calculate_progress_percent (challenges.py:168-173):
// the caller has already checked target is set and positive.
func calculateProgressPercent(progress, target float64) string {
	percent := progress / target * 100
	if percent > 100 {
		percent = 100
	}
	return fmt.Sprintf("%.1f%%", percent)
}

// isoDateOnly ports _parse_iso_date (challenges.py:129-134): the date part of an ISO
// datetime string, split on its first "T".
func isoDateOnly(value string) string {
	if before, _, ok := strings.Cut(value, "T"); ok {
		return before
	}
	return value
}

// optionalISODate applies isoDateOnly to an optional decoded string, matching
// _parse_iso_date's own None-in-None-out contract.
func optionalISODate(text client.Text) *string {
	value, ok := text.Value()
	if !ok || value == "" {
		return nil
	}
	out := isoDateOnly(value)
	return &out
}

// challengePageInput is the strict start/limit argument set every challenge-list
// tool in this package takes.
type challengePageInput struct {
	Start *int `json:"start,omitempty"`
	Limit *int `json:"limit,omitempty"`
}

// resolveChallengePage applies the manifest defaults and refuses an out-of-range
// argument before any Garmin call is made.
func resolveChallengePage(start, limit *int, defaultStart, minStart int) (client.Page, error) {
	startValue, limitValue := defaultStart, defaultChallengeLimit
	if start != nil {
		startValue = *start
	}
	if limit != nil {
		limitValue = *limit
	}

	switch {
	case startValue < minStart:
		return client.Page{}, invalidArgument("start must not be less than " + strconv.Itoa(minStart))
	case limitValue < 1:
		return client.Page{}, invalidArgument("limit must be at least 1")
	case limitValue > maxChallengePageSize:
		return client.Page{}, invalidArgument(
			"limit must not exceed " + strconv.Itoa(maxChallengePageSize))
	}

	page, err := client.NewPage(startValue, limitValue)
	if err != nil {
		return client.Page{}, fail(err)
	}
	return page, nil
}

// challengePageProperties declares the shared start/limit schema for a challenge
// list, with the tool's own manifest start default and minimum. minStart must equal
// the minStart the tool's own resolveChallengePage call enforces, so the advertised
// schema and the handler cannot drift apart: get_inprogress_virtual_challenges is
// the one tool in this package where minStart is 1 rather than 0. Every
// challenge-list tool in this package declares the same limit default,
// defaultChallengeLimit.
func challengePageProperties(defaultStart, minStart int) []Property {
	return []Property{
		{
			Name:        argStart,
			Types:       []string{typeInteger},
			Description: "starting index for pagination",
			Minimum:     bound(float64(minStart)),
			Maximum:     bound(float64(client.MaxPageStartCap)),
			Default:     defaultStart,
		},
		{
			Name:        argLimit,
			Types:       []string{typeInteger},
			Description: "maximum number of challenges to return",
			Minimum:     bound(1),
			Maximum:     bound(maxChallengePageSize),
			Default:     defaultChallengeLimit,
		},
	}
}
