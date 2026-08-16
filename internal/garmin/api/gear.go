package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Gear reads the gear linked to an activity and links or unlinks it.
//
// Source: get_activity_gear over garmin_connect_gear
// ("/gear-service/gear/filterGear"), and add_gear_to_activity and
// remove_gear_from_activity over
// "/gear-service/gear/{link,unlink}/{uuid}/activity/{id}".
type Gear struct {
	req requester
}

// NewGear returns a gear client over the request layer.
func NewGear(rc *client.Client) (*Gear, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Gear{req: req}, nil
}

// GearUUID is a validated Garmin gear identifier. Only a parsed value reaches a
// URL path, so a gear identifier can carry no path separator and no traversal
// segment. Source: _validate_uuid.
type GearUUID struct {
	value string
}

// uuidLayout is the canonical 8-4-4-4-12 hyphen layout.
var uuidLayout = [...]int{8, 4, 4, 4, 12}

// ParseGearUUID validates a canonical hyphenated UUID.
func ParseGearUUID(value string) (GearUUID, error) {
	trimmed := strings.TrimSpace(value)
	groups := strings.Split(trimmed, "-")
	if len(groups) != len(uuidLayout) {
		return GearUUID{}, fmt.Errorf("%w: gear uuid must be a canonical UUID",
			client.ErrValidation)
	}
	for index, group := range groups {
		if len(group) != uuidLayout[index] || !isHex(group) {
			return GearUUID{}, fmt.Errorf("%w: gear uuid must be a canonical UUID",
				client.ErrValidation)
		}
	}
	return GearUUID{value: trimmed}, nil
}

