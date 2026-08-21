package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Synthetic Garmin Coach fixtures. Every identifier, name and date is invented, and
// the body is the trainingPlanScalar shape the GraphQL tier answers with.
const (
	coachPrincipal = "principal-coach-0001"
	coachDate      = "2026-01-05"
	coachUUID      = "11111111-2222-3333-4444-555555555555"

	coachWindowBody = `{"data":{"trainingPlanScalar":{"trainingPlanWorkoutScheduleDTOS":[` +
		`{"planName":"Synthetic 5K Plan","trainingPlanId":5001,` +
		`"trainingPlanClassification":"ADAPTIVE",` +
		`"trainingPlanDetailsDTO":{"trainingType":"RUNNING"},` +
		`"workoutScheduleSummaries":[` +
		`{"scheduleDate":"2026-01-05","workoutUuid":"` + coachUUID + `",` +
		`"workoutName":"Base Run","workoutType":"running","workoutPhrase":"LONG_WORKOUT",` +
		`"estimatedDurationInSecs":2400},` +
		`{"scheduleDate":"2026-01-06","workoutName":"Rest","isRestDay":true}]}]}}}`

	// coachEmptyWindowBody is the honest empty answer: Garmin generates the window
	// itself, so a date outside it returns no plan while a plan is still active.
	coachEmptyWindowBody = `{"data":{"trainingPlanScalar":{"trainingPlanWorkoutScheduleDTOS":[]}}}`

	// coachFailureBody carries a secret so the sanitization test can prove no raw
	// Garmin body reaches the caller.
	coachFailureBody = `{"error":"synthetic","token":"super-secret-di-token"}`
)

// coachCaller dispatches for one principal against the fake service. No credential is
// in play: the fake's Doer enforces the origin, so no test can reach real Garmin.
type coachCaller struct {
	doer testkit.Doer
}

func (c coachCaller) Do(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	if principal == "" {
		return nil, errors.New("coachCaller: no principal")
	}
	return c.doer.Do(req.WithContext(ctx))
}

// coachRegistrar registers the Garmin Coach tool and nothing else, so the tool can be
// put through the real server before register.go carries it.
type coachRegistrar struct {
	svc *service
}

func (c coachRegistrar) RegisterTools(registry *mcpserver.Registry) error {
	return registerGetGarminCoachWorkouts(registry, c.svc)
}

// coachService builds the tool service over a scripted fake Garmin service.
func coachService(t *testing.T, body string, status int) (*service, *testkit.Server) {
	t.Helper()

	fake := testkit.NewServer(t, testkit.NewScript().
		With(client.PathGraphQL, testkit.JSON(status, body)))
	rc, err := client.New(client.Config{Hosts: fake.Hosts(protocol.DomainGlobal)})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}
	svc, err := newService(Deps{Client: rc, Caller: coachCaller{doer: fake.Doer()}})
	if err != nil {
		t.Fatalf("newService() = %v", err)
	}
	return svc, fake
}

// coachContext returns a request context carrying a resolved principal, which is the
// only way a tool can learn whose account it reads.
func coachContext(t *testing.T) context.Context {
	t.Helper()

	principal, err := identity.NewPrincipal(coachPrincipal)
	if err != nil {
		t.Fatalf("identity.NewPrincipal() = %v", err)
	}
	return identity.WithPrincipal(t.Context(), principal)
}

func TestGarminCoachWorkoutsDeclaresTheUpstreamContract(t *testing.T) {
	t.Parallel()

	contract := getGarminCoachWorkoutsContract()
	if got := contract.Spec.Name; got != "get_garmin_coach_workouts" {
		t.Errorf("wire name = %q, want the upstream compatibility name", got)
	}
	if got := contract.Spec.Tier; got != policy.TierReadOnly {
		t.Errorf("tier = %v, want the read-only tier", got)
	}
	if got := contract.Spec.Category; got != categoryHealth {
		t.Errorf("log category = %q, want %q", got, categoryHealth)
	}

	want := mcpserver.Annotations{
		ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: true,
	}
	if got := contract.Spec.Annotations; got != want {
		t.Errorf("annotations = %+v, want %+v", got, want)
	}
}

