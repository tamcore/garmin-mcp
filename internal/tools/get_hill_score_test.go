package tools

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture in the training-scores tests is synthetic: the scores, thresholds,
// ages and loads are invented, and none is a recording of a real account.

// The window every scores test asks for.
const (
	scoresStartDate = "2026-01-01"
	scoresEndDate   = "2026-01-31"
)

// scoresRegistrar registers exactly the seven training-scores tools.
//
// It exists because these tools are not yet listed in register.go, so the shared
// harness cannot reach them. It drives the real registration functions through the
// real server, which is what the shared harness does for the tools that are listed.
type scoresRegistrar struct {
	svc *service
}

// scoresRegistrations is the list register.go must grow, in the same order.
func scoresRegistrations() []registration {
	return []registration{
		{getHillScoreContract, registerGetHillScore},
		{getEnduranceScoreContract, registerGetEnduranceScore},
		{getTrainingEffectContract, registerGetTrainingEffect},
		{getFitnessAgeDataContract, registerGetFitnessAgeData},
		{getTrainingStatusContract, registerGetTrainingStatus},
		{getCyclingFTPContract, registerGetCyclingFTP},
		{getLactateThresholdContract, registerGetLactateThreshold},
	}
}

func (r scoresRegistrar) RegisterTools(registry *mcpserver.Registry) error {
	for _, entry := range scoresRegistrations() {
		if err := entry.register(registry, r.svc); err != nil {
			return err
		}
	}
	return nil
}

// newScoresHarness drives the seven tools over an MCP session against a scripted fake
// Garmin service, exactly as newToolHarness drives the registered surface.
func newScoresHarness(t *testing.T, script testkit.Script) toolHarness {
	t.Helper()
	return newScoresHarnessWith(t, script, client.Limits{})
}

func newScoresHarnessWith(
	t *testing.T, script testkit.Script, limits client.Limits,
) toolHarness {
	t.Helper()
	return newScoresHarnessAt(t, script, limits, nil)
}

// newScoresHarnessAt is newScoresHarnessWith with the service clock injected, for the
// tools that ask Garmin for a day rather than being told one.
func newScoresHarnessAt(
	t *testing.T, script testkit.Script, limits client.Limits, now func() time.Time,
) toolHarness {
	t.Helper()

	fake := testkit.NewServer(t, script)
	rc, err := client.New(client.Config{
		Hosts:   fake.Hosts(protocol.DomainGlobal),
		Limits:  limits,
		Sleeper: client.SleeperFunc(func(context.Context, time.Duration) error { return nil }),
		Jitter:  func() float64 { return 0 },
	})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}
	svc, err := newService(Deps{
		Client: rc, Caller: harnessCaller{doer: fake.Doer()}, Now: now,
	})
	if err != nil {
		t.Fatalf("newService() = %v", err)
	}
	return toolHarness{fake: fake, session: connectHarness(t, newScoresServer(t, svc))}
}

// newScoresServer builds a server carrying only the training-scores tools.
func newScoresServer(t *testing.T, svc *service) *mcpserver.Server {
	t.Helper()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{harnessPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}
	pol, err := policy.New(policy.Config{
		Mode: policy.ModeLocal,
		ReadOnlyTools: append([]string{mcpserver.ServerInfoToolName},
			namesOf(scoresRegistrations())...),
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	server, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-scores-test", Version: harnessVersion},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{scoresRegistrar{svc: svc}},
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}
	return server
}

// hillScoreDocument is two scored days plus the window aggregates. The maximum arrives
// as a numeric string and one score is null, because both are real shapes.
const hillScoreDocument = `{"periodAvgScore":{"7":63.5},"maxScore":"71",` +
	`"hillScoreDTOList":[{"calendarDate":"` + scoresEndDate + `","overallScore":68,` +
	`"strengthScore":51,"enduranceScore":null,"hillScoreClassificationId":3},` +
	`{"calendarDate":"2026-01-30","overallScore":66,"strengthScore":50,` +
	`"enduranceScore":49,"hillScoreClassificationId":3}]}`

func scoresWindowArgs() map[string]any {
	return map[string]any{argStartDate: scoresStartDate, argEndDate: scoresEndDate}
}

func TestGetHillScoreReturnsTheWindowAndItsLatestDay(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathHillScoreStats,
		testkit.JSON(http.StatusOK, hillScoreDocument))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetHillScore, scoresWindowArgs())

	if got, _ := result["start_date"].(string); got != scoresStartDate {
		t.Errorf("start_date = %q, want %q", got, scoresStartDate)
	}
	if got := number(t, result, "count"); got != 2 {
		t.Fatalf("count = %v, want 2", got)
	}
	if got := number(t, result, "period_avg_score"); got != 63.5 {
		t.Errorf("period_avg_score = %v, want 63.5 from the keyed object", got)
	}
	if got := number(t, result, "max_score"); got != 71 {
		t.Errorf("max_score = %v, want 71 from the string form", got)
	}
	if got := number(t, result, "latest_overall_score"); got != 68 {
		t.Errorf("latest_overall_score = %v, want 68", got)
	}
	if got, _ := result["latest_date"].(string); got != scoresEndDate {
		t.Errorf("latest_date = %q, want %q", got, scoresEndDate)
	}
	if _, present := result["latest_endurance_score"]; present {
		t.Error("a null endurance score reached the result")
	}

	day := entry(t, list(t, result, "daily_scores"), 0)
	if got := number(t, day, "strength"); got != 51 {
		t.Errorf("strength = %v, want 51", got)
	}
}

