package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// The synthetic remote deployment every test in this file starts from. None of
// these values reaches a network: the transport is an http.Handler, and the tests
// drive it through httptest rather than through a listener.
const (
	remotePublicURL   = "https://mcp.example.test/mcp"
	remoteBindAddress = "127.0.0.1:8443"
	remoteClientName  = "Example MCP client"
	remoteRedirectURI = "https://client.example.test/callback"
	remoteScope       = "garmin:read"
)

// remoteConfig returns a validated streamable-http configuration rooted at a
// private, symlink-free state directory.
func remoteConfig(t *testing.T) config.Config {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}

	cfg := config.Default()
	cfg.Transport = config.TransportStreamableHTTP
	cfg.StateDir = dir
	cfg.DatabasePath = filepath.Join(dir, "state.db")
	cfg.MasterKeyPath = filepath.Join(dir, "keys", "key-v1.json")
	cfg.PublicURL = remotePublicURL
	cfg.BindAddress = remoteBindAddress
	cfg.OAuthClients = []config.OAuthClient{{
		ID:           registerDatabaseClient(t, cfg),
		Name:         remoteClientName,
		RedirectURIs: []string{remoteRedirectURI},
		Scopes:       []string{remoteScope},
		Resources:    []string{remotePublicURL},
		Public:       true,
	}}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("the test configuration does not validate: %v", err)
	}
	return cfg
}

// registerDatabaseClient performs the half of a client registration the database
// owns, and returns the identifier it assigned.
//
// A registration lives in two places: the database holds the identity and the
// exact redirect URIs, and configuration holds the OAuth policy — the scope bound,
// the resource indicators, and the secret digest — which the database has no
// column for. The store assigns the identifier, so the configured client id is the
// one it returns here rather than a name an operator invents.
func registerDatabaseClient(t *testing.T, cfg config.Config) string {
	t.Helper()

	key, err := cryptostore.LoadOrCreateKey(filepath.Join(cfg.StateDir, "keys"), 1)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	sqlite, err := store.OpenSQLite(t.Context(),
		store.SQLiteConfig{Path: cfg.DatabasePath, Key: key})
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer func() {
		if err := sqlite.Close(); err != nil {
			t.Errorf("closing the bootstrap store: %v", err)
		}
	}()

	client, err := sqlite.RegisterClient(t.Context(), store.ClientRegistration{
		Name:         remoteClientName,
		RedirectURIs: []string{remoteRedirectURI},
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	return client.ID
}

func buildRemote(t *testing.T, cfg config.Config) *remoteDeployment {
	t.Helper()

	// The logs are discarded rather than left on standard error: a test that
	// printed lifecycle records would bury the failures it exists to report.
	remote, err := newRemoteDeployment(t.Context(), cfg, &wiring{Logs: io.Discard})
	if err != nil {
		t.Fatalf("newRemoteDeployment returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := remote.close(); err != nil {
			t.Errorf("close returned error: %v", err)
		}
	})
	return remote
}

// TestRemoteSharesOneTokenGateBetweenLoginAndRefresh is the same wiring guarantee
// the stdio path has, asserted on the remote graph: a browser login and a
// background refresh both end in a compare-and-set write of one principal's DI
// token set, and two private gates would let a login overwrite a token set a
// concurrent refresh had just rotated.
func TestRemoteSharesOneTokenGateBetweenLoginAndRefresh(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))

	login := remote.deps.tokenConfigs.login.TokenGate
	refresh := remote.deps.tokenConfigs.refresh.TokenGate

	if login == nil {
		t.Fatal("auth.Config.TokenGate is nil: the Authenticator built a private gate")
	}
	if refresh == nil {
		t.Fatal("auth.RefreshConfig.TokenGate is nil: the Refresher built a private gate")
	}
	if login != refresh {
		t.Fatal("the Authenticator and the Refresher hold different token gates, " +
			"so a login can overwrite a token set a concurrent refresh just rotated")
	}
	if remote.deps.tokenConfigs.login.Store != remote.deps.tokenConfigs.refresh.Store {
		t.Error("login and refresh write different token stores, so the gate protects nothing")
	}
}

// TestRemoteAndStdioShareNoState is the mode-isolation guarantee. The two shapes
// are assembled in one process here, which is the only way a shared package-level
// value could show up, and every component they could have shared is compared.
func TestRemoteAndStdioShareNoState(t *testing.T) {
	local := buildDependencies(t, localConfig(t))
	remote := buildRemote(t, remoteConfig(t))

	if local.tokenGate == remote.deps.tokenGate {
		t.Error("the two modes share one token gate")
	}
	if local.tokenConfigs.login.Store == remote.deps.tokenConfigs.login.Store {
		t.Error("the two modes share one Garmin token store")
	}
	if local.policy == remote.deps.policy {
		t.Error("the two modes share one tool policy")
	}
	if local.limiter == remote.deps.limiter {
		t.Error("the two modes share one rate limiter")
	}
	if local.principals == remote.deps.principals {
		t.Error("the two modes share one principal resolver")
	}
	if local.files == nil {
		t.Error("the stdio graph has no file-backed token store")
	}
	if remote.deps.files != nil {
		t.Error("the remote graph opened the single-user file store")
	}
}

