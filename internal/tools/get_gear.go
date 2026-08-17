package tools

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetGear is the upstream compatibility name of the account-wide gear read.
const ToolGetGear = "get_gear"

// defaultMaxGearItems bounds the gear and gear-defaults lists one call returns. A
// real account's gear collection is a handful of items — shoes, bikes, a
// heart-rate strap — so a list past this bound is drift rather than a real
// inventory.
const defaultMaxGearItems = 128

// GearStatsSummary is one gear item's usage statistics.
//
// Source: gear_management.py's get_gear tool (gear_management.py:113-120):
// stats.get("totalActivities") and stats.get("totalDistance") (meters, converted to
// kilometers and rounded to one decimal here, matching round(total_distance / 1000, 1)).
type GearStatsSummary struct {
	TotalActivities *int64   `json:"total_activities,omitempty" jsonschema:"how many activities used this gear"`
	TotalDistanceKM *float64 `json:"total_distance_km,omitempty" jsonschema:"total distance in kilometers, one decimal"`
}

// GearEntry is one piece of registered gear.
//
// Source: the curation in gear_management.py's get_gear tool (gear_management.py:82-124):
// g.get("uuid"), g.get("displayName"), g.get("customMakeModel"), g.get("gearTypeName"),
// g.get("gearStatusName") lowercased, the calendar-only date via
// _parse_iso_date(g.get("dateBegin"/"dateEnd")) (gear_management.py:25-29), and
// g.get("maximumMeters") converted to kilometers when it is set and positive.
//
// IsDefaultForActivityTypes deliberately differs from upstream's is_default_for,
// which labels each association with a name from ACTIVITY_TYPE_MAPPING
// (gear_management.py:13-22) — a table its own comment calls "extrapolated from data
// and might not be complete or 100% accurate". internal/garmin/api's GearDefault
// keeps the numeric activityTypePk raw for the same reason (see its doc comment),
// and this tool follows that choice rather than porting a guessed label table: the
// numeric key is exact, and a caller that wants a label can map it itself.
type GearEntry struct {
	UUID          *string  `json:"uuid,omitempty" jsonschema:"the gear identifier the write tools take"`
	Name          *string  `json:"name,omitempty" jsonschema:"the name the account gave the gear"`
	FullName      *string  `json:"full_name,omitempty" jsonschema:"the make and model typed in"`
	Type          *string  `json:"type,omitempty" jsonschema:"the gear type, for example Shoes"`
	Status        string   `json:"status" jsonschema:"the gear status, lowercased, for example active"`
	DateBegin     *string  `json:"date_begin,omitempty" jsonschema:"when the gear entered service, date only"`
	DateEnd       *string  `json:"date_end,omitempty" jsonschema:"when the gear left service, date only"`
	MaxDistanceKM *float64 `json:"max_distance_km,omitempty" jsonschema:"the retirement distance in km, when set"`
	// IsDefaultForActivityTypes are the numeric activity-type keys this gear is
	// the account's default for. See the type's own doc comment for why these
	// are raw keys and not upstream's guessed labels.
	IsDefaultForActivityTypes []int64           `json:"default_activity_types,omitempty" jsonschema:"activity-type keys"`
	Stats                     *GearStatsSummary `json:"stats,omitempty" jsonschema:"usage stats, if requested"`
}

// GearList is the account's bounded gear inventory.
//
// Source: gear_management.py's get_gear tool (gear_management.py:143-152):
// gear_count, active_count, retired_count, defaults and gear. Defaults is keyed by
// the numeric activity-type key rather than upstream's guessed label, for the same
// reason GearEntry's own doc comment gives.
//
// It is device material: a gear name and make/model describe a person's equipment,
// so it is returned to the authorized caller and never logged.
type GearList struct {
	GearCount    int               `json:"gear_count" jsonschema:"how many gear items this result carries"`
	ActiveCount  int               `json:"active_count" jsonschema:"how many gear items are active"`
	RetiredCount int               `json:"retired_count" jsonschema:"how many gear items are retired"`
	Defaults     map[string]string `json:"defaults" jsonschema:"activity-type key to default gear name"`
	Gear         []GearEntry       `json:"gear" jsonschema:"the account's gear, active items first"`

	// Truncated reports that the gear list or the gear-defaults list was cut at
	// defaultMaxGearItems.
	Truncated bool `json:"truncated" jsonschema:"whether the gear or defaults list was cut at this server's bound"`
}

