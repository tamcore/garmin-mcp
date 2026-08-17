package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// grantWriteOnly stands in for a bearer token that carries garmin:write but not
// garmin:destructive.
type grantWriteOnly struct{}

func (grantWriteOnly) GrantedScopes(context.Context) ([]policy.Scope, error) {
	return []policy.Scope{policy.ScopeWrite}, nil
}

// grantNothing stands in for a bearer token that carries neither tier scope —
// the read-only-token deployment shape.
type grantNothing struct{}

func (grantNothing) GrantedScopes(context.Context) ([]policy.Scope, error) { return nil, nil }

func namesOf(tools []*mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// elicitationCapableOptions declares the client capability that lets it confirm a
// destructive tool: setting ElicitationHandler is what causes the SDK to
// advertise the capability during initialize.
func elicitationCapableOptions() *mcp.ClientOptions {
	return &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "accept"}, nil
		},
	}
}

// writeOnlyGranted opens the write tier only, leaving destructive unreachable
// even though the operator enabled it — the "write enabled and granted, but not
// destructive" deployment shape.
func writeOnlyGranted(t *testing.T) func(*mcpserver.Deps) {
	t.Helper()

	return func(d *mcpserver.Deps) {
		p, err := policy.New(policy.Config{
			Mode:              policy.ModeLocal,
			ReadOnlyTools:     []string{mcpserver.ServerInfoToolName, readTool},
			WriteTools:        []string{writeTool},
			DestructiveTools:  []string{destructiveTool},
			EnableWrite:       true,
			EnableDestructive: true,
		}, grantWriteOnly{})
		if err != nil {
			t.Fatalf("policy.New returned error: %v", err)
		}
		d.Policy = p
	}
}

// readOnlyTokenGranted is the operator having enabled both tiers, but the
// caller's own token carrying neither scope — proving enablement alone is
// never sufficient.
func readOnlyTokenGranted(t *testing.T) func(*mcpserver.Deps) {
	t.Helper()

	return func(d *mcpserver.Deps) {
		p, err := policy.New(policy.Config{
			Mode:              policy.ModeLocal,
			ReadOnlyTools:     []string{mcpserver.ServerInfoToolName, readTool},
			WriteTools:        []string{writeTool},
			DestructiveTools:  []string{destructiveTool},
			EnableWrite:       true,
			EnableDestructive: true,
		}, grantNothing{})
		if err != nil {
			t.Fatalf("policy.New returned error: %v", err)
		}
		d.Policy = p
	}
}

// TestToolsListOnStdioShowsOnlyReadOnlyTools covers the stdio deployment shape:
// the scope source is empty by construction, so every write and destructive
// tool must be absent from tools/list, whatever the operator's tier lists say.
func TestToolsListOnStdioShowsOnlyReadOnlyTools(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, nil)
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}

	want := []string{mcpserver.ServerInfoToolName, readTool}
	slices.Sort(want)
	if got := namesOf(result.Tools); !slices.Equal(got, want) {
		t.Fatalf("tools/list = %v, want exactly %v", got, want)
	}
}

// TestToolsListWithReadOnlyTokenShowsOnlyReadOnlyTools covers a streamable-http
// caller whose token carries neither tier scope: enabling both tiers at the
// operator level must not be enough on its own.
func TestToolsListWithReadOnlyTokenShowsOnlyReadOnlyTools(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, readOnlyTokenGranted(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}

	want := []string{mcpserver.ServerInfoToolName, readTool}
	slices.Sort(want)
	if got := namesOf(result.Tools); !slices.Equal(got, want) {
		t.Fatalf("tools/list = %v, want exactly %v", got, want)
	}
}

// TestToolsListWithWriteGrantedShowsReadOnlyAndWrite covers write enabled and
// granted, destructive neither: the destructive tool must stay hidden even
// though the operator's destructive tier list carries it.
func TestToolsListWithWriteGrantedShowsReadOnlyAndWrite(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, writeOnlyGranted(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}

	want := []string{mcpserver.ServerInfoToolName, readTool, writeTool}
	slices.Sort(want)
	if got := namesOf(result.Tools); !slices.Equal(got, want) {
		t.Fatalf("tools/list = %v, want exactly %v", got, want)
	}
}

