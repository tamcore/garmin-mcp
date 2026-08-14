package mcpserver

import (
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// Annotations are the four MCP tool hints, each declared explicitly.
//
// The SDK's mcp.ToolAnnotations mixes plain bools and pointers at v1.7.0:
// ReadOnlyHint and IdempotentHint serialize even when false, while DestructiveHint
// and OpenWorldHint are *bool and vanish when nil. Declaring all four as plain
// bools here and converting on the way out means a tool cannot accidentally omit a
// hint by leaving a pointer unset.
type Annotations struct {
	// ReadOnly must be true for, and only for, the read-only tier.
	ReadOnly bool

	// Destructive must be true for, and only for, the destructive tier.
	Destructive bool

	// Idempotent reports that repeating the call with the same arguments has no
	// further effect. It is meaningful only when ReadOnly is false, but is
	// declared always.
	Idempotent bool

	// OpenWorld must be true for every tool here: Garmin is an unofficial,
	// undocumented external API whose responses this server does not control.
	OpenWorld bool
}

// toSDK converts to the SDK shape, setting both pointer hints explicitly so
// neither is omitted from the wire.
func (a Annotations) toSDK(title string) *mcp.ToolAnnotations {
	destructive, openWorld := a.Destructive, a.OpenWorld
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    a.ReadOnly,
		IdempotentHint:  a.Idempotent,
		DestructiveHint: &destructive,
		OpenWorldHint:   &openWorld,
	}
}

// A ToolSpec is everything the server needs to know about one tool besides its
// handler.
//
// Category is the coarse domain label the logger records in place of the exact
// tool name, so it must never itself be a tool name.
type ToolSpec struct {
	// Name is the wire name. It is the compatibility name from the pinned
	// upstream unless a documented security reason says otherwise.
	Name string

	// Title is the optional human-readable display name.
	Title string

	// Description is the model-facing description and is required.
	Description string

	// Tier is the policy tier and must be declared explicitly.
	Tier policy.Tier

	// Category is the coarse log category, for example "activities" or
	// "womens-health".
	Category string

	// Annotations are the four MCP hints.
	Annotations Annotations
}

// validate checks the spec on its own terms and against its tier.
func (s ToolSpec) validate() error {
	switch {
	case strings.TrimSpace(s.Name) == "" || s.Name != strings.TrimSpace(s.Name):
		return fmt.Errorf("tool name %q: %w", s.Name, ErrInvalidToolSpec)
	case strings.TrimSpace(s.Description) == "":
		return fmt.Errorf("tool %q has no description: %w", s.Name, ErrInvalidToolSpec)
	case strings.TrimSpace(s.Category) == "":
		return fmt.Errorf("tool %q has no log category: %w", s.Name, ErrInvalidToolSpec)
	case !s.Tier.IsValid():
		return fmt.Errorf("tool %q declares no tier: %w", s.Name, ErrInvalidToolSpec)
	}
	return s.validateAnnotations()
}

// validateAnnotations refuses hints that contradict the tier. A wrong hint is not
// cosmetic: a client decides whether to prompt its user based on it.
func (s ToolSpec) validateAnnotations() error {
	if !s.Annotations.OpenWorld {
		return fmt.Errorf("tool %q claims a closed world, but Garmin is an open-world API: %w",
			s.Name, ErrAnnotationMismatch)
	}
	if wantReadOnly := s.Tier == policy.TierReadOnly; s.Annotations.ReadOnly != wantReadOnly {
		return fmt.Errorf("tool %q is in the %s tier but sets ReadOnly=%t: %w",
			s.Name, s.Tier, s.Annotations.ReadOnly, ErrAnnotationMismatch)
	}
	if wantDestructive := s.Tier == policy.TierDestructive; s.Annotations.Destructive != wantDestructive {
		return fmt.Errorf("tool %q is in the %s tier but sets Destructive=%t: %w",
			s.Name, s.Tier, s.Annotations.Destructive, ErrAnnotationMismatch)
	}
	return nil
}

// A ToolRegistrar registers tools with the server.
//
// This is the interface later slices target. A slice implements RegisterTools and
// calls the package-level AddTool once per tool, which keeps the one-tool-per-file
// layout while leaving a single place — this package — that owns the middleware,
// the policy gate, and the annotation rules.
type ToolRegistrar interface {
	RegisterTools(registry *Registry) error
}

// A Registry records the tools registered on one server.
//
// It is not safe for concurrent use and does not need to be: registration happens
// once, inside New, before any transport is connected.
type Registry struct {
	server *mcp.Server
	specs  map[string]ToolSpec
}

func newRegistry(server *mcp.Server) *Registry {
	return &Registry{server: server, specs: make(map[string]ToolSpec)}
}

// Names returns the registered tool names, sorted, as a fresh slice.
//
// The sort makes start-up validation and the schema snapshot tests deterministic;
// the copy stops a caller reaching into the registry.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.specs))
	for name := range r.specs {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Spec returns the recorded spec for name, and whether it exists.
func (r *Registry) Spec(name string) (ToolSpec, bool) {
	spec, ok := r.specs[name]
	return spec, ok
}

// Len reports how many tools are registered.
func (r *Registry) Len() int { return len(r.specs) }

// AddTool registers one tool and records its spec.
//
// It is a package-level generic function rather than a method because Go has no
// generic methods, and the SDK's typed registration path — mcp.AddTool[In, Out] —
// is what infers and validates the input and output schemas. This is the exact
// entry point later slices call:
//
//	func (g *garminTools) RegisterTools(r *mcpserver.Registry) error {
//	    return mcpserver.AddTool(r, spec, handler)
//	}
//
// The handler itself needs no policy, rate-limit, or logging code: everything is
// applied centrally by the middleware chain New installs.
func AddTool[In, Out any](registry *Registry, spec ToolSpec, handler mcp.ToolHandlerFor[In, Out]) error {
	if registry == nil {
		return fmt.Errorf("registry is nil: %w", ErrMissingDependency)
	}
	if handler == nil {
		return fmt.Errorf("tool %q has no handler: %w", spec.Name, ErrInvalidToolSpec)
	}
	if err := spec.validate(); err != nil {
		return err
	}
	if _, exists := registry.specs[spec.Name]; exists {
		return fmt.Errorf("tool %q is already registered: %w", spec.Name, ErrDuplicateTool)
	}

	mcp.AddTool(registry.server, &mcp.Tool{
		Name:        spec.Name,
		Title:       spec.Title,
		Description: spec.Description,
		Annotations: spec.Annotations.toSDK(spec.Title),
	}, handler)
	registry.specs[spec.Name] = spec
	return nil
}
