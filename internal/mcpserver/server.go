// Package mcpserver assembles the MCP server over the official Go SDK.
//
// Everything the server needs arrives through Deps. There is no package-level
// mutable state and nothing is read from a global: a nil policy or a nil principal
// resolver is a construction failure, not something quietly defaulted.
//
// The package owns three things a tool handler therefore does not have to:
//
//   - The middleware chain. Principal resolution, logging, rate limiting, the
//     policy gate, and destructive confirmation are applied centrally, once, so a
//     handler that forgets to check something cannot exist.
//   - The registration contract. Later slices implement ToolRegistrar and call
//     AddTool; this package decides what a valid tool looks like.
//   - The stdio transport, where stdout is reserved exclusively for MCP frames.
//
// No Garmin tool is registered here. One built-in tool, server_info, exists so the
// server is exercisable end to end today.
package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

// ProtocolVersion is the MCP specification version this server is built against,
// pinned by ADR 0002 alongside SDK v1.7.0.
//
// The SDK negotiates older versions on its own; this constant is what the server
// reports about itself, not a restriction on what it will speak.
const ProtocolVersion = "2026-07-28"

// DefaultConfirmationTimeout bounds how long a destructive tool waits for the user
// to confirm. When it elapses the operation is refused, never performed.
const DefaultConfirmationTimeout = 60 * time.Second

// Info is the server's advertised identity.
type Info struct {
	// Name is the programmatic server name and is required.
	Name string

	// Title is the optional human-readable name.
	Title string

	// Version is the build version and is required.
	Version string
}

func (i Info) validate() error {
	if strings.TrimSpace(i.Name) == "" {
		return fmt.Errorf("server name is empty: %w", ErrInvalidInfo)
	}
	if strings.TrimSpace(i.Version) == "" {
		return fmt.Errorf("server version is empty: %w", ErrInvalidInfo)
	}
	return nil
}

// Deps is the injected dependency set.
//
// Policy and Principals are required. Logger and Limiter may be nil, because both
// have a defined no-op behavior — a nil logger records nothing, a nil limiter
// passes every call through — and neither absence weakens a security boundary.
// Confirmer may be nil, which is the production case: the server then asks the
// client over MCP elicitation, and a client that cannot be asked causes a refusal.
type Deps struct {
	// Info is the advertised server identity.
	Info Info

	// Logger is the structured logging seam. A nil logger records nothing.
	Logger *mcplog.Logger

	// Policy is the tier and scope gate. Required.
	Policy *policy.Policy

	// Limiter is the per-principal rate limiter. A nil limiter passes through.
	Limiter *ratelimit.Limiter

	// Principals resolves the principal for each request. Required.
	Principals identity.Resolver

	// Registrars contribute tools. A nil entry is a wiring mistake and is
	// rejected rather than skipped.
	Registrars []ToolRegistrar

	// Confirmer overrides how destructive confirmation is obtained. Leave it nil
	// in production to use MCP elicitation over the calling client's session.
	Confirmer policy.Confirmer

	// ConfirmationTimeout bounds the confirmation wait. Zero means
	// DefaultConfirmationTimeout.
	ConfirmationTimeout time.Duration

	// Instructions are optional MCP server instructions for connected clients.
	Instructions string

	// Clock supplies the current time for latency measurement. Zero means
	// time.Now.
	Clock func() time.Time

	// SafetyDelay pauses a write or destructive call after every gate has allowed
	// it and before its handler runs. Zero disables the pause.
	SafetyDelay time.Duration

	// Sleep waits for a duration and reports why it stopped. Nil means a real,
	// context-aware wait. It is the seam that keeps the tests instant.
	Sleep func(ctx context.Context, d time.Duration) error
}

func (d Deps) validate() error {
	if err := d.Info.validate(); err != nil {
		return err
	}
	if d.Policy == nil {
		return fmt.Errorf("policy is nil: %w", ErrMissingDependency)
	}
	if d.Principals == nil {
		return fmt.Errorf("principal resolver is nil: %w", ErrMissingDependency)
	}
	for i, registrar := range d.Registrars {
		if registrar == nil {
			return fmt.Errorf("registrar %d is nil: %w", i, ErrMissingDependency)
		}
	}
	return nil
}

// A Server is an assembled MCP server: an SDK server plus its registry, its
// middleware chain, and the dependencies both were built from.
type Server struct {
	mcpServer *mcp.Server
	registry  *Registry
	deps      Deps
	clock     func() time.Time
	// requestIDs numbers requests within this process so a log line can be
	// correlated with one call. The SDK does not expose the JSON-RPC request id to
	// middleware at v1.7.0, so the id is server-assigned.
	requestIDs atomic.Uint64
}

// New validates deps, registers the built-in tool and every registrar's tools,
// checks the policy against what was actually registered, and installs the
// middleware chain.
//
// Validating the policy against the registry is what turns a typo in a tier list
// into a start-up failure. It runs after registration, because the registered set
// does not exist before then.
func New(deps Deps) (*Server, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}

	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}

	mcpServer := newSDKServer(deps)
	server := &Server{
		mcpServer: mcpServer,
		registry:  newRegistry(mcpServer),
		deps:      deps,
		clock:     clock,
	}

	if err := server.registerTools(); err != nil {
		return nil, err
	}
	if err := deps.Policy.Validate(server.registry.Names()); err != nil {
		return nil, fmt.Errorf("validating the tool policy against the registered tools: %w", err)
	}

	server.installMiddleware()

	deps.Logger.Lifecycle(mcplog.LifecycleEvent{
		Phase:           "startup",
		Mode:            deps.Policy.Mode().String(),
		ProtocolVersion: ProtocolVersion,
		ToolCount:       server.registry.Len(),
	})

	return server, nil
}

// newSDKServer builds the underlying SDK server.
//
// Capabilities is set to an empty value deliberately. The SDK's historical default
// advertises the `logging` capability, which SEP-2577 deprecates as of protocol
// 2026-07-28 and which ADR 0002 forbids building on. The tools capability is still
// inferred when tools are added.
//
// ServerOptions.Logger stays nil: the SDK would otherwise log its own activity
// outside mcplog's redaction rules and its closed field set, and under stdio a
// misconfigured SDK logger is exactly how stdout gets corrupted.
func newSDKServer(deps Deps) *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{
		Name:    deps.Info.Name,
		Title:   deps.Info.Title,
		Version: deps.Info.Version,
	}, &mcp.ServerOptions{
		Instructions: deps.Instructions,
		Capabilities: &mcp.ServerCapabilities{},
	})
}

// registerTools adds the built-in tool first, then every registrar in order.
func (s *Server) registerTools() error {
	if err := registerServerInfo(s.registry, s); err != nil {
		return fmt.Errorf("registering the built-in server info tool: %w", err)
	}
	for i, registrar := range s.deps.Registrars {
		if err := registrar.RegisterTools(s.registry); err != nil {
			return fmt.Errorf("registrar %d: %w", i, err)
		}
	}
	return nil
}

// MCPServer returns the underlying SDK server, so a caller can connect a transport
// this package does not own — the Streamable HTTP handler, for example.
func (s *Server) MCPServer() *mcp.Server { return s.mcpServer }

// Registry returns the tool registry, for contract and snapshot tests.
func (s *Server) Registry() *Registry { return s.registry }

// ToolNames returns the registered tool names, sorted, as a fresh slice.
func (s *Server) ToolNames() []string { return s.registry.Names() }

// nextRequestID returns a fresh per-process correlation id.
func (s *Server) nextRequestID() string {
	return "req-" + strconv.FormatUint(s.requestIDs.Add(1), 10)
}