// TestToolsListWithDestructiveGrantedAndElicitationShowsAllThreeTiers covers
// both tiers enabled and both scopes granted, with a client that declares it can
// confirm: every registered tool must be visible, because this is the one
// deployment shape where the destructive tool could actually be called end to
// end. This assumes elicitation capability; see
// TestToolsListHidesDestructiveToolsWhenClientDeclaresNoElicitation for the
// deliberately narrower case.
func TestToolsListWithDestructiveGrantedAndElicitationShowsAllThreeTiers(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, destructiveEnabled(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, elicitationCapableOptions())

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}

	want := []string{mcpserver.ServerInfoToolName, readTool, writeTool, destructiveTool}
	slices.Sort(want)
	if got := namesOf(result.Tools); !slices.Equal(got, want) {
		t.Fatalf("tools/list = %v, want exactly %v", got, want)
	}
}

// TestToolsListHidesDestructiveToolsWhenClientDeclaresNoElicitation is the P1
// regression: a client holding garmin:destructive but declaring no elicitation
// capability would have every destructive call refused by confirmDestructive, so
// tools/list must not advertise the tool as a capability in the first place.
// Read-only and write tools, which never need confirmation, are unaffected.
func TestToolsListHidesDestructiveToolsWhenClientDeclaresNoElicitation(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, destructiveEnabled(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}

	want := []string{mcpserver.ServerInfoToolName, readTool, writeTool}
	slices.Sort(want)
	if got := namesOf(result.Tools); !slices.Equal(got, want) {
		t.Fatalf("tools/list = %v, want exactly %v (destructiveTool must be hidden "+
			"without a declared elicitation capability)", got, want)
	}
}

// TestToolsListNeverShowsAToolAnAllowlistExcludes pins the filter to Decide
// itself rather than to a parallel classification: a hardcoded tier-only view
// would show writeTool here because it is nominally in the write tier, but
// Decide refuses it because the operator's allowlist does not name it.
func TestToolsListNeverShowsAToolAnAllowlistExcludes(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, func(d *mcpserver.Deps) {
		p, err := policy.New(policy.Config{
			Mode:              policy.ModeLocal,
			ReadOnlyTools:     []string{mcpserver.ServerInfoToolName, readTool},
			WriteTools:        []string{writeTool},
			DestructiveTools:  []string{destructiveTool},
			EnableWrite:       true,
			EnableDestructive: true,
			Allowlist:         []string{mcpserver.ServerInfoToolName, readTool},
		}, grantAll{})
		if err != nil {
			t.Fatalf("policy.New returned error: %v", err)
		}
		d.Policy = p
	})
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}

	want := []string{mcpserver.ServerInfoToolName, readTool}
	slices.Sort(want)
	if got := namesOf(result.Tools); !slices.Equal(got, want) {
		t.Fatalf("tools/list = %v, want exactly %v (the allowlist must be honored)", got, want)
	}
}

// TestToolsListNeverAddsAToolDecideWouldRefuse is the property test: for every
// deployment shape above, every tool tools/list returns must be one Decide
// itself allows. This is what makes "the filter is the same gate" true rather
// than aspirational.
func TestToolsListNeverAddsAToolDecideWouldRefuse(t *testing.T) {
	t.Parallel()

	shapes := []func(*mcpserver.Deps){
		nil,
		readOnlyTokenGranted(t),
		writeOnlyGranted(t),
		destructiveEnabled(t),
	}

	for i, shape := range shapes {
		server, _, _ := tieredServer(t, shape)
		ctx := context.Background()
		session := connectClient(t, ctx, server, nil)

		result, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("shape %d: ListTools returned error: %v", i, err)
		}
		for _, tool := range result.Tools {
			decision := server.Policy().Decide(ctx, tool.Name)
			if !decision.Allowed {
				t.Fatalf("shape %d: %s is on the wire but Decide refuses it: %s",
					i, tool.Name, decision.Reason)
			}
		}
	}
}

// TestToolsListPublishesTheTierInMeta proves a client can answer "why is this
// tool not in my list" from the wire itself: every listed tool's _meta carries
// its policy tier under the "tier" key, using the SDK's supported per-tool
// metadata slot rather than a non-standard top-level field.
func TestToolsListPublishesTheTierInMeta(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, destructiveEnabled(t))
	ctx := context.Background()
	// Elicitation-capable, so the destructive tool is on the wire to check: an
	// undeclared-capability client would never see it at all, and this test's
	// point is what its _meta says, not whether it is present.
	session := connectClient(t, ctx, server, elicitationCapableOptions())

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}

	want := map[string]string{
		mcpserver.ServerInfoToolName: tierReadOnly,
		readTool:                     tierReadOnly,
		writeTool:                    tierWrite,
		destructiveTool:              tierDestructive,
	}
	for _, tool := range result.Tools {
		wantTier, ok := want[tool.Name]
		if !ok {
			continue
		}
		if tool.Meta == nil {
			t.Errorf("%s: _meta is absent", tool.Name)
			continue
		}
		if got, _ := tool.Meta["tier"].(string); got != wantTier {
			t.Errorf("%s: _meta[tier] = %q, want %q", tool.Name, got, wantTier)
		}
	}
}

