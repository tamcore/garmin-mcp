//go:build garminlive

package live

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// envWriteAck is the fourth gate. It is separate from GARMIN_LIVE_ACK and default
// off, so acknowledging live *traffic* never acknowledges live *mutation*: the
// read-only suite runs exactly as it did before with this one unset.
const envWriteAck = "GARMIN_LIVE_WRITE_ACK"

// writeAckValue is the exact value envWriteAck must carry. Like the read
// acknowledgement it is spelled out rather than truthy.
const writeAckValue = "i-accept-live-garmin-writes"

// objectPrefix marks every object this suite creates.
//
// It is reserved and deliberately unmistakable: no Garmin default begins with it. It
// is not on its own a licence to remove anything — a prefix is a string a person could
// type, so the sweeper parses the whole generated shape and an earlier run's stamp
// before it touches an object. See isPreviousRunObject.
const objectPrefix = "garmin-mcp-live-"

// scheduleOffsetDays places every calendar write far enough into the future that it
// cannot sit on a real training day. Nothing already on the calendar is read for a
// decision, and nothing but this suite's own entry is ever removed.
const scheduleOffsetDays = 240

// writeGate reports why the write tests must not run, or "" when all four gates are
// open. A missing gate is a skip, never a failure.
func writeGate() string {
	if skip := gate(); skip != "" {
		return skip
	}
	if os.Getenv(envWriteAck) != writeAckValue {
		return fmt.Sprintf(
			"not run — write acknowledgement absent: set %s=%s to allow this suite to create, "+
				"update and delete objects it creates itself on the real Garmin account",
			envWriteAck, writeAckValue)
	}
	return ""
}

// writeScopes is the granted-scope source the write policy is built on.
//
// It is the single piece of the remote path this suite stands in for: the scopes a
// real deployment reads from a verified bearer token are supplied here directly,
// because this suite runs no authorization server. Everything the scopes then pass
// through — the operator enablement, the tier intersection, the confirmation
// middleware, the registry and the request layer — is the real code.
type writeScopes struct{}

// GrantedScopes grants both tier scopes.
func (writeScopes) GrantedScopes(context.Context) ([]policy.Scope, error) {
	return []policy.Scope{policy.ScopeWrite, policy.ScopeDestructive}, nil
}

// writeEnv is the write half of the suite: one guarded session, one MCP session
// whose policy enables both higher tiers, and the domain clients the read-backs use.
type writeEnv struct {
	// skip is the reason the write tests must not run. Every other field is unset
	// when it is non-empty.
	skip string

	owned *ownedObjects

	// names renders every generated name of this run, and startedAt is the one
	// instant the run is stamped with. They are fields rather than package state so
	// the run's identity is something the environment carries, and so the sweeper's
	// cut-off and the names it later parses come from the same instant.
	names     *nameSequence
	startedAt time.Time

	session client.Session
	mcp     *mcp.ClientSession

	// confirmations counts the elicitations the client answered, so a test can
	// assert that a destructive tool really did ask before it ran.
	confirmations *atomic.Int64

	workouts *api.Workouts
	writes   *api.ActivityWrites
	details  *api.ActivityDetails
	calendar *api.Calendar
}

// A writeSuite is the lifecycle of the one write environment a run may have: build it
// at most once, and let the end-of-suite leak report reach it without building
// anything.
//
// It is the write half's only package-level state, and it is one value rather than the
// three it replaces — a counter, a clock and a pointer — because `go test` gives a
// suite exactly one entry point that is not a test, TestMain, and a test can be handed
// nothing but its own *testing.T. Everything a run accumulates lives inside the
// environment this holds, so the state is per run and explicit; what cannot be avoided
// is the single handle to it.
//
// That handle, theWriteSuite below, is one of the two package-level variables AGENTS.md
// records as forced exceptions to the no-package-state rule; see its "Code Conventions"
// section. It is written once, by the sync.Once inside it, and read afterwards. New
// state of the write half goes inside writeEnv rather than beside it.
type writeSuite struct {
	once  sync.Once
	built atomic.Pointer[writeEnv]
	env   *writeEnv
	err   error
}

// theWriteSuite is that single handle.
var theWriteSuite writeSuite

// get builds the environment on the first call and returns the same one after that.
func (s *writeSuite) get(now func() time.Time) (*writeEnv, error) {
	s.once.Do(func() {
		s.env, s.err = buildWriteEnv(now)
		if s.err != nil {
			return
		}
		// The environment is published before the sweep runs, so a sweep that fails
		// half way still leaves the leak report able to reach the ledger.
		s.built.Store(s.env)
		if s.env.skip == "" {
			s.err = s.env.sweep()
		}
	})
	return s.env, s.err
}

// builtWriteEnv returns the write environment if one was built, and nil otherwise.
func builtWriteEnv() *writeEnv { return theWriteSuite.built.Load() }

// liveWriteEnv returns the shared write session, skipping the calling test when a
// gate is shut.
func liveWriteEnv(t *testing.T) *writeEnv {
	t.Helper()

	w, err := theWriteSuite.get(time.Now)
	if err != nil {
		t.Fatalf("live: preparing the guarded write session: %v", err)
	}
	if w.skip != "" {
		t.Skip(w.skip)
	}
	return w
}

