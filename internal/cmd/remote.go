package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/oauthstore"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// resourceName is the human-readable name of this protected resource, as it
// appears in the RFC 9728 document.
const resourceName = "Garmin Connect MCP"

// remoteInstructions are the MCP server instructions a remote client receives.
// They state the two facts a client cannot infer: the API is unofficial, and each
// caller sees only the Garmin account its own authorization linked.
const remoteInstructions = "Garmin Connect data for the account this authorization linked. " +
	"Garmin's API is unofficial and undocumented, so a call can fail or change shape " +
	"without notice. The account is bound to the access token: no tool accepts a user, " +
	"an email, or a token path."

// A remoteDeployment is the assembled multi-user server.
//
// It is built once, by [newRemoteDeployment], and not mutated afterwards. It owns
// the database handle, so it is the thing a caller closes; everything else it
// holds is either immutable or owns its own bounded state.
type remoteDeployment struct {
	cfg  config.Config
	deps *dependencies

	sqlite    *store.SQLiteStore
	clients   *configClients
	oauth     *oauthserver.Server
	transport *mcpserver.HTTPTransport
	login     *loginweb.RemoteServer

	// revocations carries what the store revoked to the transport, which closes
	// the sessions it covers.
	revocations *revocationBus

	endpoints remoteEndpoints
	handler   http.Handler
}