// TestToolsListResultIsPrivatelyCacheable proves the filtered tools/list result
// is marked private rather than the SDK's default public: the result is
// caller-specific (this session's own scopes and elicitation capability), so a
// shared intermediary permitted to cache a "public" result could serve one
// caller's filtered list to a different caller.
func TestToolsListResultIsPrivatelyCacheable(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, nil)
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}
	if result.CacheScope != "private" {
		t.Fatalf("CacheScope = %q, want %q", result.CacheScope, "private")
	}
}

// httpGrantScopes reads granted scopes off ctx through an HTTPAuthorizer's
// Grant, exactly the way internal/cmd's production grantedScopes reads them off
// a verified bearer token — the point being that the scopes come from the
// request context the transport put them on, not from a context-insensitive
// test fake. This is what makes TestToolsListOverStreamableHTTPUsesTheRequests
// ContextNotABackgroundContext able to catch a Decide(context.Background(), ...)
// mutant: a fake ScopeSource that ignores ctx cannot fail that way in the first
// place, so it cannot tell the two apart.
type httpGrantScopes struct {
	authorizer mcpserver.HTTPAuthorizer
}

func (h httpGrantScopes) GrantedScopes(ctx context.Context) ([]policy.Scope, error) {
	grant, err := h.authorizer.Grant(ctx)
	if err != nil {
		return nil, err
	}
	scopes := make([]policy.Scope, 0, len(grant.Scopes))
	for _, scope := range grant.Scopes {
		scopes = append(scopes, policy.Scope(scope))
	}
	return scopes, nil
}

// tieredHTTPServer builds a Streamable HTTP transport wired to authorizer, with
// the same three-tier tool set tieredServer registers over the in-memory
// transport, and a policy whose ScopeSource is httpGrantScopes — a real,
// context-derived source rather than an in-memory fake.
func tieredHTTPServer(t *testing.T, authorizer mcpserver.HTTPAuthorizer) *mcpserver.HTTPTransport {
	t.Helper()

	resolver, err := identity.NewBearerResolver(mcpserver.PrincipalSource(authorizer))
	if err != nil {
		t.Fatalf("identity.NewBearerResolver returned error: %v", err)
	}
	pol, err := policy.New(policy.Config{
		Mode:              policy.ModeRemote,
		ReadOnlyTools:     []string{mcpserver.ServerInfoToolName, readTool},
		WriteTools:        []string{writeTool},
		DestructiveTools:  []string{destructiveTool},
		EnableWrite:       true,
		EnableDestructive: true,
	}, httpGrantScopes{authorizer: authorizer})
	if err != nil {
		t.Fatalf("policy.New returned error: %v", err)
	}

	probes := map[string]*probe{readTool: {}, writeTool: {}, destructiveTool: {}}
	deps := mcpserver.Deps{
		Info:       mcpserver.Info{Name: testServerName, Version: testVersion},
		Logger:     mustLogger(t, &syncBuffer{}, mcplog.Config{}),
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{registrarFunc(func(r *mcpserver.Registry) error {
			for name, tier := range map[string]policy.Tier{
				readTool: policy.TierReadOnly, writeTool: policy.TierWrite,
				destructiveTool: policy.TierDestructive,
			} {
				if err := mcpserver.AddTool(r, spec(name, tier), probes[name].handler); err != nil {
					return err
				}
			}
			return nil
		})},
	}
	server := newTestServer(t, deps)

	transport, err := mcpserver.NewHTTPTransport(server, testHTTPOptions(authorizer))
	if err != nil {
		t.Fatalf("NewHTTPTransport returned error: %v", err)
	}
	return transport
}

// listToolsBody is a protocol-correct tools/list request.
func listToolsBody(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list","params":{}}`, id)
}

