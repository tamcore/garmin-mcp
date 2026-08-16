package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Argument names the write tests send, named once so a rename shows up in one place.
const (
	argName            = "name"
	argSets            = "sets"
	argKind            = "kind"
	argCategory        = "category"
	argExerciseName    = "exercise_name"
	argReps            = "reps"
	argRestSeconds     = "rest_seconds"
	argDurationSeconds = "duration_seconds"
	argRepetitions     = "repetitions"
	argWeightGrams     = "weight_grams"
	argStartTime       = "start_time"
	argRunSeconds      = "run_seconds"
	argWalkSeconds     = "walk_seconds"
	argWarmupMin       = "warmup_min"
	argCooldownMin     = "cooldown_min"
	argHRMin           = "hr_min"
	argHRMax           = "hr_max"
	argWorkoutID       = "workout_id"
	argWorkoutData     = "workout_data"
	argCalendarDate    = "calendar_date"
	argActivityName    = "activity_name"
	argTypeKey         = "type_key"
	argDescription     = "description"
	argFormat          = "format"
)

// Synthetic values several write tests share.
const (
	testExerciseName = "BARBELL_BENCH_PRESS"
	testCategory     = "BENCH_PRESS"
	setKindActive    = "ACTIVE"
	testSetStart     = "2026-01-31T06:12:00Z"
	savedWorkoutName = "Saved by Garmin"
	workoutNameKey   = "workoutName"

	// traversalAttempt is the hostile argument every path-shaped argument is
	// probed with: no tool may let it reach a URL or a filesystem.
	traversalAttempt = "../../etc/passwd"
)

// grantedScopes is the ScopeSource a write test needs.
//
// The default deployment refuses every write, because the tier starts disabled and no
// scope is presented. A test that wants to exercise a handler has to grant the scope
// explicitly, which is the same seam the remote bearer-token path fills in production.
type grantedScopes struct {
	scopes []policy.Scope
}

func (g grantedScopes) GrantedScopes(context.Context) ([]policy.Scope, error) {
	return g.scopes, nil
}

// refusingConfirmer stands in for a client that cannot be asked to confirm.
type refusingConfirmer struct{}

func (refusingConfirmer) Confirm(context.Context, policy.ConfirmationRequest) error {
	return policy.ErrConfirmationUnsupported
}

// grantingConfirmer stands in for a user who confirmed.
type grantingConfirmer struct{}

func (grantingConfirmer) Confirm(context.Context, policy.ConfirmationRequest) error { return nil }

// writeOptions configure a harness that may reach the write tiers.
type writeOptions struct {
	enableWrite       bool
	enableDestructive bool
	scopes            []policy.Scope
	confirmer         policy.Confirmer
}

// enabledWrites grants everything a write tool needs and confirms every destructive
// call, which is the configuration a handler test wants.
func enabledWrites() writeOptions {
	return writeOptions{
		enableWrite:       true,
		enableDestructive: true,
		scopes:            []policy.Scope{policy.ScopeWrite, policy.ScopeDestructive},
		confirmer:         grantingConfirmer{},
	}
}

// newWriteHarness builds a harness whose policy is configured by opts.
func newWriteHarness(t *testing.T, script testkit.Script, opts writeOptions) harness {
	t.Helper()

	return newWriteHarnessWithCatalog(t, script, opts, nil)
}

// newWriteHarnessWithCatalog is newWriteHarness with an explicit strength
// catalog, which is how a write test drives the tools against a fetched catalog.
func newWriteHarnessWithCatalog(
	t *testing.T, script testkit.Script, opts writeOptions, catalog *api.ExerciseCatalog,
) harness {
	t.Helper()

	fake := testkit.NewServer(t, script)
	server := newWriteServer(t,
		newRegistrar(t, fake, tools.Bounds{}, client.Limits{}, catalog), opts)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.MCPServer().Run(ctx, serverTransport)
	}()

	session, err := mcp.NewClient(&mcp.Implementation{
		Name: "garmin-mcp-write-test", Version: "test",
	}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connecting the test client: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		<-done
	})

	return harness{fake: fake, session: session}
}

func newWriteServer(t *testing.T, registrar *tools.Registrar, opts writeOptions) *mcpserver.Server {
	t.Helper()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{testPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}

	var scopes policy.ScopeSource
	if len(opts.scopes) > 0 {
		scopes = grantedScopes{scopes: opts.scopes}
	}
	pol, err := policy.New(policy.Config{
		Mode:              policy.ModeLocal,
		ReadOnlyTools:     tools.ReadOnlyTools(),
		WriteTools:        tools.WriteTools(),
		DestructiveTools:  tools.DestructiveTools(),
		EnableWrite:       opts.enableWrite,
		EnableDestructive: opts.enableDestructive,
	}, scopes)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	server, err := mcpserver.New(mcpserver.Deps{
		Info:                mcpserver.Info{Name: "garmin-mcp-test", Version: "0.0.0-test"},
		Policy:              pol,
		Principals:          resolver,
		Registrars:          []mcpserver.ToolRegistrar{registrar},
		Confirmer:           opts.confirmer,
		ConfirmationTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}
	return server
}

// activityWritePath is the activity path every field write in these tests targets.
func activityWritePath() string { return client.PathActivityPrefix + "/" + testActivityID }

// workoutPath renders the path a single-workout call targets.
func workoutPath(id string) string { return client.PathWorkoutPrefix + "/" + id }

// bodyFor returns the decoded body of the first recorded request to method and path.
func (h harness) bodyFor(t *testing.T, method, path string) map[string]any {
	t.Helper()

	for _, request := range h.fake.Requests() {
		if request.Method != method || request.Path != path {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal(request.Body, &decoded); err != nil {
			t.Fatalf("decoding the %s %s body: %v", method, path, err)
		}
		return decoded
	}
	t.Fatalf("no %s %s request was recorded; recorded %v", method, path, h.recordedMethods())
	return nil
}

// recordedMethods returns the method and path of every recorded request.
func (h harness) recordedMethods() []string {
	recorded := h.fake.Requests()
	out := make([]string, 0, len(recorded))
	for _, request := range recorded {
		out = append(out, request.Method+" "+request.Path)
	}
	return out
}

// okJSON is the response a write endpoint that answers with a body gives.
func okJSON(body string) testkit.Behavior { return testkit.JSON(http.StatusOK, body) }
