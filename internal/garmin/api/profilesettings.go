package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// ProfileSettings is the profile settings document, which is a different
// document from the account settings UserSettings holds.
//
// It is sensitive: it names the person and where they are. Every field is
// optional, because Garmin omits fields per account age and privacy setting, and
// an unknown field never fails the response.
type ProfileSettings struct {
	ID              client.Number   `json:"id"`
	ProfileID       client.Number   `json:"profileId"`
	DisplayName     *string         `json:"displayName"`
	FullName        *string         `json:"fullName"`
	UserName        *string         `json:"userName"`
	Location        *string         `json:"location"`
	Gender          client.Text     `json:"gender"`
	BirthDate       *string         `json:"birthDate"`
	MeasurementUnit client.Text     `json:"measurementSystem"`
	PowerFormat     json.RawMessage `json:"powerFormat"`
	HeartRateFormat json.RawMessage `json:"heartRateFormat"`

	raw client.Payload
}

// Payload is the retained raw response.
func (p ProfileSettings) Payload() client.Payload { return p.raw }

// ProfileSettings reads the profile settings document.
//
// Source: get_userprofile_settings over garmin_connect_userprofile_settings_url.
func (p *Profile) ProfileSettings(
	ctx context.Context, session client.Session,
) (ProfileSettings, error) {
	req := readRequest(client.OpGetUserProfileSettings, client.EndpointUserProfileSettings,
		client.PathUserProfileSettings, nil)

	var settings ProfileSettings
	payload, err := p.req.read(ctx, session, req, &settings)
	if err != nil {
		return ProfileSettings{}, err
	}
	settings.raw = payload
	return settings, nil
}

// UnitSystem reports the account's measurement system, for example "metric" or
// "statute_us".
//
// Source: get_unit_system, which upstream serves from the userData of the
// account settings document it cached at login. This package holds no
// per-session cache, so the document is read when it is asked for.
func (p *Profile) UnitSystem(ctx context.Context, session client.Session) (string, error) {
	settings, err := p.Settings(ctx, session)
	if err != nil {
		return "", err
	}

	req := readRequest(client.OpGetUserSettings, client.EndpointUserSettings,
		client.PathUserSettings, nil)
	if settings.UserData == nil {
		return "", unexpected(req, fmt.Errorf(
			"%w: the settings document carried no userData", client.ErrMalformedPayload))
	}
	system, ok := settings.UserData.MeasurementSystem.Value()
	if !ok {
		return "", unexpected(req, fmt.Errorf(
			"%w: the settings document carried no measurement system",
			client.ErrMalformedPayload))
	}
	return system, nil
}

// FullName reports the account's full name.
//
// It is identity material: hand it to an authorized caller, never to a log.
func (p *Profile) FullName(ctx context.Context, session client.Session) (string, error) {
	profile, err := p.Social(ctx, session)
	if err != nil {
		return "", err
	}
	if profile.FullName == nil {
		req := readRequest(client.OpGetSocialProfile, client.EndpointSocialProfile,
			client.PathSocialProfile, nil)
		return "", unexpected(req, fmt.Errorf("%w: the profile carried no full name",
			client.ErrMalformedPayload))
	}
	return *profile.FullName, nil
}

// PersonalRecord is one personal record.
//
// It is health data: a record is a measured performance, so it is never logged.
type PersonalRecord struct {
	ID         client.Number `json:"id"`
	TypeID     client.Number `json:"typeId"`
	ActivityID client.Number `json:"activityId"`
	Value      client.Number `json:"value"`
	// StartLocal and StartGMT are client.Text rather than *string because Garmin
	// sends them as a number — an epoch in milliseconds, not the timestamp string
	// the field names suggest. Declared as strings, they made every real account's
	// personal records undecodable. The live suite found that; no fixture did,
	// because the fixtures were written to the same wrong assumption as the model.
	StartLocal   client.Text     `json:"prStartTimeLocal"`
	StartGMT     client.Text     `json:"prStartTimeGmt"`
	ActivityName client.Text     `json:"activityName"`
	ActivityType json.RawMessage `json:"activityType"`
}

// PersonalRecords reads the account's personal records.
//
// The display name is a path segment, so it is a validated client.DisplayName
// escaped into exactly one segment. Source: get_personal_record, whose
// _require_display_name exists precisely to stop an unset or hostile name from
// reaching a URL path.
func (p *Profile) PersonalRecords(
	ctx context.Context, session client.Session, name client.DisplayName,
) ([]PersonalRecord, error) {
	req := readRequest(client.OpGetPersonalRecords, client.EndpointPersonalRecords,
		displayNamePath(client.PathPersonalRecords, name), nil)
	if err := requireDisplayName(req, name); err != nil {
		return nil, err
	}

	var records client.List[PersonalRecord]
	if _, err := p.req.read(ctx, session, req, &records); err != nil {
		return nil, err
	}
	return records.Items(), nil
}