// TestRemotePrincipalComesOnlyFromAVerifiedToken proves the remote resolver has no
// process-bound account to fall back on: an unauthenticated context resolves to
// nothing at all, whatever the configured principal identifier says.
func TestRemotePrincipalComesOnlyFromAVerifiedToken(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))

	_, err := remote.deps.principals.Resolve(t.Context())
	if !errors.Is(err, identity.ErrNoPrincipal) {
		t.Errorf("Resolve on an unauthenticated context = %v, want ErrNoPrincipal", err)
	}
}

// TestStdioStillBindsExactlyOneAccount keeps the local rule intact while the
// remote shape exists beside it, including the refusal of an ambiguous
// multi-account configuration.
func TestStdioStillBindsExactlyOneAccount(t *testing.T) {
	cfg := localConfig(t)
	cfg.PrincipalID = "principal-local"
	local := buildDependencies(t, cfg)

	resolved, err := local.principals.Resolve(t.Context())
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.ID() != "principal-local" {
		t.Errorf("principal = %q, want %q", resolved.ID(), "principal-local")
	}

	_, err = identity.NewStdioResolver(identity.StdioConfig{PrincipalIDs: []string{"a", "b"}})
	if !errors.Is(err, identity.ErrAmbiguousPrincipal) {
		t.Errorf("an ambiguous stdio configuration returned %v, want ErrAmbiguousPrincipal", err)
	}
}

// TestRemoteHandlerMountsTheDocumentedRoutes drives the assembled handler
// directly. Each route is asserted by the property that makes it useful, not by
// its body.
func TestRemoteHandlerMountsTheDocumentedRoutes(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))

	tests := []struct {
		name string
		path string
		want int
	}{
		{
			name: "protected resource metadata",
			path: mcpserver.DefaultResourceMetadataPath,
			want: http.StatusOK,
		},
		{
			name: "authorization server metadata",
			path: authServerMetadataPath,
			want: http.StatusOK,
		},
		{
			name: "an unsolicited login page",
			path: "/login",
			want: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			remote.handler.ServeHTTP(recorder,
				httptest.NewRequest(http.MethodGet, "https://mcp.example.test"+tc.path, nil))

			if recorder.Code != tc.want {
				t.Errorf("status = %d, want %d", recorder.Code, tc.want)
			}
		})
	}
}

// TestRemoteMCPEndpointRefusesAnUnauthenticatedCall is the boundary the whole
// remote mode rests on: a principal comes from a verified bearer token and from
// nowhere else, so an unauthenticated MCP request is challenged rather than served.
func TestRemoteMCPEndpointRefusesAnUnauthenticatedCall(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, remotePublicURL,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	request.Header.Set("Content-Type", "application/json")
	remote.handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	challenge := recorder.Header().Get("WWW-Authenticate")
	if !strings.Contains(challenge, "Bearer") {
		t.Errorf("WWW-Authenticate = %q, want a Bearer challenge", challenge)
	}
	if !strings.Contains(challenge, "resource_metadata") {
		t.Errorf("WWW-Authenticate = %q, want the resource_metadata URI", challenge)
	}
}

// TestRemoteRefusesACleartextPublicURL fails closed before anything is served. The
// authorization server issues tokens for the public URL and names it as the
// issuer, and a cleartext issuer publishes every bearer token in plaintext.
func TestRemoteRefusesACleartextPublicURL(t *testing.T) {
	cfg := remoteConfig(t)
	cfg.PublicURL = "http://127.0.0.1:8180/mcp"
	cfg.BindAddress = "127.0.0.1:8180"
	cfg.OAuthClients[0].Resources = []string{cfg.PublicURL}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the loopback configuration does not validate: %v", err)
	}

	remote, err := newRemoteDeployment(t.Context(), cfg, nil)
	if err == nil {
		_ = remote.close()
		t.Fatal("newRemoteDeployment accepted a cleartext public URL")
	}
	if !errors.Is(err, ErrInsecureDeployment) {
		t.Errorf("error %v does not match ErrInsecureDeployment", err)
	}
}