func TestGetHillScoreSendsTheWindowAndTheDailyAggregation(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathHillScoreStats,
		testkit.JSON(http.StatusOK, hillScoreDocument))
	h := newScoresHarness(t, script)

	h.call(t, ToolGetHillScore, scoresWindowArgs())

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want one", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryAggregation); got != client.AggregationDaily {
		t.Errorf("aggregation = %q, want %q", got, client.AggregationDaily)
	}
}

func TestGetHillScoreRefusesABadWindowBeforeDispatch(t *testing.T) {
	t.Parallel()

	cases := map[string]map[string]any{
		"inverted":  {argStartDate: scoresEndDate, argEndDate: scoresStartDate},
		"malformed": {argStartDate: "2026-01-32", argEndDate: scoresEndDate},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newScoresHarness(t, testkit.NewScript())

			advice := h.callError(t, ToolGetHillScore, args)
			assertNoRawPayload(t, advice)
			if got := len(h.fake.Requests()); got != 0 {
				t.Errorf("the fake received %d requests, want none", got)
			}
		})
	}
}

func TestGetHillScoreBoundsTheWindowAtTheRequestLayersLimit(t *testing.T) {
	t.Parallel()

	h := newScoresHarnessWith(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 3})

	advice := h.callError(t, ToolGetHillScore, scoresWindowArgs())
	assertNoRawPayload(t, advice)
	if !strings.Contains(advice, "window") && !strings.Contains(advice, "date") {
		t.Errorf("the refusal %q does not say the window was the problem", advice)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestGetHillScoreCutsAnImplausibleDayList proves the day bound is applied and stated.
// The bound is the request layer's own date-window bound, so the test sets that bound
// and answers with more days than the window can hold.
func TestGetHillScoreCutsAnImplausibleDayList(t *testing.T) {
	t.Parallel()

	limits := client.Limits{MaxDateRangeDays: 60}
	days := make([]string, 0, 62)
	for i := range 62 {
		days = append(days, `{"calendarDate":"`+scoresEndDate+`","overallScore":`+
			strconv.Itoa(i)+`}`)
	}
	body := `{"hillScoreDTOList":[` + strings.Join(days, ",") + `]}`
	script := testkit.NewScript().With(client.PathHillScoreStats,
		testkit.JSON(http.StatusOK, body))
	h := newScoresHarnessWith(t, script, limits)

	result := h.call(t, ToolGetHillScore, scoresWindowArgs())

	if got := number(t, result, "count"); got != float64(limits.MaxDateRangeDays) {
		t.Errorf("count = %v, want the bound %d", got, limits.MaxDateRangeDays)
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true for a cut day list")
	}
}

// TestGetHillScoreReportsAQuietWindowAsEmpty proves an account with no hill score is a
// normal answer.
func TestGetHillScoreReportsAQuietWindowAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathHillScoreStats,
		testkit.JSON(http.StatusOK, `{}`))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetHillScore, scoresWindowArgs())
	if got := number(t, result, "count"); got != 0 {
		t.Errorf("count = %v, want 0", got)
	}
	if got := len(list(t, result, "daily_scores")); got != 0 {
		t.Errorf("daily_scores holds %d entries, want none", got)
	}
}

