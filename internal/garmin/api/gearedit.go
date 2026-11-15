package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Gear lifecycle writes over the v2 gear service.
//
// Source: python-garminconnect db0ddaa441 garminconnect/api/gear.py: create_gear,
// get_gear_details, update_gear, set_gear_status and delete_gear.

// GearStatus is a gear lifecycle state. Source: set_gear_status's
// {"ACTIVE", "RETIRED"}.
type GearStatus string

// The two gear states Garmin accepts.
const (
	GearStatusActive  GearStatus = "ACTIVE"
	GearStatusRetired GearStatus = "RETIRED"
)

// Bounds on the caller-supplied gear fields.
const (
	// MaxGearTextLen bounds a gear name, brand or model.
	MaxGearTextLen = 256
	// MaxGearActivityTypes bounds the default activity-type list.
	MaxGearActivityTypes = 64
	// maxGearUsage bounds a retirement threshold in meters or seconds: 1e9 m is
	// a million kilometers.
	maxGearUsage = 1_000_000_000
)

// DefaultGearUsageType is create_gear's usage_type default.
const DefaultGearUsageType = "DISTANCE"

// GearFields are the editable fields of a v2 gear object. A nil field is absent:
// create sends Garmin's empty value for it, update keeps the current value.
//
// Source: _GEAR_EDITABLE_FIELDS. A zero threshold means "no threshold", an empty
// note clears the note (sent as null), and an empty activity-type list clears every default
// association, matching update_gear.
type GearFields struct {
	GearType           *string
	Brand              *string
	Model              *string
	Name               *string
	FirstUseDate       *client.Date
	UsageType          *string
	MaxDistanceMeters  *int64
	MaxDurationSeconds *int64
	Notes              *string
	ActivityTypeKeys   *[]string
}

// gearActivityTypeDTO is one associatedActivityTypes entry. Source:
// _gear_activity_types.
type gearActivityTypeDTO struct {
	ActivityTypeKey string `json:"activityTypeKey"`
	DefaultGear     bool   `json:"defaultGear"`
	PreferredGear   bool   `json:"preferredGear"`
}

// GearDetail is one v2 gear object, as get_gear_details returns it.
//
// It is device material and never logged. Only the editable fields and the
// identifier are named, because those are the spellings update_gear evidences.
type GearDetail struct {
	UUID                    *string         `json:"uuid"`
	GearType                *string         `json:"gearType"`
	Brand                   *string         `json:"brand"`
	Model                   *string         `json:"model"`
	Name                    *string         `json:"name"`
	FirstUseDate            *string         `json:"firstUseDate"`
	UsageType               *string         `json:"usageType"`
	MaxUsageDistanceMeters  client.Number   `json:"maxUsageDistanceMeters"`
	MaxUsageDurationSeconds client.Number   `json:"maxUsageDurationSeconds"`
	Notes                   *string         `json:"notes"`
	AssociatedActivityTypes json.RawMessage `json:"associatedActivityTypes"`
}

// LogValue reports which fields arrived, never a name, brand or note.
func (g GearDetail) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "gearDetail"),
		slog.String("uuid", presence(g.UUID != nil)),
		slog.String("name", presence(g.Name != nil)),
		slog.String("gearType", presence(g.GearType != nil)),
	)
}

// Hyphenated is the canonical 8-4-4-4-12 form, which the v2 paths take. The gear
// list returns the hyphen-free form.
func (g GearUUID) Hyphenated() string {
	if len(g.value) != compactUUIDLen {
		return g.value
	}
	parts := make([]string, 0, len(uuidLayout))
	rest := g.value
	for _, size := range uuidLayout {
		parts = append(parts, rest[:size])
		rest = rest[size:]
	}
	return strings.Join(parts, "-")
}

// gearPath is the v2 path of one gear item.
func gearPath(gear GearUUID) string {
	return client.PathGearV2 + "/" + gear.Hyphenated()
}

// textField pairs an optional field with its wire key.
type textField struct {
	value *string
	key   string
}

// overlay validates the fields and renders each present one under its wire key.
func (f GearFields) overlay(req client.Request) (map[string]any, error) {
	out := map[string]any{}
	for _, k := range []textField{{f.GearType, "gearType"}, {f.UsageType, "usageType"}} {
		if k.value == nil {
			continue
		}
		key, err := parseUpperKey(*k.value, k.key)
		if err != nil {
			return nil, invalid(req, err)
		}
		out[k.key] = key
	}
	for _, t := range []textField{{f.Brand, "brand"}, {f.Model, "model"}, {f.Name, "name"}} {
		if t.value == nil {
			continue
		}
		text, err := requireBoundedText(req, *t.value, t.key, MaxGearTextLen)
		if err != nil {
			return nil, err
		}
		out[t.key] = text
	}
	if f.FirstUseDate != nil {
		out["firstUseDate"] = f.FirstUseDate.String()
	}
	if err := f.overlayRest(req, out); err != nil {
		return nil, err
	}
	return out, nil
}

