package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The gear lifecycle tools. Source: python-garminconnect db0ddaa441
// garminconnect/api/gear.py; the pinned upstream has no gear lifecycle tool.
const (
	ToolCreateGear    = "create_gear"
	ToolUpdateGear    = "update_gear"
	ToolSetGearStatus = "set_gear_status"
	ToolDeleteGear    = "delete_gear"
)

// Gear argument bounds.
const (
	maxGearKeyArgumentLen = 32
	maxGearUsageKM        = 1_000_000
	maxGearUsageMinutes   = 16_000_000
	metersPerKilometer    = 1000
	gearStatusArgActive   = "active"
	gearStatusArgRetired  = "retired"
)

// gearFieldsInput is the optional field set create_gear and update_gear share.
type gearFieldsInput struct {
	GearType         *string   `json:"gear_type,omitempty" jsonschema:"the gear type"`
	Brand            *string   `json:"brand,omitempty" jsonschema:"the brand"`
	Model            *string   `json:"model,omitempty" jsonschema:"the model"`
	Name             *string   `json:"name,omitempty" jsonschema:"the name shown in Garmin Connect"`
	FirstUseDate     *string   `json:"first_use_date,omitempty" jsonschema:"the first use date"`
	UsageType        *string   `json:"usage_type,omitempty" jsonschema:"how usage is tracked"`
	MaxUsageKM       *float64  `json:"max_usage_distance_km,omitempty" jsonschema:"retirement distance"`
	MaxUsageMinutes  *float64  `json:"max_usage_duration_min,omitempty" jsonschema:"retirement duration"`
	Notes            *string   `json:"notes,omitempty" jsonschema:"the free-text note"`
	ActivityTypeKeys *[]string `json:"activity_type_keys,omitempty" jsonschema:"default activity types"`
}

// toFields converts the arguments to the domain model. Text and key validation is
// api.GearFields' own; only what the domain model cannot see is checked here.
func (in gearFieldsInput) toFields() (api.GearFields, error) {
	fields := api.GearFields{
		GearType: in.GearType, Brand: in.Brand, Model: in.Model, Name: in.Name,
		UsageType: in.UsageType, Notes: in.Notes, ActivityTypeKeys: in.ActivityTypeKeys,
	}
	if in.FirstUseDate != nil {
		date, err := parseCalendarDate("first_use_date", *in.FirstUseDate)
		if err != nil {
			return api.GearFields{}, err
		}
		fields.FirstUseDate = &date
	}
	if in.MaxUsageKM != nil {
		meters, err := api.KilometersToMeters(*in.MaxUsageKM)
		if err != nil {
			return api.GearFields{}, invalidArgument("max_usage_distance_km must be 0 or at least 0.001")
		}
		fields.MaxDistanceMeters = &meters
	}
	if in.MaxUsageMinutes != nil {
		seconds, err := api.MinutesToSeconds(*in.MaxUsageMinutes)
		if err != nil {
			return api.GearFields{}, invalidArgument("max_usage_duration_min must be 0 or at least 1/60")
		}
		fields.MaxDurationSeconds = &seconds
	}
	return fields, nil
}

// gearFieldProperties declares the field set; required marks the create-only
// mandatory fields.
func gearFieldProperties(required bool) []Property {
	text := func(name, description string) Property {
		return Property{
			Name: name, Types: []string{typeString}, Description: description,
			MaxLength: new(api.MaxGearTextLen), Required: required,
		}
	}
	firstUse := dateProperty("first_use_date", "the date the gear was first used")
	firstUse.Required = required
	return []Property{
		{
			Name: "gear_type", Types: []string{typeString}, Required: required,
			Description: "the gear type, for example SHOES or BIKE",
			Pattern:     `^[A-Za-z_]+$`, MaxLength: new(maxGearKeyArgumentLen),
		},
		text("brand", "the brand, for example the shoe maker"),
		text("model", "the model name"),
		text("name", "the name shown in Garmin Connect"),
		firstUse,
		{
			Name: "usage_type", Types: []string{typeString},
			Description: "how usage is tracked, for example DISTANCE or TIME",
			Pattern:     `^[A-Za-z_]+$`, MaxLength: new(maxGearKeyArgumentLen),
		},
		{
			Name: "max_usage_distance_km", Types: []string{typeNumber},
			Description: "the retirement distance in kilometers; 0 removes it",
			Minimum:     new(0.0), Maximum: new(float64(maxGearUsageKM)),
		},
		{
			Name: "max_usage_duration_min", Types: []string{typeNumber},
			Description: "the retirement duration in minutes; 0 removes it",
			Minimum:     new(0.0), Maximum: new(float64(maxGearUsageMinutes)),
		},
		{
			Name: "notes", Types: []string{typeString},
			Description: "the free-text note; an empty string clears it",
			MaxLength:   new(api.MaxTextLen),
		},
		{
			Name: "activity_type_keys", Types: []string{typeArray},
			Description: "lowercase activity type keys, for example running, this gear is the " +
				"default for. It replaces the current list; an empty list clears it",
			MaxItems: new(api.MaxGearActivityTypes),
			Items: map[string]any{
				keyType: typeString, keyPattern: `^[a-z0-9_]+$`, keyMaxLength: maxGearKeyArgumentLen,
			},
		},
	}
}