// TestGarminCoachWorkoutsDeclaresAStrictSchemaWithNoAccountSelector pins the schema
// against the manifest's shape and against the rule that no tool names an account.
func TestGarminCoachWorkoutsDeclaresAStrictSchemaWithNoAccountSelector(t *testing.T) {
	t.Parallel()

	schema := getGarminCoachWorkoutsContract().Schema
	if got, want := schema.Required(), []string{argNameCalendarDate}; !slices.Equal(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}

	document := schema.JSON()
	if additional, ok := document[keyAdditionalProperties].(bool); !ok || additional {
		t.Errorf("additionalProperties = %v, want false", document[keyAdditionalProperties])
	}

	properties := schema.Properties()
	if len(properties) != 1 {
		t.Fatalf("declared %d properties, want exactly one", len(properties))
	}
	assertCalendarDateProperty(t, properties[0])
}

// assertCalendarDateProperty pins the one declared argument: a bounded calendar date,
// and nothing that could name an account.
func assertCalendarDateProperty(t *testing.T, date Property) {
	t.Helper()

	switch {
	case date.Name != argNameCalendarDate:
		t.Errorf("property name = %q, want %q", date.Name, argNameCalendarDate)
	case !slices.Equal(date.Types, []string{typeString}):
		t.Errorf("property types = %v, want [string]", date.Types)
	case date.Format != formatDate || date.Pattern != patternCalendarDate:
		t.Errorf("format = %q, pattern = %q, want a bounded calendar date",
			date.Format, date.Pattern)
	case date.MaxLength == nil || *date.MaxLength != maxDateArgumentLen:
		t.Errorf("maxLength = %v, want %d", date.MaxLength, maxDateArgumentLen)
	case !date.Required:
		t.Error("the reference date is optional, want it required")
	}

	for _, selector := range []string{"user_id", keyEmail, keyDisplayName, keyToken} {
		if strings.Contains(strings.ToLower(date.Description), selector) {
			t.Errorf("the argument description mentions the account selector %q", selector)
		}
	}
}

func TestGarminCoachWorkoutsRegistersInTheReadOnlyTier(t *testing.T) {
	t.Parallel()

	svc, _ := coachService(t, coachWindowBody, http.StatusOK)
	server := coachServer(t, svc)

	if !slices.Contains(server.ToolNames(), ToolGetGarminCoachWorkouts) {
		t.Fatalf("registered names = %v, want %q", server.ToolNames(), ToolGetGarminCoachWorkouts)
	}
	spec, ok := server.Registry().Spec(ToolGetGarminCoachWorkouts)
	if !ok {
		t.Fatal("the registry recorded no spec for the Garmin Coach tool")
	}
	if spec.Tier != policy.TierReadOnly {
		t.Errorf("registered tier = %v, want the read-only tier", spec.Tier)
	}
	if !spec.Annotations.ReadOnly || spec.Annotations.Destructive || !spec.Annotations.OpenWorld {
		t.Errorf("registered annotations = %+v, want a read-only open-world tool", spec.Annotations)
	}
}

// coachServer stands up the real server carrying only the Garmin Coach tool, so the
// registration path is exercised before register.go lists it.
func coachServer(t *testing.T, svc *service) *mcpserver.Server {
	t.Helper()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{coachPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}
	pol, err := policy.New(policy.Config{
		Mode:          policy.ModeLocal,
		ReadOnlyTools: []string{mcpserver.ServerInfoToolName, ToolGetGarminCoachWorkouts},
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	server, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-test", Version: "0.0.0-test"},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{coachRegistrar{svc: svc}},
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}
	return server
}

// coachSession connects an in-memory MCP client to a server carrying only the Garmin
// Coach tool, so the registered handler is driven the way a real client drives it.
func coachSession(t *testing.T, server *mcpserver.Server) *mcp.ClientSession {
	t.Helper()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.MCPServer().Run(ctx, serverTransport)
	}()

	session, err := mcp.NewClient(&mcp.Implementation{
		Name: "garmin-mcp-coach-test", Version: "test",
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
	return session
}

// callCoach invokes the registered tool over the session and returns the result.
func callCoach(t *testing.T, session *mcp.ClientSession, date string) *mcp.CallToolResult {
	t.Helper()

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      ToolGetGarminCoachWorkouts,
		Arguments: map[string]any{argNameCalendarDate: date},
	})
	if err != nil {
		t.Fatalf("CallTool(%q) transport error = %v", ToolGetGarminCoachWorkouts, err)
	}
	return result
}

