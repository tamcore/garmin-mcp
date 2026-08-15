package cmd

import (
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

// This file holds the half of the dependency graph that is identical in both
// deployment shapes: the tool set, the policy the tiers are gated by, and the
// per-principal rate limiter. It is separate from wiring.go so neither file grows
// past the size the house conventions allow.

// buildToolsAndPolicy invokes the tool factory and builds the policy from the tier
// lists it reported, intersected with the operator's enablement and name lists.
func (d *dependencies) buildToolsAndPolicy(factory ToolFactory) error {
	set, err := d.contributeTools(factory)
	if err != nil {
		return err
	}

	readOnly, write, destructive := set.tierNames()
	gate, err := policy.New(policy.Config{
		Mode:              d.mode,
		ReadOnlyTools:     readOnly,
		WriteTools:        write,
		DestructiveTools:  destructive,
		EnableWrite:       d.cfg.EnableWriteTools,
		EnableDestructive: d.cfg.EnableDestructiveTools,
		Allowlist:         d.cfg.ToolAllowlist,
		Denylist:          d.cfg.ToolDenylist,
	}, d.scopes)
	if err != nil {
		return fmt.Errorf("building the tool policy: %w", err)
	}

	d.tools = set
	d.policy = gate
	return nil
}

// contributeTools asks the factory for its tool set. A nil factory contributes
// nothing, which is a supported deployment rather than an error.
func (d *dependencies) contributeTools(factory ToolFactory) (ToolSet, error) {
	if factory == nil {
		return ToolSet{}, nil
	}

	toolDeps, err := d.toolDeps()
	if err != nil {
		return ToolSet{}, err
	}
	set, err := factory(toolDeps)
	if err != nil {
		return ToolSet{}, fmt.Errorf("building the tool set: %w", err)
	}
	return set, nil
}

// toolDeps builds the domain clients a tool package works through. Every one of
// them shares the single request layer, so limits, retries, and error
// classification are identical across domains.
func (d *dependencies) toolDeps() (ToolDeps, error) {
	activities, err := api.NewActivities(d.rest)
	if err != nil {
		return ToolDeps{}, fmt.Errorf("building the activities client: %w", err)
	}
	details, err := api.NewActivityDetails(d.rest)
	if err != nil {
		return ToolDeps{}, fmt.Errorf("building the activity details client: %w", err)
	}
	devices, err := api.NewDevices(d.rest)
	if err != nil {
		return ToolDeps{}, fmt.Errorf("building the devices client: %w", err)
	}
	profile, err := api.NewProfile(d.rest)
	if err != nil {
		return ToolDeps{}, fmt.Errorf("building the profile client: %w", err)
	}
	wellness, err := api.NewWellness(d.rest)
	if err != nil {
		return ToolDeps{}, fmt.Errorf("building the wellness client: %w", err)
	}

	return ToolDeps{
		Client:          d.rest,
		Caller:          d.refresher,
		Activities:      activities,
		ActivityDetails: details,
		Devices:         devices,
		Profile:         profile,
		Wellness:        wellness,
	}, nil
}

// buildLimiter builds the per-principal rate limiter from the configured budgets.
func (d *dependencies) buildLimiter() error {
	limiter, err := ratelimit.New(ratelimit.Config{
		ReadPerMinute:  d.cfg.ReadRateLimitPerMinute,
		ReadBurst:      burstFor(d.cfg.ReadRateLimitPerMinute, ratelimit.DefaultReadBurst),
		WritePerMinute: d.cfg.WriteRateLimitPerMinute,
		WriteBurst:     burstFor(d.cfg.WriteRateLimitPerMinute, ratelimit.DefaultWriteBurst),
		MaxPrincipals:  ratelimit.DefaultMaxPrincipals,
	}, nil)
	if err != nil {
		return fmt.Errorf("building the per-principal rate limiter: %w", err)
	}
	d.limiter = limiter
	return nil
}

// burstFor keeps the instantaneous allowance at or below the sustained budget, so
// an operator who lowered a rate does not keep the shipped burst.
func burstFor(perMinute, def int) int {
	if perMinute < def {
		return perMinute
	}
	return def
}