// GearRecord is one gear item after a create or an update.
//
// It is device material: returned to the authorized caller, never logged.
type GearRecord struct {
	UUID                 *string  `json:"uuid,omitempty" jsonschema:"the gear identifier"`
	Name                 *string  `json:"name,omitempty" jsonschema:"the name shown in Garmin Connect"`
	Brand                *string  `json:"brand,omitempty" jsonschema:"the brand"`
	Model                *string  `json:"model,omitempty" jsonschema:"the model"`
	GearType             *string  `json:"gear_type,omitempty" jsonschema:"the gear type"`
	FirstUseDate         *string  `json:"first_use_date,omitempty" jsonschema:"the first use date"`
	UsageType            *string  `json:"usage_type,omitempty" jsonschema:"how usage is tracked"`
	MaxUsageKM           *float64 `json:"max_usage_distance_km,omitempty" jsonschema:"retirement distance, when set"`
	MaxUsageMinutes      *float64 `json:"max_usage_duration_min,omitempty" jsonschema:"retirement duration, when set"`
	Notes                *string  `json:"notes,omitempty" jsonschema:"the note, when set"`
	DefaultActivityTypes []string `json:"default_activity_types,omitempty" jsonschema:"its default activity types"`
}

// LogValue reports which fields arrived, never a name or note.
func (r GearRecord) LogValue() slog.Value {
	return shape("gearRecord",
		slog.String("uuid", presence(r.UUID != nil)),
		slog.Int("defaultActivityTypes", len(r.DefaultActivityTypes)),
	)
}

// newGearRecord maps the v2 gear object.
func newGearRecord(detail api.GearDetail) GearRecord {
	return GearRecord{
		UUID: detail.UUID, Name: detail.Name, Brand: detail.Brand, Model: detail.Model,
		GearType: detail.GearType, FirstUseDate: detail.FirstUseDate, UsageType: detail.UsageType,
		MaxUsageKM:           scaledUsage(detail.MaxUsageDistanceMeters, metersPerKilometer),
		MaxUsageMinutes:      scaledUsage(detail.MaxUsageDurationSeconds, secondsPerMinute),
		Notes:                nonEmpty(detail.Notes),
		DefaultActivityTypes: defaultActivityTypes(detail.AssociatedActivityTypes),
	}
}

// scaledUsage converts a positive threshold to the caller's unit; zero is "unset".
func scaledUsage(value client.Number, divisor float64) *float64 {
	raw, ok := value.Float64()
	if !ok || raw <= 0 {
		return nil
	}
	scaled := math.Round(raw/divisor*1000) / 1000
	return &scaled
}

func nonEmpty(value *string) *string {
	if value == nil || *value == "" {
		return nil
	}
	return value
}