// LogValue reports counts only, never a gear name.
func (l GearList) LogValue() slog.Value {
	return shape("gearList",
		slog.Int("gear", l.GearCount),
		slog.Int("active", l.ActiveCount),
		slog.Int("retired", l.RetiredCount),
		slog.Bool("truncated", l.Truncated),
	)
}

// getGearInput is the argument set: whether to include usage statistics.
type getGearInput struct {
	IncludeStats *bool `json:"include_stats,omitempty" jsonschema:"include usage stats for each item, default true"`
}

// argIncludeStats is get_gear's optional usage-statistics argument name.
const argIncludeStats = "include_stats"

// includeStatsProperty declares the optional include_stats argument.
func includeStatsProperty() Property {
	return Property{
		Name:  argIncludeStats,
		Types: []string{typeBoolean},
		Description: "include usage statistics for each gear item; set false for a " +
			"faster read of a large gear collection",
		Default: true,
	}
}

func getGearContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetGear,
			Title: "Get gear",
			Description: "read every piece of gear registered to the account: name, " +
				"type, service dates, the configured retirement distance, the " +
				"activity types it defaults to, and — unless include_stats is false " +
				"— its usage statistics. The account's own profile identifier is " +
				"looked up automatically; no argument names it",
			Tier:        policy.TierReadOnly,
			Category:    categoryDevice,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(includeStatsProperty()),
	}
}

// registerGetGear registers the tool.
func registerGetGear(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getGearInput) (
		*mcp.CallToolResult, GearList, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, GearList{}, err
		}
		last, err := svc.devices.LastUsed(ctx, session)
		if err != nil {
			return nil, GearList{}, fail(err)
		}

		items, err := svc.gear.List(ctx, session, last.UserProfileNumber)
		if err != nil {
			return nil, GearList{}, fail(err)
		}
		defaults, err := svc.gear.Defaults(ctx, session, last.UserProfileNumber)
		if err != nil {
			return nil, GearList{}, fail(err)
		}

		includeStats := in.IncludeStats == nil || *in.IncludeStats
		result, err := svc.newGearList(ctx, session, items, defaults, includeStats)
		if err != nil {
			return nil, GearList{}, err
		}
		return nil, result, nil
	}
	return mcpserver.AddTool(registry, getGearContract().Registration(), handler)
}

// newGearList bounds the gear and defaults lists, maps each gear item — fetching its
// usage statistics when includeStats is set — and builds the defaults summary.
//
// Unlike upstream's get_gear_stats call (gear_management.py:111-122), which wraps
// every per-item stats fetch in a bare except and silently continues, a stats
// fetch failure here fails the whole call. AGENTS.md requires errors to be handled
// explicitly rather than swallowed, and api.Gear.Stats already converts the one
// expected "no stats yet" case — a 404 for retired or unused gear — into a zero
// value with no error, so any error this call still sees is a real one a caller
// should see rather than one this tool hides. A caller that wants the gear list
// without paying for statistics can pass include_stats=false.
func (s *service) newGearList(
	ctx context.Context, session client.Session,
	items []api.GearItem, defaults []api.GearDefault, includeStats bool,
) (GearList, error) {
	truncated := false
	if len(items) > defaultMaxGearItems {
		items = items[:defaultMaxGearItems]
		truncated = true
	}
	if len(defaults) > defaultMaxGearItems {
		defaults = defaults[:defaultMaxGearItems]
		truncated = true
	}

	defaultsByUUID := indexGearDefaults(defaults)
	entries := make([]GearEntry, 0, len(items))
	nameByUUID := make(map[string]string, len(items))
	active, retired := 0, 0
	for _, item := range items {
		entry, err := s.newGearEntry(ctx, session, item, defaultsByUUID, includeStats)
		if err != nil {
			return GearList{}, err
		}
		switch entry.Status {
		case valueActive:
			active++
		case gearStatusRetired:
			retired++
		}
		if item.UUID != nil {
			nameByUUID[*item.UUID] = stringOrEmpty(entry.Name)
		}
		entries = append(entries, entry)
	}
	sortGearEntries(entries)

	return GearList{
		GearCount:    len(entries),
		ActiveCount:  active,
		RetiredCount: retired,
		Defaults:     gearDefaultsSummary(defaults, nameByUUID),
		Gear:         entries,
		Truncated:    truncated,
	}, nil
}

// gearStatusRetired is the lowercased retired-gear status Garmin sends.
const gearStatusRetired = "retired"

