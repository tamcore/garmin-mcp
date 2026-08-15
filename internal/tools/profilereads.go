package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the remaining profile reads.
const (
	ToolGetUserProfileSettings = "get_userprofile_settings"
	ToolGetPersonalRecord      = "get_personal_record"
)

// ProfileSettingsResult is the account's profile settings document.
//
// It is identity material and is never logged. The document Garmin serves carries
// more than this: the fields kept here are the ones a caller can act on, and the
// volatile display-format sub-objects are dropped rather than passed through.
type ProfileSettingsResult struct {
	ProfileID       *int64  `json:"profile_id,omitempty" jsonschema:"the Garmin profile identifier"`
	DisplayName     *string `json:"display_name,omitempty" jsonschema:"the account's display name"`
	FullName        *string `json:"full_name,omitempty" jsonschema:"the account's full name"`
	UserName        *string `json:"user_name,omitempty" jsonschema:"the account's user name"`
	Location        *string `json:"location,omitempty" jsonschema:"the location on the profile"`
	Gender          *string `json:"gender,omitempty" jsonschema:"the gender on the profile"`
	BirthDate       *string `json:"birth_date,omitempty" jsonschema:"the birth date on the profile"`
	MeasurementUnit *string `json:"measurement_system,omitempty" jsonschema:"the measurement system"`
}

// LogValue reports which fields were present, never the identity in them.
func (r ProfileSettingsResult) LogValue() slog.Value {
	return shape("profileSettings",
		slog.String("display_name", presence(r.DisplayName != nil)),
		slog.String("location", presence(r.Location != nil)),
		slog.String("birth_date", presence(r.BirthDate != nil)),
	)
}

func getUserProfileSettingsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetUserProfileSettings,
			Title: "Get profile settings",
			Description: "read the account's profile settings, which is a different document " +
				"from the account settings get_user_profile returns",
			Tier:        policy.TierReadOnly,
			Category:    categoryProfile,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

func registerGetUserProfileSettings(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, ProfileSettingsResult, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, ProfileSettingsResult{}, err
		}

		settings, err := svc.profile.ProfileSettings(ctx, session)
		if err != nil {
			return nil, ProfileSettingsResult{}, fail(err)
		}
		return nil, newProfileSettingsResult(settings), nil
	}
	return mcpserver.AddTool(registry, getUserProfileSettingsContract().Registration(), handler)
}

func newProfileSettingsResult(settings api.ProfileSettings) ProfileSettingsResult {
	return ProfileSettingsResult{
		ProfileID:       optionalInt64(settings.ProfileID),
		DisplayName:     settings.DisplayName,
		FullName:        settings.FullName,
		UserName:        settings.UserName,
		Location:        settings.Location,
		Gender:          optionalText(settings.Gender),
		BirthDate:       settings.BirthDate,
		MeasurementUnit: optionalText(settings.MeasurementUnit),
	}
}

// A PersonalRecord is one recorded best performance. It is health data.
type PersonalRecord struct {
	TypeID       *int64   `json:"type_id,omitempty" jsonschema:"Garmin's record type identifier"`
	ActivityID   *int64   `json:"activity_id,omitempty" jsonschema:"the activity that set the record"`
	ActivityName *string  `json:"activity_name,omitempty" jsonschema:"the name of that activity"`
	Value        *float64 `json:"value,omitempty" jsonschema:"the recorded value, in Garmin's own unit"`
	StartGMT     *string  `json:"start_time_gmt,omitempty" jsonschema:"when the record was set, in UTC"`
}

// A PersonalRecordList is the bounded personal-record collection.
type PersonalRecordList struct {
	Records   []PersonalRecord `json:"records" jsonschema:"the personal records, as Garmin returned them"`
	Count     int              `json:"count" jsonschema:"how many records this result carries"`
	Truncated bool             `json:"truncated" jsonschema:"whether the list was cut at this server's bound"`
}

// LogValue reports the record count, never a performance.
func (l PersonalRecordList) LogValue() slog.Value {
	return shape("personalRecordList",
		slog.Int("records", len(l.Records)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getPersonalRecordContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetPersonalRecord,
			Title: "Get personal records",
			Description: "read the account's personal records: the best performances Garmin " +
				"has recorded, with the activity each one came from",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

func registerGetPersonalRecord(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, PersonalRecordList, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, PersonalRecordList{}, err
		}
		name, err := svc.displayName(ctx, session)
		if err != nil {
			return nil, PersonalRecordList{}, err
		}

		records, err := svc.profile.PersonalRecords(ctx, session, name)
		if err != nil {
			return nil, PersonalRecordList{}, fail(err)
		}
		return nil, newPersonalRecordList(records, svc.bounds.MaxPersonalRecords), nil
	}
	return mcpserver.AddTool(registry, getPersonalRecordContract().Registration(), handler)
}

func newPersonalRecordList(records []api.PersonalRecord, limit int) PersonalRecordList {
	truncated := len(records) > limit
	if truncated {
		records = records[:limit]
	}

	out := make([]PersonalRecord, 0, len(records))
	for _, record := range records {
		out = append(out, PersonalRecord{
			TypeID:       optionalInt64(record.TypeID),
			ActivityID:   optionalInt64(record.ActivityID),
			ActivityName: record.ActivityName,
			Value:        optionalFloat(record.Value),
			StartGMT:     record.StartGMT,
		})
	}
	return PersonalRecordList{Records: out, Count: len(out), Truncated: truncated}
}