// TestGarminCoachWorkoutsAnswersOverTheWire drives the registered handler end to end,
// which is the only path that proves the declared schema and the handler agree.
func TestGarminCoachWorkoutsAnswersOverTheWire(t *testing.T) {
	t.Parallel()

	svc, _ := coachService(t, coachWindowBody, http.StatusOK)
	session := coachSession(t, coachServer(t, svc))

	result := callCoach(t, session, coachDate)
	if result.IsError {
		t.Fatalf("%s returned an error result", ToolGetGarminCoachWorkouts)
	}

	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshalling the structured content: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decoding the structured content: %v", err)
	}
	if got := out["count"]; got != float64(2) {
		t.Errorf("count = %v, want 2", got)
	}
	if got := out["date"]; got != coachDate {
		t.Errorf("date = %v, want %q", got, coachDate)
	}
}

// TestGarminCoachWorkoutsReturnsAnErrorResultOverTheWire covers the handler's refusal
// arm: a bad argument comes back as an MCP error result, not as a transport failure.
//
// The date used here satisfies the published pattern, so the schema admits it and the
// handler is the thing that refuses it. A date that fails the pattern would be turned
// away by the SDK and would prove nothing about this handler.
func TestGarminCoachWorkoutsReturnsAnErrorResultOverTheWire(t *testing.T) {
	t.Parallel()

	svc, fake := coachService(t, coachWindowBody, http.StatusOK)
	session := coachSession(t, coachServer(t, svc))

	if result := callCoach(t, session, "2026-13-45"); !result.IsError {
		t.Fatal("a malformed date succeeded, want an error result")
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("dispatched %d requests, want 0", got)
	}
}

func TestGarminCoachWorkoutsRefusesARegistryItDoesNotHave(t *testing.T) {
	t.Parallel()

	svc, _ := coachService(t, coachWindowBody, http.StatusOK)
	err := registerGetGarminCoachWorkouts(nil, svc)
	if !errors.Is(err, mcpserver.ErrMissingDependency) {
		t.Errorf("registering on no registry = %v, want ErrMissingDependency", err)
	}
}

func TestGarminCoachWorkoutsReturnsTheCuratedPlanWindow(t *testing.T) {
	t.Parallel()

	svc, fake := coachService(t, coachWindowBody, http.StatusOK)
	window, err := readTrainingPlanWindow(coachContext(t), svc, coachDate)
	if err != nil {
		t.Fatalf("readTrainingPlanWindow() = %v", err)
	}

	if window.Date != coachDate {
		t.Errorf("date = %q, want %q", window.Date, coachDate)
	}
	assertCoachPlan(t, window)
	assertCoachWorkouts(t, window)

	if got := len(fake.Requests()); got != 1 {
		t.Errorf("dispatched %d requests, want exactly 1", got)
	}
}

func assertCoachPlan(t *testing.T, window TrainingPlanWindow) {
	t.Helper()

	if len(window.Plans) != 1 {
		t.Fatalf("plans = %+v, want one plan", window.Plans)
	}
	plan := window.Plans[0]
	if plan.Name == nil || *plan.Name != "Synthetic 5K Plan" {
		t.Errorf("plan name = %v", plan.Name)
	}
	if plan.TrainingType == nil || *plan.TrainingType != "RUNNING" {
		t.Errorf("training type = %v", plan.TrainingType)
	}
	if plan.Classification == nil || *plan.Classification != "ADAPTIVE" {
		t.Errorf("classification = %v", plan.Classification)
	}
}

