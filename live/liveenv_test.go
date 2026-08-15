//go:build garminlive

package live

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/store"
	"github.com/tamcore/garmin-mcp/internal/tokenlink"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// This file assembles the one authenticated read session the suite shares: the
// temporary key and token store, the login and refresh layers over one token gate, the
// domain clients, and the real MCP server the read tools are called through. It is
// separated from live_test.go so the suite's own contract — the gates, the shared
// session, the tool call helpers — stays readable next to the assembly that serves it.

// buildEnv opens every gate, logs in once and assembles the clients.
func buildEnv() (*env, error) {
	if skip := gate(); skip != "" {
		return &env{skip: skip}, nil
	}

	logger := slog.New(slog.DiscardHandler)
	hosts, err := protocol.NewHosts(protocol.DomainGlobal)
	if err != nil {
		return nil, fmt.Errorf("building the Garmin hosts: %w", err)
	}

	tokenStore, err := openTemporaryStore()
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DialContext:       (&net.Dialer{Timeout: dialTimeout}).DialContext,
			ForceAttemptHTTP2: true,
		},
	}
	closers = append(closers, httpClient.CloseIdleConnections)

	authenticator, refresher, err := buildAuth(hosts, httpClient, tokenStore, logger)
	if err != nil {
		return nil, err
	}

	strategy, skip, err := login(authenticator)
	if err != nil {
		return nil, err
	}
	if skip != "" {
		return &env{skip: skip}, nil
	}

	rest, err := client.New(client.Config{Hosts: hosts, Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("building the Garmin request layer: %w", err)
	}
	caller := readOnlyCaller{inner: refresher}
	session, err := client.NewSession(caller, livePrincipal)
	if err != nil {
		return nil, fmt.Errorf("building the Garmin session: %w", err)
	}

	e := &env{strategy: strategy, session: session, rest: rest, refresher: refresher}
	if err := e.buildDomainClients(rest); err != nil {
		return nil, err
	}
	if err := e.buildMCPSession(rest, caller); err != nil {
		return nil, err
	}
	return e, nil
}

// openTemporaryStore creates the key and the encrypted token store inside the
// suite's own temporary directory.
func openTemporaryStore() (auth.TokenStore, error) {
	key, err := cryptostore.LoadOrCreateKey(filepath.Join(stateDir, "keys"), keyVersion)
	if err != nil {
		return nil, fmt.Errorf("creating the temporary encryption key: %w", err)
	}
	files, err := store.NewFileStore(store.Config{Dir: filepath.Join(stateDir, "tokens"), Key: key})
	if err != nil {
		return nil, fmt.Errorf("opening the temporary token store: %w", err)
	}
	tokens, err := tokenlink.New(files)
	if err != nil {
		return nil, fmt.Errorf("adapting the temporary token store: %w", err)
	}
	return tokens, nil
}

// buildAuth assembles the login and refresh layers over one shared token gate, the
// way internal/cmd does.
func buildAuth(
	hosts protocol.Hosts, doer *http.Client, tokens auth.TokenStore, logger *slog.Logger,
) (*auth.Authenticator, *auth.Refresher, error) {
	registry, err := auth.NewRegistry(auth.RegistryConfig{})
	if err != nil {
		return nil, nil, fmt.Errorf("building the MFA transaction registry: %w", err)
	}

	gate := auth.NewTokenGate()
	authenticator, err := auth.NewAuthenticator(auth.Config{
		Hosts: hosts, Transport: doer, Store: tokens,
		Registry: registry, TokenGate: gate, Logger: logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("building the Garmin authenticator: %w", err)
	}
	refresher, err := auth.NewRefresher(auth.RefreshConfig{
		Hosts: hosts, Transport: doer, Store: tokens, TokenGate: gate, Logger: logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("building the Garmin token refresher: %w", err)
	}
	return authenticator, refresher, nil
}

// login performs the one real login. It reports the strategy that succeeded, or a
// skip reason for a challenge that cannot be answered without a person.
func login(authenticator *auth.Authenticator) (strategy, skip string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*requestTimeout)
	defer cancel()

	creds := auth.NewCredentials(os.Getenv(envUsername), os.Getenv(envPassword))
	result, err := authenticator.Login(ctx, livePrincipal, creds)
	if err != nil {
		return "", "", fmt.Errorf("live login: %w", err)
	}

	if result.NeedsMFA() {
		code := os.Getenv(envMFACode)
		if code == "" {
			return "", fmt.Sprintf(
				"not run — the account challenged the login for a %s code and %s is unset",
				result.MFAMethod(), envMFACode), nil
		}
		result, err = authenticator.CompleteMFA(ctx, result.TransactionID(), livePrincipal, code)
		if err != nil {
			return "", "", fmt.Errorf("live MFA continuation: %w", err)
		}
	}

	if result.State() != auth.StateAuthenticated {
		return "", "", fmt.Errorf("live login ended in state %q, want authenticated", result.State())
	}
	return result.Strategy().String(), "", nil
}

// buildDomainClients builds the read clients the cross-checks drive directly.
func (e *env) buildDomainClients(rest *client.Client) error {
	var err error
	if e.activities, err = api.NewActivities(rest); err != nil {
		return fmt.Errorf("building the activity client: %w", err)
	}
	if e.details, err = api.NewActivityDetails(rest); err != nil {
		return fmt.Errorf("building the activity detail client: %w", err)
	}
	if e.files, err = api.NewActivityFiles(rest); err != nil {
		return fmt.Errorf("building the download client: %w", err)
	}
	if e.profile, err = api.NewProfile(rest); err != nil {
		return fmt.Errorf("building the profile client: %w", err)
	}
	if e.devices, err = api.NewDevices(rest); err != nil {
		return fmt.Errorf("building the device client: %w", err)
	}
	return nil
}

// buildMCPSession stands up the real server, the real registry and a real MCP client
// over an in-memory transport, so a tool call in this suite takes the same path a
// client's call takes: the middleware chain, the policy, the registry and the
// request layer.
func (e *env) buildMCPSession(rest *client.Client, caller client.Caller) error {
	registrar, err := tools.New(tools.Deps{Client: rest, Caller: caller})
	if err != nil {
		return fmt.Errorf("building the tool registrar: %w", err)
	}
	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{livePrincipal},
	})
	if err != nil {
		return fmt.Errorf("building the principal resolver: %w", err)
	}
	pol, err := policy.New(policy.Config{
		Mode:             policy.ModeLocal,
		ReadOnlyTools:    tools.ReadOnlyTools(),
		WriteTools:       tools.WriteTools(),
		DestructiveTools: tools.DestructiveTools(),
	}, nil)
	if err != nil {
		return fmt.Errorf("building the policy: %w", err)
	}
	server, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-live", Version: "0.0.0-live"},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{registrar},
	})
	if err != nil {
		return fmt.Errorf("building the MCP server: %w", err)
	}
	return e.connect(server)
}

// connect runs the server and attaches one MCP client session to it.
func (e *env) connect(server *mcpserver.Server) error {
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.MCPServer().Run(ctx, serverTransport)
	}()

	session, err := mcp.NewClient(&mcp.Implementation{
		Name: "garmin-mcp-live", Version: "live",
	}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		<-done
		return fmt.Errorf("connecting the live MCP client: %w", err)
	}

	closers = append(closers, func() {
		_ = session.Close()
		cancel()
		<-done
	})
	e.mcp = session
	return nil
}

// call invokes one tool over the MCP session and requires a successful result. It