// buildWriteEnv opens the fourth gate, wraps the shared session in the write guard
// and sweeps whatever a killed run left behind.
//
// now is the run's clock. It is a parameter rather than a call to time.Now inside,
// because the instant it returns is both the stamp every generated name carries and
// the cut-off the sweeper compares those names against, and those two must be the same
// instant or a name of this run could be read as an earlier run's.
func buildWriteEnv(now func() time.Time) (*writeEnv, error) {
	if skip := writeGate(); skip != "" {
		return &writeEnv{skip: skip}, nil
	}
	read, err := shared()
	if err != nil {
		return nil, err
	}
	if read.skip != "" {
		return &writeEnv{skip: read.skip}, nil
	}

	owned := newOwnedObjects()
	caller := writeCaller{inner: read.refresher, owned: owned}
	session, err := client.NewSession(caller, livePrincipal)
	if err != nil {
		return nil, fmt.Errorf("building the guarded write session: %w", err)
	}

	startedAt := now().UTC()
	w := &writeEnv{
		owned:         owned,
		names:         newNameSequence(startedAt),
		startedAt:     startedAt,
		session:       session,
		confirmations: &atomic.Int64{},
	}
	if err := w.buildDomainClients(read.rest); err != nil {
		return nil, err
	}
	if err := w.buildMCPSession(read.rest, caller); err != nil {
		return nil, err
	}
	return w, nil
}

// buildDomainClients builds the write and read-back clients over the same request
// layer the read half uses. Only the session differs, and the session carries the
// guard.
func (w *writeEnv) buildDomainClients(rest *client.Client) error {
	var err error
	if w.workouts, err = api.NewWorkouts(rest); err != nil {
		return fmt.Errorf("building the workout client: %w", err)
	}
	if w.writes, err = api.NewActivityWrites(rest); err != nil {
		return fmt.Errorf("building the activity write client: %w", err)
	}
	if w.details, err = api.NewActivityDetails(rest); err != nil {
		return fmt.Errorf("building the activity detail client: %w", err)
	}
	if w.calendar, err = api.NewCalendar(rest); err != nil {
		return fmt.Errorf("building the calendar client: %w", err)
	}
	return nil
}

// buildMCPSession stands up a second real server whose policy enables both higher
// tiers, and attaches an elicitation-capable client to it.
//
// The two servers share no state beyond the request layer: this one is built on the
// write guard and the read-only server keeps its own caller, so a write tool cannot
// be reached through the read-only session even by mistake.
func (w *writeEnv) buildMCPSession(rest *client.Client, caller client.Caller) error {
	registrar, err := tools.New(tools.Deps{Client: rest, Caller: caller})
	if err != nil {
		return fmt.Errorf("building the write tool registrar: %w", err)
	}
	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{livePrincipal},
	})
	if err != nil {
		return fmt.Errorf("building the principal resolver: %w", err)
	}
	pol, err := policy.New(policy.Config{
		Mode:              policy.ModeRemote,
		ReadOnlyTools:     tools.ReadOnlyTools(),
		WriteTools:        tools.WriteTools(),
		DestructiveTools:  tools.DestructiveTools(),
		EnableWrite:       true,
		EnableDestructive: true,
	}, writeScopes{})
	if err != nil {
		return fmt.Errorf("building the write policy: %w", err)
	}
	server, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-live-write", Version: "0.0.0-live"},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{registrar},
	})
	if err != nil {
		return fmt.Errorf("building the write MCP server: %w", err)
	}
	return w.connect(server)
}

// connect runs the write server and attaches one confirming MCP client to it.
//
// The handler confirms every destructive prompt, which is deliberate and is not the
// safety boundary: the prompt carries no identifier by design, so it could not check
// one. What stops a destructive call against the maintainer's data is the guard one
// layer below, on the transport, which an accepted elicitation cannot bypass.
func (w *writeEnv) connect(server *mcpserver.Server) error {
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.MCPServer().Run(ctx, serverTransport)
	}()

	options := &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			w.confirmations.Add(1)
			return &mcp.ElicitResult{
				Action: "accept", Content: map[string]any{"confirm": true},
			}, nil
		},
	}
	session, err := mcp.NewClient(&mcp.Implementation{
		Name: "garmin-mcp-live-write", Version: "live",
	}, options).Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		<-done
		return fmt.Errorf("connecting the live write MCP client: %w", err)
	}

	closers = append(closers, func() {
		_ = session.Close()
		cancel()
		<-done
	})
	w.mcp = session
	return nil
}

// call invokes one tool over the write session and requires a successful result.
func (w *writeEnv) call(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()

	result := w.rawCall(t, name, args)
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

// rawCall invokes one tool over the write session and returns whatever came back.
func (w *writeEnv) rawCall(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	if args == nil {
		args = map[string]any{}
	}
	time.Sleep(requestPause)

	ctx, cancel := context.WithTimeout(t.Context(), 4*requestTimeout)
	defer cancel()

	result, err := w.mcp.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) transport error: %v", name, err)
	}
	return result
}

// identifier reads a positive identifier out of a tool result.
func identifier(t *testing.T, result map[string]any, tool, field string) int64 {
	t.Helper()

	value, ok := result[field].(float64)
	if !ok || value <= 0 {
		t.Fatalf("%s returned no usable %s", tool, field)
	}
	return int64(value)
}