// newGearEntry maps one gear item, attaching its default activity types and, when
// includeStats is set, its usage statistics.
func (s *service) newGearEntry(
	ctx context.Context, session client.Session, item api.GearItem,
	defaultsByUUID map[string][]int64, includeStats bool,
) (GearEntry, error) {
	entry := GearEntry{
		UUID:      item.UUID,
		Name:      item.DisplayName,
		FullName:  item.CustomMakeModel,
		Type:      item.GearTypeName,
		Status:    strings.ToLower(stringOrEmpty(item.GearStatusName)),
		DateBegin: parseISODate(item.DateBegin),
		DateEnd:   parseISODate(item.DateEnd),
	}
	if meters, ok := item.MaximumMeters.Float64(); ok && meters > 0 {
		km := roundToOneDecimal(meters / 1000)
		entry.MaxDistanceKM = &km
	}
	if item.UUID != nil {
		if activityTypes, ok := defaultsByUUID[*item.UUID]; ok {
			entry.IsDefaultForActivityTypes = activityTypes
		}
	}
	if !includeStats || item.UUID == nil {
		return entry, nil
	}

	gearUUID, err := api.ParseGearUUID(*item.UUID)
	if err != nil {
		return GearEntry{}, fail(err)
	}
	stats, err := s.gear.Stats(ctx, session, gearUUID)
	if err != nil {
		return GearEntry{}, fail(err)
	}
	entry.Stats = newGearStatsSummary(stats)
	return entry, nil
}

// newGearStatsSummary maps one gear item's statistics, matching upstream's own
// truthiness check (`if stats:`, gear_management.py:114) by reporting nothing when
// neither field was present.
func newGearStatsSummary(stats api.GearStats) *GearStatsSummary {
	activities, hasActivities := stats.TotalActivities.Int64Exact()
	distance, hasDistance := stats.TotalDistance.Float64()
	if !hasActivities && !hasDistance {
		return nil
	}
	summary := &GearStatsSummary{}
	if hasActivities {
		summary.TotalActivities = &activities
	}
	if hasDistance {
		km := roundToOneDecimal(distance / 1000)
		summary.TotalDistanceKM = &km
	}
	return summary
}

// indexGearDefaults groups every default association's activity-type key by gear
// UUID, matching defaults_by_uuid (gear_management.py:66-75).
func indexGearDefaults(defaults []api.GearDefault) map[string][]int64 {
	byUUID := make(map[string][]int64, len(defaults))
	for _, one := range defaults {
		if one.UUID == nil {
			continue
		}
		if pk, ok := one.ActivityTypePk.Int64Exact(); ok {
			byUUID[*one.UUID] = append(byUUID[*one.UUID], pk)
		}
	}
	return byUUID
}

// gearDefaultsSummary builds the activity-type-key to gear-name summary, matching
// defaults_summary (gear_management.py:134-141): the last default association naming
// a given activity-type key wins, because a later entry overwrites the same map key.
func gearDefaultsSummary(defaults []api.GearDefault, nameByUUID map[string]string) map[string]string {
	summary := make(map[string]string, len(defaults))
	for _, one := range defaults {
		if one.UUID == nil {
			continue
		}
		pk, ok := one.ActivityTypePk.Int64Exact()
		if !ok {
			continue
		}
		name, known := nameByUUID[*one.UUID]
		if !known {
			continue
		}
		summary[strconv.FormatInt(pk, 10)] = name
	}
	return summary
}

// parseISODate extracts the calendar-day prefix of an ISO instant, matching
// _parse_iso_date (gear_management.py:25-29).
func parseISODate(value *string) *string {
	if value == nil || *value == "" {
		return nil
	}
	if index := strings.Index(*value, "T"); index >= 0 {
		date := (*value)[:index]
		return &date
	}
	return value
}

// sortGearEntries places active gear first, preserving each side's relative order —
// matching the final `curated_gear.sort(key=lambda x: x["status"] != "active")`
// (gear_management.py:132), which is the sort that survives after the interim
// date-ordered sort before it is applied.
func sortGearEntries(entries []GearEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return gearSortKey(entries[i]) < gearSortKey(entries[j])
	})
}

func gearSortKey(entry GearEntry) int {
	if entry.Status == valueActive {
		return 0
	}
	return 1
}

// roundToOneDecimal matches Python's round(value, 1) for the positive, finite
// distances this package converts.
func roundToOneDecimal(value float64) float64 {
	return math.Round(value*10) / 10
}

// stringOrEmpty renders an optional string as "" rather than nil, for a field
// upstream always includes even when Garmin omits it.
func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