// isHex reports whether every rune is an ASCII hexadecimal digit.
func isHex(value string) bool {
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// IsZero reports whether the identifier is unset.
func (g GearUUID) IsZero() bool { return g.value == "" }

// String is the validated identifier, or "".
func (g GearUUID) String() string { return g.value }

// GearItem is one piece of gear.
//
// It is device material: a gear item names a person's equipment, so it is never
// logged. Volatile sub-objects keep their raw shape and an unknown field never
// fails the response.
type GearItem struct {
	UUID            *string         `json:"uuid"`
	GearPk          client.Number   `json:"gearPk"`
	DisplayName     *string         `json:"displayName"`
	CustomMakeModel *string         `json:"customMakeModel"`
	GearMakeName    *string         `json:"gearMakeName"`
	GearModelName   *string         `json:"gearModelName"`
	GearTypeName    *string         `json:"gearTypeName"`
	GearStatusName  *string         `json:"gearStatusName"`
	DateBegin       *string         `json:"dateBegin"`
	DateEnd         *string         `json:"dateEnd"`
	Status          json.RawMessage `json:"gearStatus"`
	// MaximumMeters is the gear's configured maximum-distance retirement
	// threshold. Source: g.get("maximumMeters") in gear_management.py's get_gear
	// tool (gear_management.py:102-104), which divides it by 1000 for display; the
	// raw meters value is kept here undivided so a caller chooses its own unit.
	MaximumMeters client.Number `json:"maximumMeters"`
}

// ForActivity reads the gear linked to one activity.
//
// An empty array is a normal state: most activities have no gear.
func (g *Gear) ForActivity(
	ctx context.Context, session client.Session, id client.ID,
) ([]GearItem, error) {
	query := url.Values{}
	query.Set(client.QueryActivityID, id.String())

	req := readRequest(client.OpGetActivityGear, client.EndpointGearFilter,
		client.PathGearFilter, query)
	if err := requireID(req, id); err != nil {
		return nil, err
	}

	var items client.List[GearItem]
	if _, err := g.req.read(ctx, session, req, &items); err != nil {
		return nil, err
	}
	return items.Items(), nil
}

// linkPath builds the link or unlink path from two validated identifiers.
func linkPath(action string, gear GearUUID, id client.ID) string {
	return client.PathGearPrefix + "/" + action + "/" + gear.String() +
		"/activity/" + id.String()
}

// Add links gear to an activity.
//
// It is EffectIdempotentWrite: linking gear that is already linked leaves the
// same end state, so the request layer may repeat it after a transport failure.
func (g *Gear) Add(
	ctx context.Context, session client.Session, gear GearUUID, id client.ID,
) (WriteResult, error) {
	return g.link(ctx, session, client.OpAddGearToActivity, client.EndpointGearLink,
		"link", gear, id)
}

// Remove unlinks gear from an activity. It is idempotent for the same reason
// Add is: the end state does not depend on how many times the call was made.
func (g *Gear) Remove(
	ctx context.Context, session client.Session, gear GearUUID, id client.ID,
) (WriteResult, error) {
	return g.link(ctx, session, client.OpRemoveGearFromActivity, client.EndpointGearUnlink,
		"unlink", gear, id)
}

// link performs a gear link or unlink write.
func (g *Gear) link(
	ctx context.Context, session client.Session, op client.Op, endpoint client.Endpoint,
	action string, gear GearUUID, id client.ID,
) (WriteResult, error) {
	req := writeRequest(op, endpoint, http.MethodPut,
		linkPath(action, gear, id), client.EffectIdempotentWrite)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	if gear.IsZero() {
		return WriteResult{}, invalid(req, fmt.Errorf("%w: a gear uuid is required",
			client.ErrValidation))
	}

	payload, err := g.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}

// List reads every gear item registered to the account behind userProfileID.
//
// The account's profile id is looked up through Devices.LastUsed rather than
// accepted as a tool argument, matching AGENTS.md: no gear tool may take a
// user_id-shaped selector. Source: get_gear
// (params={"userProfilePk": userProfileNumber}), reusing PathGearFilter — the
// same list PathGearFilter already serves for ForActivity's activityId filter.
//
// This read carries no domain-level item cap: it is a single, unpaginated
// response bounded the same way EarnedBadges is, by the request layer's
// Limits.MaxResponseBytes and Limits.MaxDecompressedBytes.
func (g *Gear) List(
	ctx context.Context, session client.Session, userProfileID client.Number,
) ([]GearItem, error) {
	id, ok := userProfileID.Int64Exact()
	if !ok || id <= 0 {
		req := readRequest(client.OpGetGear, client.EndpointGearFilter, client.PathGearFilter, nil)
		return nil, invalid(req, fmt.Errorf("%w: a positive user profile id is required",
			client.ErrValidation))
	}

	query := url.Values{}
	query.Set(client.QueryUserProfilePK, strconv.FormatInt(id, 10))
	req := readRequest(client.OpGetGear, client.EndpointGearFilter, client.PathGearFilter, query)

	var items client.List[GearItem]
	if _, err := g.req.read(ctx, session, req, &items); err != nil {
		return nil, err
	}
	return items.Items(), nil
}

// GearDefault is one gear-to-activity-type default association.
//
// Source: gear_management.py's get_gear tool (gear_management.py:67-70):
// d.get("uuid") and d.get("activityTypePk"). ActivityTypePk is kept as the raw
// numeric key: the tool's ACTIVITY_TYPE_MAPPING that translates it to a label
// is explicitly documented there as "extrapolated from data and might not be
// complete or 100% accurate" (gear_management.py:11-12), so this package does
// not port a guessed mapping and leaves the translation to a caller that wants
// one.
type GearDefault struct {
	UUID           *string       `json:"uuid"`
	ActivityTypePk client.Number `json:"activityTypePk"`
}

// Defaults reads the account's gear-to-activity-type default associations.
//
// Source: get_gear_defaults
// (f"{garmin_connect_gear_baseurl}/user/{userProfileNumber}/activityTypes").
func (g *Gear) Defaults(
	ctx context.Context, session client.Session, userProfileID client.Number,
) ([]GearDefault, error) {
	id, ok := userProfileID.Int64Exact()
	if !ok || id <= 0 {
		req := readRequest(client.OpGetGearDefaults, client.EndpointGearDefaults,
			client.PathGearUserDefaultsPrefix, nil)
		return nil, invalid(req, fmt.Errorf("%w: a positive user profile id is required",
			client.ErrValidation))
	}

	path := client.PathGearUserDefaultsPrefix + "/" + strconv.FormatInt(id, 10) + "/" +
		client.ActivityTypesSegment
	req := readRequest(client.OpGetGearDefaults, client.EndpointGearDefaults, path, nil)

	var defaults client.List[GearDefault]
	if _, err := g.req.read(ctx, session, req, &defaults); err != nil {
		return nil, err
	}
	return defaults.Items(), nil
}

// GearStats is one gear item's usage statistics.
//
// Source: gear_management.py's get_gear tool (gear_management.py:113-120):
// stats.get("totalActivities") and stats.get("totalDistance").
type GearStats struct {
	TotalActivities client.Number `json:"totalActivities"`
	TotalDistance   client.Number `json:"totalDistance"`
}

// Stats reads one gear item's usage statistics.
//
// A 404 decodes as the zero GearStats rather than an error, matching
// get_gear_stats's own try/except: retired or removed gear commonly has no
// stats document, and upstream logs that case and returns {} rather than
// failing the caller's whole gear listing over it.
func (g *Gear) Stats(ctx context.Context, session client.Session, gear GearUUID) (GearStats, error) {
	req := readRequest(client.OpGetGearStats, client.EndpointGearStats,
		client.PathGearStatsPrefix+"/"+gear.String(), nil)
	if gear.IsZero() {
		return GearStats{}, invalid(req, fmt.Errorf("%w: a gear uuid is required", client.ErrValidation))
	}

	var stats GearStats
	if _, err := g.req.read(ctx, session, req, &stats); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return GearStats{}, nil
		}
		return GearStats{}, err
	}
	return stats, nil
}
