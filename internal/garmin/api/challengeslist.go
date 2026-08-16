package api

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The five paginated challenge listings: user-created social challenges, the
// account's joined and available badge challenges, and in-progress virtual
// challenges. Item models and the tolerant envelope decoders these reads use
// live in challengeslistmodels.go.
//
// Source: python-garminconnect 0.3.10's get_adhoc_challenges,
// get_badge_challenges, get_available_badge_challenges,
// get_non_completed_badge_challenges and get_inprogress_virtual_challenges,
// each read with only start and limit as query parameters.
// python-garminconnect types every one of these dict[str, Any]
// (__init__.py:1814-1866), but that type hint is contradicted by
// Taxuspt/garmin_mcp's own curation, src/garmin_mcp/challenges.py at the pinned
// commit docs/upstream-pins.md names, which is now this package's second
// evidenced source and settles both the field spellings and the envelope
// shape:
//
//   - get_adhoc_challenges, get_badge_challenges, get_available_badge_challenges
//     and get_non_completed_badge_challenges are each iterated directly —
//     `for challenge in challenges` with no isinstance guard (challenges.py:379,
//     428, 461, 498) — and each item's fields are then read with .get(), which
//     only a dict supports. Code that ran this way against a real account and
//     stayed in the shipped tool can only have seen a bare JSON array: an
//     object wrapping the array under any key would make `for challenge in
//     challenges` yield the object's string keys, and `challenge.get(...)` on
//     a string raises. A bare array is therefore the evidenced shape, not a
//     guessed wrapper key, and each family below decodes one directly.
//   - get_inprogress_virtual_challenges is the one exception: challenges.py
//     itself does not trust it to be a bare array (challenges.py:570-574) and
//     defensively branches on isinstance(challenges, dict), reading a
//     "challenges" key with a fallback that treats the whole dict as a single
//     one-item list when that key is absent. virtualChallengeEnvelope ports
//     that exact fallback rather than inventing a different one.
//
// Each family also carries its own field vocabulary — adhoc challenges never
// use the badge* spellings the badge-challenge family uses — evidenced the
// same way, per struct in challengeslistmodels.go.

// BadgeChallengePage is one page of badge challenges plus its retained raw
// payload. It carries the same sensitivity as BadgeChallengeItem.
type BadgeChallengePage struct {
	Challenges []BadgeChallengeItem

	raw client.Payload
}

// Payload is the retained raw response.
func (p BadgeChallengePage) Payload() client.Payload { return p.raw }

// AdhocChallengePage is one page of adhoc challenges plus its retained raw
// payload. It carries the same sensitivity as AdhocChallengeItem.
type AdhocChallengePage struct {
	Challenges []AdhocChallengeItem

	raw client.Payload
}

// Payload is the retained raw response.
func (p AdhocChallengePage) Payload() client.Payload { return p.raw }

// VirtualChallengePage is one page of in-progress virtual challenges plus its
// retained raw payload. It carries the same sensitivity as
// VirtualChallengeItem.
type VirtualChallengePage struct {
	Challenges []VirtualChallengeItem

	raw client.Payload
}

// Payload is the retained raw response.
func (p VirtualChallengePage) Payload() client.Payload { return p.raw }

// AdhocChallenges reads one page of user-created social challenges. Source:
// get_adhoc_challenges, whose start is validated non-negative.
func (c *Challenges) AdhocChallenges(
	ctx context.Context, session client.Session, page client.Page,
) (AdhocChallengePage, error) {
	req := readRequest(client.OpGetAdhocChallenges, client.EndpointAdhocChallenges,
		client.PathAdhocChallenges, challengeQuery(page))
	if err := c.req.limits().ValidatePage(page); err != nil {
		return AdhocChallengePage{}, invalid(req, err)
	}

	var items challengeArray[AdhocChallengeItem]
	payload, err := c.req.read(ctx, session, req, &items)
	if err != nil {
		return AdhocChallengePage{}, err
	}
	return AdhocChallengePage{Challenges: items.items, raw: payload}, nil
}