// assertCoachWorkouts pins the entry curation, rest day included: upstream counts the
// rest day, and so does this.
func assertCoachWorkouts(t *testing.T, window TrainingPlanWindow) {
	t.Helper()

	if window.Count != 2 || len(window.Workouts) != 2 {
		t.Fatalf("count = %d over %d workouts, want 2 including the rest day",
			window.Count, len(window.Workouts))
	}
	first := window.Workouts[0]
	if first.WorkoutUUID == nil || *first.WorkoutUUID != coachUUID {
		t.Errorf("workout_uuid = %v, want the adaptive plan identifier", first.WorkoutUUID)
	}
	if first.WorkoutType == nil || *first.WorkoutType != "LONG_WORKOUT" {
		t.Errorf("workout intent = %v", first.WorkoutType)
	}
	if first.EstimatedDurationSeconds == nil || *first.EstimatedDurationSeconds != 2400 {
		t.Errorf("estimated duration = %v, want 2400", first.EstimatedDurationSeconds)
	}
	if first.Completed {
		t.Error("completed = true without an associated activity")
	}
	if !window.Workouts[1].IsRestDay {
		t.Error("the second entry is not reported as a rest day")
	}
	if window.Truncated {
		t.Error("truncated = true for a two-entry window")
	}
}

// TestGarminCoachWorkoutsReportsAnEmptyWindowAsEmpty keeps the honest answer honest: a
// date outside Garmin's generated window is no plan, not a failure.
func TestGarminCoachWorkoutsReportsAnEmptyWindowAsEmpty(t *testing.T) {
	t.Parallel()

	svc, _ := coachService(t, coachEmptyWindowBody, http.StatusOK)
	window, err := readTrainingPlanWindow(coachContext(t), svc, coachDate)
	if err != nil {
		t.Fatalf("readTrainingPlanWindow() = %v", err)
	}
	if len(window.Plans) != 0 || window.Count != 0 || window.Truncated {
		t.Errorf("window = %+v, want an empty, untruncated window", window)
	}
}

func TestGarminCoachWorkoutsRefusesAMalformedDateBeforeAnyCall(t *testing.T) {
	t.Parallel()

	svc, fake := coachService(t, coachWindowBody, http.StatusOK)
	cases := map[string]string{
		"empty":        "",
		"not a date":   "not-a-date",
		"impossible":   "2026-13-45",
		"over-long":    strings.Repeat("9", 40),
		"wrong format": "05/01/2026",
	}

	for name, date := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := readTrainingPlanWindow(coachContext(t), svc, date)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("readTrainingPlanWindow(%s) = %v, want ErrInvalidArgument", name, err)
			}
			if err.Error() == "" {
				t.Error("the refusal carried no advice")
			}
		})
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("dispatched %d requests, want 0", got)
	}
}

// TestGarminCoachWorkoutsRefusesARequestWithNoPrincipal proves the account comes from
// the request context and from nowhere else.
func TestGarminCoachWorkoutsRefusesARequestWithNoPrincipal(t *testing.T) {
	t.Parallel()

	svc, fake := coachService(t, coachWindowBody, http.StatusOK)
	_, err := readTrainingPlanWindow(t.Context(), svc, coachDate)
	if !errors.Is(err, identity.ErrNoPrincipal) {
		t.Fatalf("readTrainingPlanWindow() = %v, want identity.ErrNoPrincipal", err)
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("dispatched %d requests, want 0", got)
	}
}

// TestGarminCoachWorkoutsSanitizesAGarminFailure pins the rule that no raw Garmin body
// reaches the caller.
func TestGarminCoachWorkoutsSanitizesAGarminFailure(t *testing.T) {
	t.Parallel()

	svc, _ := coachService(t, coachFailureBody, http.StatusInternalServerError)
	_, err := readTrainingPlanWindow(coachContext(t), svc, coachDate)
	if err == nil {
		t.Fatal("readTrainingPlanWindow() = nil, want a failure")
	}

	if _, ok := errors.AsType[*ToolError](err); !ok {
		t.Fatalf("readTrainingPlanWindow() = %T, want a *ToolError", err)
	}
	for _, leaked := range []string{"super-secret-di-token", "synthetic", coachPrincipal} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("the advice %q carries %q", err.Error(), leaked)
		}
	}
}