// newRemoteDeployment assembles the remote server from an already-validated
// configuration.
//
// The order is the security order. The published URLs are derived and refused
// first, so a cleartext or self-conflicting deployment fails before a key file is
// read or a database is created. The authorization server comes next, because the
// principal resolver is built from it and there is no other source of a principal
// here. The MCP graph comes last, and it is the same graph the stdio path builds:
// the same tool set, the same tier policy, and the same single shared token gate.
func newRemoteDeployment(
	ctx context.Context, cfg config.Config, w *wiring,
) (*remoteDeployment, error) {
	endpoints, err := newRemoteEndpoints(cfg.PublicURL)
	if err != nil {
		return nil, err
	}
	paths, err := resolveStatePaths(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.MasterKey.IsSet() {
		return nil, fmt.Errorf("%w: inline master key material is not supported; "+
			"supply the key through master-key-file", ErrUnsupportedKeyMaterial)
	}
	clients, err := newConfigClients(cfg)
	if err != nil {
		return nil, err
	}

	revocations := newRevocationBus()
	sqlite, err := openSQLiteStore(ctx, cfg, paths, revocations)
	if err != nil {
		return nil, err
	}
	if err := reconcileConfiguredClients(ctx, sqlite, cfg); err != nil {
		return nil, errors.Join(err, sqlite.Close())
	}

	remote, err := assembleRemote(cfg, w, paths, remoteParts{
		endpoints:   endpoints,
		clients:     clients,
		sqlite:      sqlite,
		revocations: revocations,
	})
	if err != nil {
		return nil, errors.Join(err, sqlite.Close())
	}
	return remote, nil
}

// openSQLiteStore reads the key material and opens the migrated database.
//
// Key material is read, or created once, through internal/cryptostore, which
// refuses material that is malformed, group- or world-readable, or reached through
// a symlink. Those refusals are start-up failures on purpose: a deployment that
// cannot protect its key must not serve.
// The revocation sink is supplied here rather than attached later, because a store
// that could serve before it had one would revoke silently for exactly as long as
// the gap lasted.
func openSQLiteStore(
	ctx context.Context, cfg config.Config, paths statePaths, revocations store.RevocationSink,
) (*store.SQLiteStore, error) {
	key, err := cryptostore.LoadOrCreateKey(paths.keys, keyVersion)
	if err != nil {
		return nil, fmt.Errorf("opening the encryption key: %w", err)
	}

	sqlite, err := store.OpenSQLite(ctx, store.SQLiteConfig{
		Path:        cfg.DatabasePath,
		Key:         key,
		Revocations: revocations,
	})
	if err != nil {
		return nil, fmt.Errorf("opening the multi-user store: %w", err)
	}
	return sqlite, nil
}

// remoteParts are the pieces newRemoteDeployment built before the graph.
type remoteParts struct {
	endpoints   remoteEndpoints
	clients     *configClients
	sqlite      *store.SQLiteStore
	revocations *revocationBus
}

// assembleRemote builds everything that depends on the open store. It is separate
// so its caller can close that store on any failure along the way.
func assembleRemote(
	cfg config.Config, w *wiring, paths statePaths, parts remoteParts,
) (*remoteDeployment, error) {
	oauth, err := newAuthorizationServer(parts)
	if err != nil {
		return nil, err
	}
	authorizer, err := mcpserver.NewOAuthAuthorizer(oauth)
	if err != nil {
		return nil, fmt.Errorf("building the protected-resource authorizer: %w", err)
	}
	deps, err := newRemoteGraph(cfg, w, paths, parts.sqlite, authorizer)
	if err != nil {
		return nil, err
	}

	server, err := mcpserver.New(deps.serverDeps(remoteInstructions))
	if err != nil {
		return nil, fmt.Errorf("assembling the MCP server: %w", err)
	}
	transport, err := mcpserver.NewHTTPTransport(server,
		httpOptions(cfg, authorizer, parts.revocations))
	if err != nil {
		return nil, fmt.Errorf("building the Streamable HTTP transport: %w", err)
	}
	login, err := newRemoteLoginServer(deps, oauth, parts)
	if err != nil {
		return nil, err
	}

	remote := &remoteDeployment{
		cfg:         cfg,
		deps:        deps,
		sqlite:      parts.sqlite,
		clients:     parts.clients,
		oauth:       oauth,
		transport:   transport,
		login:       login,
		revocations: parts.revocations,
		endpoints:   parts.endpoints,
	}
	remote.handler = remote.mount()
	return remote, nil
}

// newAuthorizationServer builds the OAuth authorization and protected-resource
// server over the storage adapter.
//
// The advertised scope set is the union of what the registered clients may hold,
// so the deployment bound and the registration bound cannot contradict each other:
// a client whose registration exceeded the advertisement would be refused at every
// authorization request, which is a start-up mistake reported at run time.
func newAuthorizationServer(parts remoteParts) (*oauthserver.Server, error) {
	adapter, err := oauthstore.New(parts.sqlite, parts.clients)
	if err != nil {
		return nil, fmt.Errorf("adapting the store for the authorization server: %w", err)
	}

	server, err := oauthserver.New(oauthserver.Config{
		Issuer:                parts.endpoints.issuer,
		Resource:              parts.endpoints.resource,
		AuthorizationEndpoint: parts.endpoints.authorization,
		TokenEndpoint:         parts.endpoints.token,
		RevocationEndpoint:    parts.endpoints.revocation,
		ResourceMetadataURL:   parts.endpoints.resourceMetadata,
		ResourceName:          resourceName,
		ScopesSupported:       parts.clients.Scopes(),
	}, oauthserver.Deps{Store: adapter})
	if err != nil {
		return nil, fmt.Errorf("building the authorization server: %w", err)
	}
	return server, nil
}

// newRemoteGraph builds the shared dependency graph for the remote shape.
//
// The principal is resolved from the request's verified bearer token and from
// nothing else, and the scope source reads the same verified grant. Neither can be
// steered by a tool argument, a header, or a session identifier: both take a
// context and nothing else.
func newRemoteGraph(
	cfg config.Config, w *wiring, paths statePaths,
	sqlite *store.SQLiteStore, authorizer *mcpserver.OAuthAuthorizer,
) (*dependencies, error) {
	sqliteTokens, err := newSQLiteTokens(sqlite)
	if err != nil {
		return nil, err
	}
	// The staging wrapper is the store both the authenticator and the refresher
	// receive, so they still share one store and one gate. It changes nothing for
	// a principal that exists; it exists so a login can produce a token set before
	// its principal does.
	staging, err := newStagedTokens(sqliteTokens, maxPendingLogins, pendingLoginTTL, nil)
	if err != nil {
		return nil, err
	}
	principals, err := identity.NewBearerResolver(mcpserver.PrincipalSource(authorizer))
	if err != nil {
		return nil, fmt.Errorf("building the bearer principal resolver: %w", err)
	}
	scopes, err := newGrantedScopes(authorizer)
	if err != nil {
		return nil, err
	}

	return newGraph(cfg, w, paths, shape{
		mode:       policy.ModeRemote,
		principals: principals,
		tokens:     staging,
		staging:    staging,
		scopes:     scopes,
	})
}

// httpOptions projects the configuration onto the transport's options.
//
// The transport deliberately does not listen: the listener and its TLS material
// belong to the composition root. It still needs the bind address, because that is
// what lets it refuse a cleartext public bind, and refusing that is not a decision
// a caller should be able to skip by forgetting to pass it.
func httpOptions(cfg config.Config, authorizer *mcpserver.OAuthAuthorizer,
	revocations mcpserver.RevocationSource,
) mcpserver.HTTPOptions {
	return mcpserver.HTTPOptions{
		Revocations:            revocations,
		PublicURL:              cfg.PublicURL,
		BindAddress:            cfg.BindAddress,
		Authorizer:             authorizer,
		AllowedOrigins:         cfg.AllowedOrigins,
		TrustedProxyCIDRs:      cfg.TrustedProxyCIDRs,
		AllowInsecureCleartext: cfg.AllowInsecureHTTP,
		SessionTimeout:         cfg.SessionTimeout,
		MaxRequestBodyBytes:    cfg.MaxRequestBytes,
	}
}

// newRemoteLoginServer builds the transaction-gated browser login.
//
// The pages see two adapters and nothing else: one that drives the authorization
// server, and one that runs a Garmin login for the account the credentials name.
// No page, no form, and no route can select an account.
func newRemoteLoginServer(
	deps *dependencies, oauth *oauthserver.Server, parts remoteParts,
) (*loginweb.RemoteServer, error) {
	grants, err := newAuthorizations(oauth, parts.clients)
	if err != nil {
		return nil, err
	}
	logins, err := newRemoteLogin(remoteLoginDeps{
		authenticator: deps.authenticator,
		directory:     parts.sqlite,
		tokens:        deps.tokens,
		staging:       deps.staging,
		gate:          deps.tokenGate,
	})
	if err != nil {
		return nil, err
	}

	login, err := loginweb.NewRemote(loginweb.RemoteConfig{
		Authorizations: grants,
		Authenticator:  logins,
		Logger:         deps.events,
	})
	if err != nil {
		return nil, fmt.Errorf("building the browser login server: %w", err)
	}
	return login, nil
}

// mount routes the deployment.
//
// Every path is exact except the login subtree, which carries the forms and the
// stylesheet. The MCP endpoint and the protected resource metadata document go to
// the transport, which authenticates the first and serves the second; the two
// remaining OAuth endpoints go to the authorization server; everything a browser
// reaches goes to the login profile, which applies its own security headers and
// answers a generic 404 for anything it does not recognize.
func (r *remoteDeployment) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(r.endpoints.mcpPath, r.transport)
	mux.Handle(mcpserver.DefaultResourceMetadataPath, r.transport)
	mux.Handle(authServerMetadataPath, r.oauth.AuthorizationServerMetadataHandler())
	mux.Handle(tokenPath, r.oauth.TokenHandler())
	mux.Handle(revocationPath, r.oauth.RevocationHandler())

	loginHandler := r.login.Handler()
	mux.Handle(loginweb.RemoteAuthorizePath, loginHandler)
	mux.Handle(loginPath, loginHandler)
	mux.Handle(loginSubtreePath, loginHandler)
	return mux
}

// close releases what the deployment opened. It is safe on a partially built
// value, so a caller can defer it as soon as it has one.
func (r *remoteDeployment) close() error {
	if r == nil {
		return nil
	}
	r.deps.close()
	if r.sqlite == nil {
		return nil
	}
	return r.sqlite.Close()
}