// listedToolNamesOverHTTP drives one tools/list POST and extracts the tool names
// from the JSON-RPC response body, tolerating either a plain JSON body or an SSE
// event stream, whichever the transport chose for this response.
func listedToolNamesOverHTTP(t *testing.T, transport *mcpserver.HTTPTransport, token, sessionID string) []string {
	t.Helper()

	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder, mcpPOST(t, listToolsBody(2), token, sessionID))
	if recorder.Code != 200 {
		t.Fatalf("tools/list status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}

	body := recorder.Body.String()
	if idx := strings.Index(body, "{"); idx >= 0 {
		body = body[idx:]
	}
	if idx := strings.LastIndex(body, "}"); idx >= 0 {
		body = body[:idx+1]
	}

	var decoded struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding tools/list response %q: %v", body, err)
	}
	names := make([]string, 0, len(decoded.Result.Tools))
	for _, tool := range decoded.Result.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// TestToolsListOverStreamableHTTPUsesTheRequestsContextNotABackgroundContext is
// the P5 regression: it drives two real Streamable HTTP sessions, one bearer
// token carrying the write scope and one carrying none, against the same
// running server, and requires the two tools/list results to differ. Only the
// write tool is at stake here, not destructive, because destructive visibility
// additionally needs a declared elicitation capability (P1) that this raw
// JSON-RPC client does not advertise; that is a separate, already-covered
// property, and mixing it in here would muddy which gate a failure points at.
//
// An in-memory, context-insensitive ScopeSource fake cannot exercise this,
// because it answers the same regardless of which request asked; it also cannot
// catch the mutant this guards against, Decide(context.Background(), tool.Name),
// which would make httpGrantScopes fail to find a grant on the empty context and
// both sessions would silently fall back to read-only-only.
func TestToolsListOverStreamableHTTPUsesTheRequestsContextNotABackgroundContext(t *testing.T) {
	t.Parallel()

	const (
		privilegedToken = "token-with-write-scope" //nolint:gosec // test fixture, not a credential
		plainToken      = "token-read-only"        //nolint:gosec // test fixture, not a credential
	)
	authorizer := &fakeAuthorizer{grants: map[string]mcpserver.Grant{
		privilegedToken: {
			Principal: mustPrincipalID(t, "principal-privileged"),
			ClientID:  clientA,
			Resource:  testResource,
			Scopes:    []string{string(policy.ScopeWrite)},
			Family:    "family-privileged",
		},
		plainToken: {
			Principal: mustPrincipalID(t, "principal-plain"),
			ClientID:  clientB,
			Resource:  testResource,
			Scopes:    nil,
			Family:    "family-plain",
		},
	}}
	transport := tieredHTTPServer(t, authorizer)

	privilegedSession := initSession(t, transport, privilegedToken)
	plainSession := initSession(t, transport, plainToken)

	privileged := listedToolNamesOverHTTP(t, transport, privilegedToken, privilegedSession)
	plain := listedToolNamesOverHTTP(t, transport, plainToken, plainSession)

	wantPrivileged := []string{mcpserver.ServerInfoToolName, readTool, writeTool}
	slices.Sort(wantPrivileged)
	wantPlain := []string{mcpserver.ServerInfoToolName, readTool}
	slices.Sort(wantPlain)

	if !slices.Equal(privileged, wantPrivileged) {
		t.Fatalf("privileged session tools/list = %v, want %v", privileged, wantPrivileged)
	}
	if !slices.Equal(plain, wantPlain) {
		t.Fatalf("plain session tools/list = %v, want %v", plain, wantPlain)
	}
	if slices.Equal(privileged, plain) {
		t.Fatalf("both sessions saw the same tools/list %v; the filter must be reading "+
			"per-request scopes, not a fixed value", privileged)
	}
}

// TestToolsListFilteringLeavesTheRegistryComplete proves the registry itself is
// never narrowed: only the per-request view is. This is what "registration is
// unchanged" means operationally.
func TestToolsListFilteringLeavesTheRegistryComplete(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, nil)

	want := []string{mcpserver.ServerInfoToolName, readTool, writeTool, destructiveTool}
	slices.Sort(want)
	if got := server.ToolNames(); !slices.Equal(got, want) {
		t.Fatalf("ToolNames() = %v, want the complete registered set %v", got, want)
	}
	if server.Registry().Len() != len(want) {
		t.Fatalf("Registry().Len() = %d, want %d", server.Registry().Len(), len(want))
	}
}
