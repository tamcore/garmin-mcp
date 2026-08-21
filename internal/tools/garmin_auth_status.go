package tools

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGarminAuthStatus is the compatibility name of the session-status tool.
const ToolGarminAuthStatus = "garmin_auth_status"

const (
	authStatusReasonNoCredentials = "no_credentials"
	authStatusReasonRejected      = "rejected"
)

// GarminAuthStatus reports whether a live Garmin profile request authenticated.
type GarminAuthStatus struct {
	Authenticated bool    `json:"authenticated" jsonschema:"whether Garmin accepted the live profile request"`
	Account       *string `json:"account,omitempty" jsonschema:"the Garmin account's full name, when set"`
	Reason        string  `json:"reason,omitempty" jsonschema:"why session is unusable: no_credentials or rejected"`
}

// LogValue reports presence, never the account name.
func (s GarminAuthStatus) LogValue() slog.Value {
	return shape("garminAuthStatus",
		slog.Bool("authenticated", s.Authenticated),
		slog.String("account", presence(s.Account != nil)),
		slog.String("reason", presence(s.Reason != "")),
	)
}

func garminAuthStatusContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGarminAuthStatus,
			Title: "Garmin authentication status",
			Description: "check whether the current account has a usable Garmin session by " +
				"making a live profile request. The read may refresh stored DI tokens. Takes no " +
				"arguments; the account is the one this session is authenticated for",
			Tier:        policy.TierReadOnly,
			Category:    categoryProfile,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

func registerGarminAuthStatus(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, GarminAuthStatus, error,
	) {
		status, err := svc.garminAuthStatus(ctx)
		return nil, status, err
	}
	return mcpserver.AddTool(registry, garminAuthStatusContract().Registration(), handler)
}

func (s *service) garminAuthStatus(ctx context.Context) (GarminAuthStatus, error) {
	profile, err := s.socialProfile(ctx)
	if err == nil {
		return GarminAuthStatus{Authenticated: true, Account: nonBlankAccount(profile.FullName)}, nil
	}

	switch {
	case errors.Is(err, auth.ErrNoTokens), errors.Is(err, auth.ErrNoRefreshToken):
		return GarminAuthStatus{Reason: authStatusReasonNoCredentials}, nil
	case errors.Is(err, auth.ErrRefreshRejected), isUnauthorizedProfile(err):
		return GarminAuthStatus{Reason: authStatusReasonRejected}, nil
	default:
		return GarminAuthStatus{}, err
	}
}

func nonBlankAccount(account *string) *string {
	if account == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*account)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isUnauthorizedProfile(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized
}