// defaultActivityTypes reads the keys marked defaultGear. An unexpected shape is
// reported as no defaults rather than failing an applied write.
func defaultActivityTypes(raw json.RawMessage) []string {
	var entries []struct {
		Key         *string `json:"activityTypeKey"`
		DefaultGear *bool   `json:"defaultGear"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	var keys []string
	for _, entry := range entries {
		if entry.Key != nil && entry.DefaultGear != nil && *entry.DefaultGear {
			keys = append(keys, *entry.Key)
		}
	}
	return keys
}

// GearChange reports a status change or a removal.
type GearChange struct {
	GearUUID string `json:"gear_uuid" jsonschema:"the gear identifier"`
	Outcome  string `json:"outcome" jsonschema:"what happened, for example gear_retired"`
	Status   int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
}

// LogValue reports the outcome, never which gear it named.
func (c GearChange) LogValue() slog.Value {
	return shape("gearChange", slog.String("outcome", c.Outcome), slog.Int("status", c.Status))
}

type createGearInput struct {
	gearFieldsInput
}

func createGearContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCreateGear,
			Title: "Create gear",
			Description: "create a piece of gear, such as a pair of shoes or a bike. Only " +
				"gear_type SHOES with usage_type DISTANCE is confirmed against a real account. " +
				"Creates a new item every time it is called: read get_gear first to avoid a duplicate",
			Tier:        policy.TierWrite,
			Category:    categoryDevice,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(gearFieldProperties(true)...),
	}
}

func registerCreateGear(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in createGearInput) (
		*mcp.CallToolResult, GearRecord, error,
	) {
		fields, err := in.toFields()
		if err != nil {
			return nil, GearRecord{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, GearRecord{}, err
		}
		created, err := svc.gear.Create(ctx, session, fields)
		if err != nil {
			return nil, GearRecord{}, fail(err)
		}
		return nil, newGearRecord(created), nil
	}
	return mcpserver.AddTool(registry, createGearContract().Registration(), handler)
}

type updateGearInput struct {
	GearUUID string `json:"gear_uuid" jsonschema:"the gear identifier from get_gear"`
	gearFieldsInput
}

func updateGearContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolUpdateGear,
			Title: "Update gear",
			Description: "change the given fields of one piece of gear; an omitted field keeps " +
				"its value. Garmin replaces the whole item, so this tool reads it first and " +
				"writes it back with the changes. Use set_gear_status to retire gear",
			Tier:        policy.TierWrite,
			Category:    categoryDevice,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(append([]Property{gearUUIDProperty("update")}, gearFieldProperties(false)...)...),
	}
}

func registerUpdateGear(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in updateGearInput) (
		*mcp.CallToolResult, GearRecord, error,
	) {
		gear, err := parseGearIdentifier(in.GearUUID)
		if err != nil {
			return nil, GearRecord{}, err
		}
		fields, err := in.toFields()
		if err != nil {
			return nil, GearRecord{}, err
		}
		if fields == (api.GearFields{}) {
			return nil, GearRecord{}, invalidArgument("at least one field to change is required")
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, GearRecord{}, err
		}
		updated, err := svc.gear.Update(ctx, session, gear, fields)
		if err != nil {
			return nil, GearRecord{}, fail(err)
		}
		return nil, newGearRecord(updated), nil
	}
	return mcpserver.AddTool(registry, updateGearContract().Registration(), handler)
}

type setGearStatusInput struct {
	GearUUID string `json:"gear_uuid" jsonschema:"the gear identifier from get_gear"`
	Status   string `json:"status" jsonschema:"active or retired"`
}

func setGearStatusContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetGearStatus,
			Title: "Retire or reactivate gear",
			Description: "retire one piece of gear or make it active again. Its history is " +
				"kept, and setting the status it already has leaves the same end state",
			Tier:        policy.TierWrite,
			Category:    categoryDevice,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(gearUUIDProperty("change"), Property{
			Name: "status", Types: []string{typeString}, Description: "the new gear status",
			Enum: []any{gearStatusArgActive, gearStatusArgRetired}, Required: true,
		}),
	}
}

func registerSetGearStatus(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setGearStatusInput) (
		*mcp.CallToolResult, GearChange, error,
	) {
		gear, err := parseGearIdentifier(in.GearUUID)
		if err != nil {
			return nil, GearChange{}, err
		}
		status, outcome := api.GearStatusActive, "gear_activated"
		switch in.Status {
		case gearStatusArgActive:
		case gearStatusArgRetired:
			status, outcome = api.GearStatusRetired, "gear_retired"
		default:
			return nil, GearChange{}, invalidArgument("status must be active or retired")
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, GearChange{}, err
		}
		result, err := svc.gear.SetStatus(ctx, session, gear, status)
		if err != nil {
			return nil, GearChange{}, fail(err)
		}
		return nil, GearChange{GearUUID: gear.String(), Outcome: outcome, Status: result.Status}, nil
	}
	return mcpserver.AddTool(registry, setGearStatusContract().Registration(), handler)
}

type deleteGearInput struct {
	GearUUID string `json:"gear_uuid" jsonschema:"the gear identifier from get_gear"`
}

func deleteGearContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDeleteGear,
			Title: "Delete gear",
			Description: "permanently delete one piece of gear. It cannot be undone and it " +
				"requires confirmation. To keep the gear's history, retire it with set_gear_status",
			Tier:        policy.TierDestructive,
			Category:    categoryDevice,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(gearUUIDProperty("delete")),
	}
}

func registerDeleteGear(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deleteGearInput) (
		*mcp.CallToolResult, GearChange, error,
	) {
		gear, err := parseGearIdentifier(in.GearUUID)
		if err != nil {
			return nil, GearChange{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, GearChange{}, err
		}
		result, err := svc.gear.Delete(ctx, session, gear)
		if err != nil {
			return nil, GearChange{}, fail(err)
		}
		return nil, GearChange{GearUUID: gear.String(), Outcome: "gear_deleted", Status: result.Status}, nil
	}
	return mcpserver.AddTool(registry, deleteGearContract().Registration(), handler)
}
