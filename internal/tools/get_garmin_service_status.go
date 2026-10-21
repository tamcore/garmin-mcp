package tools

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garminstatus/pkg/garminstatus"
)

// ToolGetGarminServiceStatus is the name of the public service-status tool.
const ToolGetGarminServiceStatus = "get_garmin_service_status"

// ServiceState is one Garmin service as the status page lists it.
type ServiceState struct {
	Name    string   `json:"name" jsonschema:"the service name as Garmin's status page lists it"`
	Status  string   `json:"status" jsonschema:"up or down"`
	Reasons []string `json:"reasons,omitempty" jsonschema:"Garmin's stated reasons, when a service is degraded"`
}

// GarminServiceStatus is Garmin's public service status, sorted by name.
type GarminServiceStatus struct {
	Platforms []ServiceState `json:"platforms" jsonschema:"platform services, such as Garmin Connect"`
	Features  []ServiceState `json:"features" jsonschema:"feature services, such as sync or activity upload"`
}

// LogValue reports counts, never service names or reasons.
func (s GarminServiceStatus) LogValue() slog.Value {
	return shape("garminServiceStatus",
		slog.Int("platforms", len(s.Platforms)),
		slog.Int("features", len(s.Features)),
	)
}

func getGarminServiceStatusContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetGarminServiceStatus,
			Title: "Garmin service status",
			Description: "read Garmin's public service-status page: whether each Garmin " +
				"platform and feature is up or down, with Garmin's stated reasons. Sends no " +
				"credential, so it answers when the account's session is broken. Takes no arguments",
			Tier:        policy.TierReadOnly,
			Category:    categoryOrdinary,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

func registerGetGarminServiceStatus(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, GarminServiceStatus, error,
	) {
		status, err := svc.garminServiceStatus(ctx)
		return nil, status, err
	}
	return mcpserver.AddTool(registry, getGarminServiceStatusContract().Registration(), handler)
}

func (s *service) garminServiceStatus(ctx context.Context) (GarminServiceStatus, error) {
	status, err := garminstatus.FetchStatus(ctx, s.statusClient, s.statusURL)
	if err != nil {
		return GarminServiceStatus{}, serviceStatusFailure(err)
	}
	return GarminServiceStatus{
		Platforms: serviceStates(status.Platforms),
		Features:  serviceStates(status.Features),
	}, nil
}

func serviceStates(services garminstatus.ServiceMap) []ServiceState {
	states := make([]ServiceState, 0, len(services))
	for name, info := range services {
		states = append(states, ServiceState{
			Name: name, Status: string(info.Status), Reasons: info.StatusReason,
		})
	}
	slices.SortFunc(states, func(a, b ServiceState) int { return cmp.Compare(a.Name, b.Name) })
	return states
}

func serviceStatusFailure(err error) error {
	advice := "Garmin's status page could not be read. Retry in a moment."
	switch {
	case errors.Is(err, garminstatus.ErrNoServices):
		advice = "Garmin's status page listed no service this server could read. This is " +
			"usually upstream drift; retrying rarely helps."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		advice = advise(err)
	}
	return &ToolError{Advice: advice, Err: err}
}