// TestRemoteReadsAConfidentialClientDigestFromItsFile proves the "-file" variant
// is the working path, and that the digest never reaches a rendering.
func TestRemoteReadsAConfidentialClientDigestFromItsFile(t *testing.T) {
	cfg := remoteConfig(t)
	digestPath := filepath.Join(cfg.StateDir, "client.sha256")
	// A well-formed hex SHA-256 that is the digest of nothing in particular.
	const digest = "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8"
	if err := os.WriteFile(digestPath, []byte(digest+"\n"), 0o600); err != nil {
		t.Fatalf("write the digest file: %v", err)
	}

	cfg.OAuthClients[0].Public = false
	cfg.OAuthClients[0].SecretHashPath = digestPath
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the confidential configuration does not validate: %v", err)
	}

	remote := buildRemote(t, cfg)

	client, err := remote.clients.Client(t.Context(), cfg.OAuthClients[0].ID)
	if err != nil {
		t.Fatalf("Client returned error: %v", err)
	}
	if client.IsPublic() {
		t.Error("the client is public, want confidential")
	}
	if rendered := client.String(); strings.Contains(rendered, digest) {
		t.Errorf("the client rendering leaks its digest: %s", rendered)
	}
}

// TestRemoteRegistersAConfiguredClientThatHasNoDatabaseRow is the half of a
// registration configuration cannot hold, written by start-up rather than demanded
// from the operator. An authorization transaction references the database row by
// foreign key, so a client that exists only in configuration could open none, and
// there was no way to create that row under the operator's own identifier.
func TestRemoteRegistersAConfiguredClientThatHasNoDatabaseRow(t *testing.T) {
	cfg := remoteConfig(t)
	cfg.OAuthClients[0].ID = "operator-chosen-client"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the configuration does not validate: %v", err)
	}

	remote := buildRemote(t, cfg)

	client, err := remote.sqlite.ClientByID(t.Context(), "operator-chosen-client")
	if err != nil {
		t.Fatalf("the configured client has no database row after start-up: %v", err)
	}
	if !client.HasRedirectURI(remoteRedirectURI) {
		t.Errorf("the registered redirect uris are %v, want the configured one",
			client.RedirectURIs)
	}
}

// TestRemoteAppliesAChangedRedirectURIList proves configuration stays the source of
// truth across a restart: a URI an operator withdrew stops being a redirect target,
// and one they added starts being one.
func TestRemoteAppliesAChangedRedirectURIList(t *testing.T) {
	cfg := remoteConfig(t)
	buildRemote(t, cfg)

	const movedRedirectURI = "https://client.example.test/moved"
	changed := cfg
	changed.OAuthClients = []config.OAuthClient{cfg.OAuthClients[0]}
	changed.OAuthClients[0].RedirectURIs = []string{movedRedirectURI}
	if err := changed.Validate(); err != nil {
		t.Fatalf("the changed configuration does not validate: %v", err)
	}

	restarted := buildRemote(t, changed)

	client, err := restarted.sqlite.ClientByID(t.Context(), changed.OAuthClients[0].ID)
	if err != nil {
		t.Fatalf("ClientByID: %v", err)
	}
	if !client.HasRedirectURI(movedRedirectURI) {
		t.Errorf("redirect uris are %v, want the newly configured one", client.RedirectURIs)
	}
	if client.HasRedirectURI(remoteRedirectURI) {
		t.Errorf("redirect uris are %v, want the withdrawn one gone", client.RedirectURIs)
	}
}

// TestRemoteRefusesToResurrectADisabledClient is what remains fail-closed about the
// registration: an operator who switched a client off must not have it switched
// back on by a restart, so start-up refuses rather than re-enabling.
func TestRemoteRefusesToResurrectADisabledClient(t *testing.T) {
	cfg := remoteConfig(t)
	first := buildRemote(t, cfg)
	if err := first.sqlite.DisableClient(t.Context(), cfg.OAuthClients[0].ID); err != nil {
		t.Fatalf("DisableClient: %v", err)
	}

	remote, err := newRemoteDeployment(t.Context(), cfg, &wiring{Logs: io.Discard})
	if err == nil {
		_ = remote.close()
		t.Fatal("newRemoteDeployment re-enabled a client an operator had disabled")
	}
	if !errors.Is(err, ErrUnregisteredClient) {
		t.Errorf("error %v does not match ErrUnregisteredClient", err)
	}
	if !errors.Is(err, store.ErrClientDisabled) {
		t.Errorf("error %v does not keep store.ErrClientDisabled reachable", err)
	}
}

// TestRemoteRefusesAnUnknownClient keeps the registry closed: a client exists
// because an operator registered it, never because one asked.
func TestRemoteRefusesAnUnknownClient(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))

	if _, err := remote.clients.Client(t.Context(), "not-registered"); err == nil {
		t.Fatal("the client store answered for an unregistered client")
	}
}