// overlayRest renders the thresholds, the note and the activity types into out,
// which overlay owns.
func (f GearFields) overlayRest(req client.Request, out map[string]any) error {
	usages := []struct {
		value *int64
		key   string
	}{{f.MaxDistanceMeters, "maxUsageDistanceMeters"}, {f.MaxDurationSeconds, "maxUsageDurationSeconds"}}
	for _, u := range usages {
		if u.value == nil {
			continue
		}
		if *u.value < 0 || *u.value > maxGearUsage {
			return invalid(req, fmt.Errorf("%w: %s is out of range", client.ErrValidation, u.key))
		}
		out[u.key] = *u.value
	}
	if f.Notes != nil {
		if len(*f.Notes) > MaxTextLen || hasControlRune(*f.Notes) {
			return invalid(req, fmt.Errorf("%w: notes must be bounded text", client.ErrValidation))
		}
		out["notes"] = noteValue(*f.Notes)
	}
	if f.ActivityTypeKeys != nil {
		types, err := gearActivityTypes(*f.ActivityTypeKeys)
		if err != nil {
			return invalid(req, err)
		}
		out["associatedActivityTypes"] = types
	}
	return nil
}

// noteValue renders a note. Garmin refuses an empty note ("size must be between 1
// and 2000"), so no note is sent as null.
func noteValue(note string) any {
	if note == "" {
		return nil
	}
	return note
}

// parseUpperKey normalizes a gear or usage type key. Source: _validate_sport_key.
func parseUpperKey(value, field string) (string, error) {
	upper := strings.ToUpper(strings.TrimSpace(value))
	if upper == "" || len(upper) > maxTokenLen {
		return "", fmt.Errorf("%w: %s must be a short key", client.ErrValidation, field)
	}
	for _, r := range upper {
		if (r < 'A' || r > 'Z') && r != '_' {
			return "", fmt.Errorf("%w: %s must contain only letters and underscores",
				client.ErrValidation, field)
		}
	}
	return upper, nil
}

// gearActivityTypes builds associatedActivityTypes. Source: _gear_activity_types.
func gearActivityTypes(keys []string) ([]gearActivityTypeDTO, error) {
	if len(keys) > MaxGearActivityTypes {
		return nil, fmt.Errorf("%w: too many activity types", client.ErrValidation)
	}
	out := make([]gearActivityTypeDTO, 0, len(keys))
	for _, key := range keys {
		token, present, err := parseLowerToken(key, "activity type")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, fmt.Errorf("%w: activity type must not be empty", client.ErrValidation)
		}
		out = append(out, gearActivityTypeDTO{ActivityTypeKey: token, DefaultGear: true})
	}
	return out, nil
}

// Create creates a gear item and returns it. Brand, model, name, gear type and
// first use date are required.
//
// It is EffectUnsafeWrite: each POST creates a new item, so a lost response is
// never replayed. Source: create_gear, POST "/gear-service/gear/v2".
func (g *Gear) Create(ctx context.Context, session client.Session, fields GearFields) (GearDetail, error) {
	req := writeRequest(client.OpCreateGear, client.EndpointGearV2, http.MethodPost,
		client.PathGearV2, client.EffectUnsafeWrite)
	if fields.GearType == nil || fields.Brand == nil || fields.Model == nil ||
		fields.Name == nil || fields.FirstUseDate == nil {
		return GearDetail{}, invalid(req, fmt.Errorf(
			"%w: gear type, brand, model, name and first use date are required", client.ErrValidation))
	}
	if fields.UsageType == nil {
		usage := DefaultGearUsageType
		fields.UsageType = &usage
	}
	set, err := fields.overlay(req)
	if err != nil {
		return GearDetail{}, err
	}
	payload := map[string]any{
		"uuid": nil, "maxUsageDate": nil, "maxUsageDistanceMeters": 0,
		"maxUsageDurationSeconds": 0, "notes": nil,
		"associatedActivityTypes": []gearActivityTypeDTO{},
	}
	maps.Copy(payload, set)
	body, err := jsonBody(req, payload)
	if err != nil {
		return GearDetail{}, err
	}
	req.Body = body

	var created GearDetail
	if _, err := g.req.write(ctx, session, req, &created); err != nil {
		return GearDetail{}, err
	}
	return created, nil
}

