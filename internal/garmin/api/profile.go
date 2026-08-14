package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Profile reads the account's own profile and settings.
//
// Source: connectapi("/userprofile-service/socialProfile") and
// garmin_connect_user_settings_url in python-garminconnect 0.3.10.
type Profile struct {
	req requester
}

// NewProfile returns a profile client over the request layer.
func NewProfile(rc *client.Client) (*Profile, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Profile{req: req}, nil
}

// SocialProfile is the flat profile object.
//
// It is sensitive: it carries identity — display name, full name, location — so it
// must not be logged. LogValue reports its shape instead; see sensitive.go. Every
// field is optional, because Garmin omits fields per account age and privacy
// setting, and an unknown field never fails the response.
type SocialProfile struct {
	ProfileID       *int64          `json:"profileId"`
	DisplayName     *string         `json:"displayName"`
	FullName        *string         `json:"fullName"`
	UserName        *string         `json:"userName"`
	Location        *string         `json:"location"`
	ProfileImageURL *string         `json:"profileImageUrlLarge"`
	UserLevel       client.Number   `json:"userLevel"`
	GarminGUID      *string         `json:"garminGUID"`
	UserRoles       json.RawMessage `json:"userRoles"`

	raw client.Payload
}

// Payload is the retained raw response, kept for diagnostics. It is bounded,
// sealed and unloggable.
func (p SocialProfile) Payload() client.Payload { return p.raw }

// UserSettings is the account settings document, whose useful content sits in the
// nested userData object.
//
// It is sensitive: userData carries birth date, weight and measurement preferences.
type UserSettings struct {
	ID       *int64    `json:"id"`
	UserData *UserData `json:"userData"`

	raw client.Payload
}

// Payload is the retained raw response.
func (s UserSettings) Payload() client.Payload { return s.raw }

// UserData is the nested settings object. Source: the userData["measurementSystem"]
// read in _load_profile_and_settings.
type UserData struct {
	MeasurementSystem client.Text     `json:"measurementSystem"`
	BirthDate         *string         `json:"birthDate"`
	Gender            client.Text     `json:"gender"`
	Weight            client.Number   `json:"weight"`
	Height            client.Number   `json:"height"`
	Handedness        client.Text     `json:"handedness"`
	TimeFormat        client.Text     `json:"timeFormat"`
	SleepWindows      json.RawMessage `json:"sleepTimeWindows"`
}

// Social reads the flat social profile.
func (p *Profile) Social(ctx context.Context, session client.Session) (SocialProfile, error) {
	req := readRequest(client.OpGetSocialProfile, client.EndpointSocialProfile,
		client.PathSocialProfile, nil)

	var profile SocialProfile
	payload, err := p.req.read(ctx, session, req, &profile)
	if err != nil {
		return SocialProfile{}, err
	}
	profile.raw = payload
	return profile, nil
}

// Settings reads the account settings document.
func (p *Profile) Settings(ctx context.Context, session client.Session) (UserSettings, error) {
	req := readRequest(client.OpGetUserSettings, client.EndpointUserSettings,
		client.PathUserSettings, nil)

	var settings UserSettings
	payload, err := p.req.read(ctx, session, req, &settings)
	if err != nil {
		return UserSettings{}, err
	}
	settings.raw = payload
	return settings, nil
}

// DisplayName reads the profile and validates its display name for use as a URL
// path segment, which the date-keyed wellness endpoints require.
//
// Source: _require_display_name. An absent or empty display name is a real Garmin
// state for a new account, and interpolating "None" into a path yields a 403, so it
// is reported as a validation failure the caller can act on rather than as a server
// error.
func (p *Profile) DisplayName(ctx context.Context, session client.Session) (client.DisplayName, error) {
	profile, err := p.Social(ctx, session)
	if err != nil {
		return client.DisplayName{}, err
	}

	req := readRequest(client.OpGetSocialProfile, client.EndpointSocialProfile,
		client.PathSocialProfile, nil)
	if profile.DisplayName == nil {
		return client.DisplayName{}, invalid(req, fmt.Errorf(
			"%w: the Garmin profile carries no display name; set one in Garmin Connect",
			client.ErrValidation))
	}

	name, parseErr := client.ParseDisplayName(*profile.DisplayName)
	if parseErr != nil {
		return client.DisplayName{}, invalid(req, parseErr)
	}
	return name, nil
}
