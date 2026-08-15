//go:build garminlive

package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// The gate environment variables. All three must be set, on top of the garminlive
// build tag, before one request is dispatched.
const (
	envUsername = "GARMIN_USERNAME"
	envPassword = "GARMIN_PASSWORD"
	envAck      = "GARMIN_LIVE_ACK"

	// envMFACode carries a one-time code for an account that challenges the login.
	// It is optional: without it an MFA challenge skips the suite rather than
	// hanging on a prompt no test can answer.
	envMFACode = "GARMIN_LIVE_MFA_CODE"
)

// ackValue is the exact value envAck must carry. It is spelled out rather than
// truthy, so no stray "1" in an environment can start live traffic by accident.
const ackValue = "i-accept-live-garmin-traffic"

// livePrincipal is the synthetic account key this suite stores its token set under.
// It is not an account selector: the credentials decide whose account is reached,
// and this is only the key of the temporary store record.
const livePrincipal = "garminlive-suite"

// Pacing and bounds. This suite is a guest on an unofficial private API: requests
// are serial, spaced, bounded in number, and given a generous but finite deadline.
const (
	requestPause   = 400 * time.Millisecond
	requestTimeout = 90 * time.Second
	dialTimeout    = 15 * time.Second
	keyVersion     = 1

	// maxFITCandidates bounds how many recent activities the FIT cross-check may
	// download before it gives up looking for one it can analyse.
	maxFITCandidates = 3
)

// stateDir is the temporary directory every piece of state lives in. It is created
// by TestMain and removed when the suite ends, so this suite never reads or writes
// the maintainer's own token store, key or configuration.
var stateDir string

// closers releases what the shared session opened. TestMain runs them after the
// tests, because the session outlives every individual test by design: one login is
// shared by the whole suite rather than repeated per test.
var closers []func()

// shared builds the one authenticated session the suite uses.
var shared = sync.OnceValues(buildEnv)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "garmin-mcp-live")
	if err != nil {
		fmt.Fprintln(os.Stderr, "live: creating the temporary state directory:", err)
		os.Exit(1)
	}
	// internal/securefile refuses a path reached through a symlink, and the
	// platform temporary directory is one on macOS, so the directory is resolved
	// before any secret file is installed under it.
	stateDir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "live: resolving the temporary state directory:", err)
		os.Exit(1)
	}

	code := m.Run()

	for _, release := range closers {
		release()
	}
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, "live: removing the temporary state directory:", err)
	}
	os.Exit(code)
}

// gate reports why the suite must not run, or "" when every gate is open.
func gate() string {
	if os.Getenv(envAck) != ackValue {
		return fmt.Sprintf(
			"not run — acknowledgement absent: set %s=%s to allow this suite to contact the real Garmin service",
			envAck, ackValue)
	}
	if os.Getenv(envUsername) == "" || os.Getenv(envPassword) == "" {
		return fmt.Sprintf(
			"not run — credentials unavailable: set %s and %s for a dedicated non-primary Garmin account",
			envUsername, envPassword)
	}
	return ""
}

// env is the one authenticated live session, plus everything built on it.
type env struct {
	// skip is the reason the suite must not run. Every other field is unset when
	// it is non-empty.
	skip string

	// strategy names the login strategy that succeeded. It is the drift signal: a
	// fallback that used to be unnecessary becoming necessary is visible here.
	strategy string

	session client.Session
	mcp     *mcp.ClientSession

	activities *api.Activities
	details    *api.ActivityDetails
	files      *api.ActivityFiles
	profile    *api.Profile
	devices    *api.Devices
}

// liveEnv returns the shared session, skipping the calling test when a gate is shut.
func liveEnv(t *testing.T) *env {
	t.Helper()

	e, err := shared()
	if err != nil {
		t.Fatalf("live: preparing the authenticated session: %v", err)
	}
	if e.skip != "" {
		t.Skip(e.skip)
	}
	return e
}

// readOnlyCaller is what makes this suite read-only by construction rather than by
// convention. Every domain client and every tool reaches Garmin through it, and it
// refuses anything that is not a read, so a write or destructive tool cannot mutate
// the account even if some future test called one.
//
// One exception is unavoidable and is deliberately narrow: Garmin serves the workout
// calendar from a GraphQL gateway, and a GraphQL query is a POST. The guard therefore
// allows a POST to that one path and to nothing else. Every mutating REST call this
// server can make targets a different path, so no write can pass.
//
// It wraps the caller only. The login and the token refresh use their own transport
// and legitimately POST, which is why the guard sits here and not on the HTTP client.
type readOnlyCaller struct {
	inner client.Caller
}

func (c readOnlyCaller) Do(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	if !isReadRequest(req) {
		return nil, fmt.Errorf("live: refusing a %s request to %s: this suite is read-only",
			req.Method, req.URL.Path)
	}
	return c.inner.Do(ctx, principal, req)
}

// isReadRequest reports whether one request only reads.
func isReadRequest(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead:
		return true
	case http.MethodPost:
		return req.URL != nil && req.URL.Path == client.PathGraphQL
	default:
		return false
	}
}

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

	e := &env{strategy: strategy, session: session}
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
// paces itself, so a whole-surface sweep does not arrive at Garmin as a burst.
func (e *env) call(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()

	result := e.rawCall(t, name, args)
	if result.IsError {
		t.Fatalf("%s returned an error result: %s", name, resultText(result))
	}
	if result.StructuredContent == nil {
		t.Fatalf("%s returned no structured content", name)
	}

	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshalling the %s result: %v", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decoding the %s result: %v", name, err)
	}
	return out
}

// rawCall invokes one tool and returns whatever came back, an error result included.
func (e *env) rawCall(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	if args == nil {
		args = map[string]any{}
	}
	time.Sleep(requestPause)

	ctx, cancel := context.WithTimeout(t.Context(), 4*requestTimeout)
	defer cancel()

	result, err := e.mcp.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) transport error: %v", name, err)
	}
	return result
}

// resultText joins the textual content of a result. It is used only for a refusal,
// whose message is an authored remediation rather than a payload.
func resultText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	return text.String()
}