// Details reads one v2 gear object. Source: get_gear_details,
// GET "/gear-service/gear/v2/{uuid}".
func (g *Gear) Details(ctx context.Context, session client.Session, gear GearUUID) (GearDetail, error) {
	var detail GearDetail
	if err := g.readDetails(ctx, session, gear, &detail); err != nil {
		return GearDetail{}, err
	}
	return detail, nil
}

// readDetails reads the v2 gear object into out.
func (g *Gear) readDetails(ctx context.Context, session client.Session, gear GearUUID, out any) error {
	req := readRequest(client.OpGetGearDetails, client.EndpointGearV2, gearPath(gear), nil)
	if gear.IsZero() {
		return invalid(req, fmt.Errorf("%w: a gear uuid is required", client.ErrValidation))
	}
	_, err := g.req.read(ctx, session, req, out)
	return err
}

// Update changes the present fields of one gear item and returns the item as Garmin
// then reports it.
//
// Garmin refuses a partial body, so the current object is read, the fields are
// overlaid and the whole object is sent back; a field this package does not name is
// kept verbatim. Source: update_gear, PUT "/gear-service/gear/v2/{uuid}".
func (g *Gear) Update(
	ctx context.Context, session client.Session, gear GearUUID, fields GearFields,
) (GearDetail, error) {
	req := writeRequest(client.OpUpdateGear, client.EndpointGearV2, http.MethodPut,
		gearPath(gear), client.EffectIdempotentWrite)
	set, err := fields.overlay(req)
	if err != nil {
		return GearDetail{}, err
	}
	var current map[string]json.RawMessage
	if err := g.readDetails(ctx, session, gear, &current); err != nil {
		return GearDetail{}, err
	}
	if len(current) == 0 {
		return GearDetail{}, invalid(req, fmt.Errorf("%w: gear not found", client.ErrNotFound))
	}
	payload := make(map[string]any, len(current)+len(set))
	for key, value := range current {
		payload[key] = value
	}
	maps.Copy(payload, set)
	body, err := jsonBody(req, payload)
	if err != nil {
		return GearDetail{}, err
	}
	req.Body = body
	if _, err := g.req.write(ctx, session, req, nil); err != nil {
		return GearDetail{}, err
	}
	return g.Details(ctx, session, gear)
}

// SetStatus retires or re-activates one gear item. Source: set_gear_status,
// PUT "/gear-service/gear/v2/{uuid}/status/{status}" with no body.
func (g *Gear) SetStatus(
	ctx context.Context, session client.Session, gear GearUUID, status GearStatus,
) (WriteResult, error) {
	req := writeRequest(client.OpSetGearStatus, client.EndpointGearV2Status, http.MethodPut,
		gearPath(gear)+"/"+client.GearStatusSegment+"/"+string(status), client.EffectIdempotentWrite)
	if gear.IsZero() {
		return WriteResult{}, invalid(req, fmt.Errorf("%w: a gear uuid is required", client.ErrValidation))
	}
	if status != GearStatusActive && status != GearStatusRetired {
		return WriteResult{}, invalid(req, fmt.Errorf("%w: status must be ACTIVE or RETIRED", client.ErrValidation))
	}
	payload, err := g.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}

// Delete removes one gear item. Source: delete_gear,
// DELETE "/gear-service/gear/v2/{uuid}".
func (g *Gear) Delete(ctx context.Context, session client.Session, gear GearUUID) (WriteResult, error) {
	req := writeRequest(client.OpDeleteGear, client.EndpointGearV2, http.MethodDelete,
		gearPath(gear), client.EffectDelete)
	if gear.IsZero() {
		return WriteResult{}, invalid(req, fmt.Errorf("%w: a gear uuid is required", client.ErrValidation))
	}
	payload, err := g.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}

// KilometersToMeters converts a retirement distance to the integer meters Garmin
// stores. Zero stays zero; a positive value below one meter is refused, because it
// would round to zero, which means no threshold. Source: _gear_distance_meters.
func KilometersToMeters(km float64) (int64, error) {
	return scaleUsage(km, 1000, "distance")
}

// MinutesToSeconds converts a retirement duration to integer seconds. Source:
// _gear_duration_seconds.
func MinutesToSeconds(minutes float64) (int64, error) {
	return scaleUsage(minutes, 60, "duration")
}

func scaleUsage(value, factor float64, field string) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value*factor > maxGearUsage {
		return 0, fmt.Errorf("%w: %s threshold is out of range", client.ErrValidation, field)
	}
	scaled := int64(math.Round(value * factor))
	if value > 0 && scaled < 1 {
		return 0, fmt.Errorf("%w: %s threshold rounds to zero", client.ErrValidation, field)
	}
	return scaled, nil
}