// TestHillScoreLogValueReportsShapeOnly proves the log record carries no reading.
func TestHillScoreLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	score := 68.5
	value := HillScore{
		PeriodAvgScore: &score,
		DailyScores:    []HillScoreDaily{{Overall: &score}},
		Truncated:      true,
	}.LogValue().String()

	if strings.Contains(value, "68.5") {
		t.Errorf("the log value %q carries a reading", value)
	}
	if !strings.Contains(value, "hillScore") || !strings.Contains(value, "truncated") {
		t.Errorf("the log value %q does not report the shape", value)
	}
}

// TestTrainingScoresContractsMatchTheManifest pins the seven contracts against
// compat/tools.json now, before register.go lists them.
//
// The package contract test covers only what Contracts() returns, and that is built
// from register.go. Until these seven are wired there, this is what keeps their names,
// argument sets, tiers, hints and log categories honest.
func TestTrainingScoresContractsMatchTheManifest(t *testing.T) {
	t.Parallel()

	entries := loadScoresManifest(t)
	for _, entry := range scoresRegistrations() {
		contract := entry.contract()
		t.Run(contract.Spec.Name, func(t *testing.T) {
			t.Parallel()

			manifest, ok := entries[contract.Spec.Name]
			if !ok {
				t.Fatalf("%s is not in the manifest", contract.Spec.Name)
			}
			assertScoresContract(t, contract, manifest)
		})
	}
}

// scoresManifestEntry is the part of a manifest record these tests compare against.
type scoresManifestEntry struct {
	Name        string         `json:"name"`
	InputSchema map[string]any `json:"inputSchema"`
	Sensitivity string         `json:"sensitivity"`
	Effect      string         `json:"effect"`
	Idempotency string         `json:"idempotency"`
}

func loadScoresManifest(t *testing.T) map[string]scoresManifestEntry {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "compat", "tools.json"))
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}
	var manifest struct {
		Tools []scoresManifestEntry `json:"tools"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decoding the manifest: %v", err)
	}

	entries := make(map[string]scoresManifestEntry, len(manifest.Tools))
	for _, entry := range manifest.Tools {
		entries[entry.Name] = entry
	}
	return entries
}

// assertScoresContract checks one contract against its manifest record. The declared
// schema may be stricter — a format, a pattern or a range the manifest does not state
// — but it may not rename, add, drop or retype an argument, or change what is required.
func assertScoresContract(t *testing.T, contract Contract, manifest scoresManifestEntry) {
	t.Helper()

	declared := contract.Schema.JSON()
	if got, want := scoresPropertyNames(declared), scoresPropertyNames(manifest.InputSchema); !slices.Equal(got, want) {
		t.Errorf("input properties drifted: declared %v, manifest %v", got, want)
	}
	if got, want := contract.Schema.Required(), scoresRequired(manifest.InputSchema); !slices.Equal(got, want) {
		t.Errorf("required arguments drifted: declared %v, manifest %v", got, want)
	}
	if additional, ok := declared[keyAdditionalProperties].(bool); !ok || additional {
		t.Error("the declared schema accepts properties it does not declare")
	}

	if got := contract.Spec.Category; got != manifest.Sensitivity {
		t.Errorf("log category = %q, manifest sensitivity = %q", got, manifest.Sensitivity)
	}
	if manifest.Effect != "read-only" || contract.Spec.Tier != policy.TierReadOnly {
		t.Errorf("tier = %v, manifest effect = %q; both must be read-only",
			contract.Spec.Tier, manifest.Effect)
	}

	annotations := contract.Spec.Annotations
	wantIdempotent := manifest.Idempotency == "idempotent"
	if !annotations.ReadOnly || annotations.Destructive || !annotations.OpenWorld ||
		annotations.Idempotent != wantIdempotent {
		t.Errorf("annotations = %+v, want read-only, non-destructive, open-world and "+
			"idempotent=%t", annotations, wantIdempotent)
	}
}

func scoresPropertyNames(schema map[string]any) []string {
	properties, _ := schema[keyProperties].(map[string]any)
	return slices.Sorted(maps.Keys(properties))
}

func scoresRequired(schema map[string]any) []string {
	raw, _ := schema[keyRequired].([]any)
	out := make([]string, 0, len(raw))
	for _, name := range raw {
		if text, ok := name.(string); ok {
			out = append(out, text)
		}
	}
	slices.Sort(out)
	return out
}
