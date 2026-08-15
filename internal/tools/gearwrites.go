package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the gear links.
const (
	ToolAddGearToActivity      = "add_gear_to_activity"
	ToolRemoveGearFromActivity = "remove_gear_from_activity"
)

// gearLinkInput is the argument set both gear tools take.
//
// The identifier is a plain integer here, not the number-or-string union the activity
// detail tools take, because that is what the manifest declares for these two.
type gearLinkInput struct {
	ActivityID int64  `json:"activity_id" jsonschema:"the Garmin activity identifier"`
	GearUUID   string `json:"gear_uuid" jsonschema:"the gear identifier from get_gear"`
}

// gearLinkSchema declares the argument set both gear tools take.
func gearLinkSchema(action string) Schema {
	return NewSchema(activityIDIntegerProperty(), Property{
		Name:        "gear_uuid",
		Types:       []string{typeString},
		Description: "the canonical UUID of the gear to " + action + ", from get_gear",
		Pattern:     `^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`,
		MaxLength:   new(maxGearUUIDLen),
		Required:    true,
	})
}

func addGearToActivityContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolAddGearToActivity,
			Title: "Link gear to an activity",
			Description: "link one piece of gear, such as a pair of shoes or a bike, to one " +
				"activity. Linking gear that is already linked leaves the same end state",
			Tier:        policy.TierWrite,
			Category:    categoryDevice,
			Annotations: writeAnnotations(true),
		},
		Schema: gearLinkSchema("link"),
	}
}

func registerAddGearToActivity(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in gearLinkInput) (
		*mcp.CallToolResult, ActivityUpdate, error,
	) {
		link, err := svc.resolveGearLink(ctx, in)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}

		result, err := svc.gear.Add(ctx, link.session, link.gear, link.id)
		if err != nil {
			return nil, ActivityUpdate{}, fail(err)
		}
		return nil, newActivityUpdate(link.id, "gear_linked", result), nil
	}
	return mcpserver.AddTool(registry, addGearToActivityContract().Registration(), handler)
}

func removeGearFromActivityContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolRemoveGearFromActivity,
			Title: "Unlink gear from an activity",
			Description: "unlink one piece of gear from one activity. The gear itself is " +
				"untouched, and unlinking gear that is not linked leaves the same end state",
			Tier:        policy.TierWrite,
			Category:    categoryDevice,
			Annotations: writeAnnotations(true),
		},
		Schema: gearLinkSchema("unlink"),
	}
}

func registerRemoveGearFromActivity(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in gearLinkInput) (
		*mcp.CallToolResult, ActivityUpdate, error,
	) {
		link, err := svc.resolveGearLink(ctx, in)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}

		result, err := svc.gear.Remove(ctx, link.session, link.gear, link.id)
		if err != nil {
			return nil, ActivityUpdate{}, fail(err)
		}
		return nil, newActivityUpdate(link.id, "gear_unlinked", result), nil
	}
	return mcpserver.AddTool(registry, removeGearFromActivityContract().Registration(), handler)
}

// gearLink is a validated gear link request.
type gearLink struct {
	id      client.ID
	gear    api.GearUUID
	session client.Session
}

// resolveGearLink validates both identifiers before it resolves the session, so a
// malformed argument costs no Garmin call at all.
func (s *service) resolveGearLink(ctx context.Context, in gearLinkInput) (gearLink, error) {
	id, err := parseIdentifier("activity_id", in.ActivityID)
	if err != nil {
		return gearLink{}, err
	}
	gear, err := parseGearIdentifier(in.GearUUID)
	if err != nil {
		return gearLink{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return gearLink{}, err
	}
	return gearLink{id: id, gear: gear, session: session}, nil
}
