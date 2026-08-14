package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
	"github.com/tamcore/garmin-mcp/internal/store"
	"github.com/tamcore/garmin-mcp/internal/tokenlink"
)

// wiring carries what the composition root cannot derive from configuration. A nil
// *wiring is valid and selects the documented defaults.
type wiring struct {
	// Logs receives structured records. Nil selects os.Stderr, which is the only
	// sink the stdio transport permits.
	Logs io.Writer

	// Tools contributes the Garmin tools. Nil registers none, which leaves the
	// server with its built-in tool only.
	Tools ToolFactory

	// Version is the advertised build version. Empty renders as "unknown".
	Version string
}

func (w *wiring) logs() io.Writer {
	if w == nil || w.Logs == nil {
		return os.Stderr
	}
	return w.Logs
}

func (w *wiring) toolFactory() ToolFactory {
	if w == nil {
		return nil
	}
	return w.Tools
}

func (w *wiring) version() string {
	if w == nil {
		return unknownBuildValue
	}
	return orUnknown(w.Version)
}

// tokenConfigs pairs the two configurations that must share one token gate.
//
// They are kept together, and kept on the built dependency set, because sharing the
// gate is a property of the wiring rather than of either component: each config
// falls back to a private gate when its field is nil, so two gates compile and pass
// their own tests while login serializes only against login and refresh only
// against refresh — which is the rotated-token overwrite the gate exists to
// prevent.
type tokenConfigs struct {
	login   auth.Config
	refresh auth.RefreshConfig
}

// dependencies is the assembled dependency graph for one invocation.
//
// It is built once, by [newDependencies], and not mutated afterwards. Nothing here
// is read from a package-level variable: every component is constructed from the
// validated configuration it was given.
type dependencies struct {
	cfg   config.Config
	paths statePaths

	logger *mcplog.Logger
	events *slog.Logger

	principal  identity.Principal
	principals *identity.StdioResolver

	files  *store.FileStore
	tokens *tokenlink.Store

	tokenGate    *auth.TokenGate
	tokenConfigs tokenConfigs

	authenticator *auth.Authenticator
	refresher     *auth.Refresher
	registry      *auth.Registry

	rest  *client.Client
	tools ToolSet

	policy  *policy.Policy
	limiter *ratelimit.Limiter

	httpClient *http.Client
	version    string
}

// newDependencies assembles everything a command needs from an already-validated
// configuration.
//
// The order is deliberate. Key material and the token store come first, because a
// missing key or an unsafe permission must be reported before anything else is
// built. The principal is bound next, so no store is touched for an unresolved
// account. The Authenticator and the Refresher come last, and both are built from
// one [tokenConfigs] value carrying one shared [auth.TokenGate].
func newDependencies(cfg config.Config, w *wiring) (*dependencies, error) {
	paths, err := resolveStatePaths(cfg)
	if err != nil {
		return nil, err
	}
	logger, events, err := newLoggers(cfg, w.logs())
	if err != nil {
		return nil, err
	}
	principal, principals, err := bindPrincipal(cfg)
	if err != nil {
		return nil, err
	}
	files, tokens, err := openTokenStore(cfg, paths)
	if err != nil {
		return nil, err
	}

	deps := &dependencies{
		cfg:        cfg,
		paths:      paths,
		logger:     logger,
		events:     events,
		principal:  principal,
		principals: principals,
		files:      files,
		tokens:     tokens,
		httpClient: newHTTPClient(cfg),
		version:    w.version(),
	}
	if err := deps.buildGarmin(); err != nil {
		return nil, err
	}
	if err := deps.buildToolsAndPolicy(w.toolFactory()); err != nil {
		return nil, err
	}
	if err := deps.buildLimiter(); err != nil {
		return nil, err
	}
	return deps, nil
}

// buildGarmin constructs the login, refresh, and request layers.
//
// This is where the shared token gate is installed. One gate value is created and
// handed to both configurations, so a login and a refresh for the same principal
// queue behind each other instead of racing to a compare-and-set write.
func (d *dependencies) buildGarmin() error {
	hosts := protocol.NewHostsForValidatedDomain(d.cfg.Region)

	registry, err := auth.NewRegistry(auth.RegistryConfig{})
	if err != nil {
		return fmt.Errorf("building the MFA transaction registry: %w", err)
	}

	gate := auth.NewTokenGate()
	configs := tokenConfigs{
		login: auth.Config{
			Hosts:     hosts,
			Transport: d.httpClient,
			Store:     d.tokens,
			Registry:  registry,
			TokenGate: gate,
			Logger:    d.events,
		},
		refresh: auth.RefreshConfig{
			Hosts:     hosts,
			Transport: d.httpClient,
			Store:     d.tokens,
			TokenGate: gate,
			Logger:    d.events,
		},
	}

	authenticator, err := auth.NewAuthenticator(configs.login)
	if err != nil {
		return fmt.Errorf("building the Garmin authenticator: %w", err)
	}
	refresher, err := auth.NewRefresher(configs.refresh)
	if err != nil {
		return fmt.Errorf("building the Garmin token refresher: %w", err)
	}
	rest, err := client.New(client.Config{Hosts: hosts, Logger: d.events})
	if err != nil {
		return fmt.Errorf("building the Garmin request layer: %w", err)
	}

	d.registry = registry
	d.tokenGate = gate
	d.tokenConfigs = configs
	d.authenticator = authenticator
	d.refresher = refresher
	d.rest = rest
	return nil
}

// buildToolsAndPolicy invokes the tool factory and builds the policy from the tier
// lists it reported, intersected with the operator's enablement and name lists.
func (d *dependencies) buildToolsAndPolicy(factory ToolFactory) error {
	set, err := d.contributeTools(factory)
	if err != nil {
		return err
	}

	readOnly, write, destructive := set.tierNames()
	gate, err := policy.New(policy.Config{
		Mode:              policy.ModeLocal,
		ReadOnlyTools:     readOnly,
		WriteTools:        write,
		DestructiveTools:  destructive,
		EnableWrite:       d.cfg.EnableWriteTools,
		EnableDestructive: d.cfg.EnableDestructiveTools,
		Allowlist:         d.cfg.ToolAllowlist,
		Denylist:          d.cfg.ToolDenylist,
	}, nil)
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

// close releases what the graph opened. The file store holds no descriptor between
// calls, so only pooled connections need releasing.
func (d *dependencies) close() {
	if d == nil || d.httpClient == nil {
		return
	}
	d.httpClient.CloseIdleConnections()
}
