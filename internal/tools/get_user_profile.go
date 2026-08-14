package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetUserProfile is the upstream compatibility name of the profile tool.
const ToolGetUserProfile = "get_user_profile"

// UserProfile is the flat profile object: the account's own identity as Garmin holds
// it. Every field is optional, because Garmin omits fields per account age and
// privacy setting.
//
// It is identity material, so LogValue reports presence rather than content.
type UserProfile struct {
	// ProfileID is the account's numeric profile identifier.
	ProfileID *int64 `json:"profile_id,omitempty" jsonschema:"the numeric Garmin profile identifier"`

	// DisplayName is the account's public display name.
	DisplayName *string `json:"display_name,omitempty" jsonschema:"the account's display name"`

	// FullName is the account's full name.
	FullName *string `json:"full_name,omitempty" jsonschema:"the account's full name"`

	// Location is the free-text location on the profile, when the account sets one.
	Location *string `json:"location,omitempty" jsonschema:"the free-text profile location"`

	// UserLevel is the Garmin Connect gamification level.
	UserLevel *float64 `json:"user_level,omitempty" jsonschema:"the Garmin Connect user level"`
}

// LogValue reports the shape of the profile, never the identity in it.
func (p UserProfile) LogValue() slog.Value {
	return shape("userProfile",
		slog.String("profileId", presence(p.ProfileID != nil)),
		slog.String("displayName", presence(p.DisplayName != nil)),
		slog.String("fullName", presence(p.FullName != nil)),
		slog.String("location", presence(p.Location != nil)),
	)
}

// getUserProfileContract declares the tool. The input schema is empty on purpose:
// there is no argument through which a caller could name an account, and adding one
// would not help, because the principal comes from the request context.
func getUserProfileContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetUserProfile,
			Title: "Get user profile",
			Description: "read the authenticated Garmin account's own profile: profile id, " +
				"display name, full name, location and user level. Takes no arguments; the " +
				"account is the one this session is authenticated for",
			Tier:        policy.TierReadOnly,
			Category:    categoryProfile,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetUserProfile registers the tool against the shared entry point, so it is
// gated by the same middleware chain as every other tool.
func registerGetUserProfile(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, UserProfile, error,
	) {
		profile, err := svc.socialProfile(ctx)
		if err != nil {
			return nil, UserProfile{}, err
		}
		return nil, newUserProfile(profile), nil
	}
	return mcpserver.AddTool(registry, getUserProfileContract().Registration(), handler)
}

// socialProfile reads the flat profile for the request's principal.
func (s *service) socialProfile(ctx context.Context) (api.SocialProfile, error) {
	session, err := s.session(ctx)
	if err != nil {
		return api.SocialProfile{}, err
	}
	profile, err := s.profile.Social(ctx, session)
	if err != nil {
		return api.SocialProfile{}, fail(err)
	}
	return profile, nil
}

// newUserProfile maps the domain model onto the bounded result. The raw payload, the
// Garmin GUID and the role list are deliberately dropped: none of them is what the
// tool was asked for.
func newUserProfile(profile api.SocialProfile) UserProfile {
	return UserProfile{
		ProfileID:   profile.ProfileID,
		DisplayName: profile.DisplayName,
		FullName:    profile.FullName,
		Location:    profile.Location,
		UserLevel:   optionalFloat(profile.UserLevel),
	}
}
