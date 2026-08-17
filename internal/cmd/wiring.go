package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
	"github.com/tamcore/garmin-mcp/internal/store"
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

	// mode is the deployment shape the policy is built for.
	mode policy.Mode

	// principal is the single account a local process is bound to. It is the
	// zero value remotely, where a principal is a property of a request rather
	// than of the process.
	principal  identity.Principal
	principals identity.Resolver

	// files is the single-user encrypted file store, and is nil remotely, where
	// the multi-user SQLite store holds the same records.
	files  *store.FileStore
	tokens auth.TokenStore

	// staging holds the token set of a login whose principal does not exist yet.
	// It is nil for stdio, where the account is bound to the process and there is
	// nothing to discover.
	staging *stagedTokens

	// scopes reports what the caller of a request was granted. It is nil for
	// stdio, which grants none.
	scopes policy.ScopeSource

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

// shape is what differs between the two deployment shapes.
//
// Everything else — the loggers, the Garmin layers, the tool set, the policy, the
// limiter — is assembled identically by [newGraph], so the two modes cannot drift
// apart in the parts they are meant to share. Nothing in a shape is a default:
// each field is supplied by the mode's own composition root, which is what keeps a
// remote deployment from silently falling back on a process-bound account.
type shape struct {
	// mode is the deployment shape the policy is built for.
	mode policy.Mode
	// principals resolves the principal of a request.
	principals identity.Resolver
	// tokens persists the per-principal Garmin DI token set.
	tokens auth.TokenStore
	// scopes reports the caller's granted OAuth scopes. Nil grants none.
	scopes policy.ScopeSource
	// staging holds a login's token set until its principal exists. It is set for
	// remote only, where the account is discovered from the credentials.
	staging *stagedTokens
	// files is the single-user file store, set for stdio only.
	files *store.FileStore
	// principal is the bound local account, set for stdio only.
	principal identity.Principal
}

// newDependencies assembles the local stdio graph from an already-validated
// configuration.
//
// The order is deliberate. Key material and the token store come first, because a
// missing key or an unsafe permission must be reported before anything else is
// built. The principal is bound next, so no store is touched for an unresolved
// account.
func newDependencies(cfg config.Config, w *wiring) (*dependencies, error) {
	paths, err := resolveStatePaths(cfg)
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

	return newGraph(cfg, w, paths, shape{
		mode:       policy.ModeLocal,
		principals: principals,
		tokens:     tokens,
		files:      files,
		principal:  principal,
	})
}

// newGraph assembles everything both modes share.
//
// The Authenticator and the Refresher come last, and both are built from one
// [tokenConfigs] value carrying one shared [auth.TokenGate], whichever mode asked
// for the graph.
func newGraph(
	cfg config.Config, w *wiring, paths statePaths, s shape,
) (*dependencies, error) {
	logger, events, err := newLoggers(cfg, w.logs())
	if err != nil {
		return nil, err
	}

	deps := &dependencies{
		cfg:        cfg,
		paths:      paths,
		logger:     logger,
		events:     events,
		mode:       s.mode,
		principal:  s.principal,
		principals: s.principals,
		files:      s.files,
		tokens:     s.tokens,
		staging:    s.staging,
		scopes:     s.scopes,
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
	rest, err := client.New(client.Config{
		Hosts:  hosts,
		Limits: garminLimits(d.cfg),
		Logger: d.events,
	})
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

// close releases what the graph opened. The file store holds no descriptor between
// calls, so only pooled connections need releasing.
func (d *dependencies) close() {
	if d == nil || d.httpClient == nil {
		return
	}
	d.httpClient.CloseIdleConnections()
}

// decompressedHeadroom is the ratio the request layer's own defaults hold between
// the decompressed bound and the wire bound (32 MiB over 8 MiB). Scaling by it
// keeps the relationship the defaults express when an operator moves the wire
// bound, in both directions: raising the wire bound past the default decompressed
// bound would otherwise violate the store's own "decompressed is at least wire"
// invariant and refuse to start, and lowering it would leave the decompressed
// bound untouched, so a deployment hardened by lowering the setting still allowed
// the old amount of memory to be produced.
const decompressedHeadroom = 4

// garminLimits builds the request layer's bounds from configuration.
//
// This existed as a gap rather than a bug for a while: client.New was called with
// no Limits at all, so every bound was the package default. max-response-bytes was
// loaded, flag-exposed, validated, capped, and printed in the redacted config dump
// — and read by nothing. An operator lowering it saw the configured value reported
// back by doctor and the dump while the running server ignored it, which is worse
// than the setting not existing.
//
// Only the settings configuration actually exposes are overridden; everything else
// stays at DefaultLimits, which is what the zero value already meant.
func garminLimits(cfg config.Config) client.Limits {
	limits := client.DefaultLimits()
	limits.RequestTimeout = cfg.RequestTimeout
	limits.MaxResponseBytes = cfg.MaxResponseBytes

	// Scaled, then clamped to the request layer's own cap. Without the clamp a wire
	// bound near its 64 MiB cap would ask for 256 MiB decompressed, exceed the
	// 128 MiB cap, and make Limits.Validate refuse — so raising the setting to its
	// documented maximum would stop the server from starting. The clamp keeps the
	// invariant that matters (decompressed is never below wire) because the
	// decompressed cap is above the wire cap.
	decompressed := min(cfg.MaxResponseBytes*decompressedHeadroom, client.MaxDecompressedBytesCap)
	limits.MaxDecompressedBytes = decompressed
	return limits
}