// BadgeChallenges reads one page of the badge challenges the account has
// joined. Source: get_badge_challenges, over the "completed" collection
// PathBadgeChallenges names.
func (c *Challenges) BadgeChallenges(
	ctx context.Context, session client.Session, page client.Page,
) (BadgeChallengePage, error) {
	return c.listBadgeChallenges(ctx, session,
		client.OpGetBadgeChallenges, client.EndpointBadgeChallenges, client.PathBadgeChallenges, page)
}

// AvailableBadgeChallenges reads one page of official challenges open to join.
// Source: get_available_badge_challenges.
func (c *Challenges) AvailableBadgeChallenges(
	ctx context.Context, session client.Session, page client.Page,
) (BadgeChallengePage, error) {
	return c.listBadgeChallenges(ctx, session, client.OpGetAvailableBadgeChallenges,
		client.EndpointAvailableBadgeChallenges, client.PathAvailableBadgeChallenges, page)
}

// NonCompletedBadgeChallenges reads one page of badge challenges joined but not
// yet completed. Source: get_non_completed_badge_challenges.
func (c *Challenges) NonCompletedBadgeChallenges(
	ctx context.Context, session client.Session, page client.Page,
) (BadgeChallengePage, error) {
	return c.listBadgeChallenges(ctx, session, client.OpGetNonCompletedBadgeChallenges,
		client.EndpointNonCompletedBadgeChallenges, client.PathNonCompletedBadgeChallenges, page)
}

// InProgressVirtualChallenges reads one page of virtual/expedition challenges
// in progress.
//
// Source: get_inprogress_virtual_challenges, whose _validate_positive_integer
// rejects a zero start — garminconnect 0.3.2 rejects 0 for this endpoint alone,
// unlike every other challenge list here, which only requires a non-negative
// one.
func (c *Challenges) InProgressVirtualChallenges(
	ctx context.Context, session client.Session, page client.Page,
) (VirtualChallengePage, error) {
	req := readRequest(client.OpGetInProgressVirtualChallenges, client.EndpointInProgressVirtualChallenges,
		client.PathInProgressVirtualChallenges, challengeQuery(page))
	if page.Start() < 1 {
		return VirtualChallengePage{}, invalid(req, fmt.Errorf(
			"%w: in-progress virtual challenges require a start of at least 1", client.ErrValidation))
	}
	if err := c.req.limits().ValidatePage(page); err != nil {
		return VirtualChallengePage{}, invalid(req, err)
	}

	var envelope virtualChallengeEnvelope
	payload, err := c.req.read(ctx, session, req, &envelope)
	if err != nil {
		return VirtualChallengePage{}, err
	}
	return VirtualChallengePage{Challenges: envelope.items, raw: payload}, nil
}

// listBadgeChallenges performs one bounded, paginated badge-challenge read.
func (c *Challenges) listBadgeChallenges(
	ctx context.Context, session client.Session,
	op client.Op, endpoint client.Endpoint, path string, page client.Page,
) (BadgeChallengePage, error) {
	req := readRequest(op, endpoint, path, challengeQuery(page))

	if err := c.req.limits().ValidatePage(page); err != nil {
		return BadgeChallengePage{}, invalid(req, err)
	}

	var items challengeArray[BadgeChallengeItem]
	payload, err := c.req.read(ctx, session, req, &items)
	if err != nil {
		return BadgeChallengePage{}, err
	}
	return BadgeChallengePage{Challenges: items.items, raw: payload}, nil
}

// challengeQuery builds the start/limit query every challenge list here sends.
func challengeQuery(page client.Page) url.Values {
	query := url.Values{}
	query.Set(client.QueryStart, strconv.Itoa(page.Start()))
	query.Set(client.QueryLimit, strconv.Itoa(page.Limit()))
	return query
}
