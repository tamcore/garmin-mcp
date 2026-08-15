package tools

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The cardio tools are not in register.go yet — that file belongs to the composition
// slice — so these tests drive the handler bodies directly against the scripted fake
// Garmin service rather than through an MCP session. Every fixture is synthetic: the
// readings are invented and none is a recording of a real account.

const (
	cardioPrincipal   = "principal-cardio-0001"
	cardioDisplayName = "fake-tester"
	cardioDate        = "2026-01-31"
)

const cardioProfileBody = `{"profileId":900001,"displayName":"` + cardioDisplayName + `",` +
	`"fullName":"Fake Tester"}`

const cardioHeartRateBody = `{"userProfilePK":900001,"calendarDate":"` + cardioDate + `",` +
	`"startTimestampGMT":"` + cardioDate + `T07:00:00.0","endTimestampGMT":"` + cardioDate +
	`T23:00:00.0","maxHeartRate":171,"minHeartRate":"48","restingHeartRate":52,` +
	`"lastSevenDaysAvgRestingHeartRate":53,` +
	`"heartRateValueDescriptors":[{"index":0,"key":"timestamp"},{"index":1,"key":"heartrate"}],` +
	`"heartRateValues":[[1786689600000,61],[1786689720000,null],[1786693320000,71]]}`

// cardioCaller is the principal-scoped caller for the fake service. In production this
// is *auth.Refresher; here testkit's Doer enforces the origin, so no credential is in
// play and no test can reach the real service.
type cardioCaller struct {
	doer testkit.Doer
}

func (c cardioCaller) Do(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	if principal == "" {
		return nil, errors.New("cardioCaller: no principal")
	}
	return c.doer.Do(req.WithContext(ctx))
}

// A cardioRoute is one scripted endpoint.
type cardioRoute struct {
	path     string
	behavior testkit.Behavior
}

func cardioJSON(path, body string) cardioRoute {
	return cardioRoute{path: path, behavior: testkit.JSON(http.StatusOK, body)}
}

// cardioScript scripts the profile read every display-name-keyed tool performs, plus
// whatever the test itself needs.
func cardioScript(entries ...cardioRoute) testkit.Script {
	script := testkit.NewScript().With(client.PathSocialProfile,
		testkit.JSON(http.StatusOK, cardioProfileBody))
	for _, entry := range entries {
		script = script.With(entry.path, entry.behavior)
	}
	return script
}

// cardioLeakedReading is the body a failing endpoint answers with. No refusal may
// quote it: a Garmin failure must never carry a reading out of this process.
const cardioLeakedReading = "61 bpm"

// cardioFailure scripts an endpoint that fails with a body carrying a reading.
func cardioFailure(path string) cardioRoute {
	return cardioRoute{path: path, behavior: testkit.Behavior{
		Status: http.StatusInternalServerError,
		Body:   `{"leaked":"` + cardioLeakedReading + `"}`,
	}}
}

// assertSanitizedGarminFailure requires the classified failure and a refusal that
// quotes nothing from the payload.
func assertSanitizedGarminFailure(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, client.ErrServer) {
		t.Fatalf("the read = %v, want a classified server failure", err)
	}
	if strings.Contains(err.Error(), cardioLeakedReading) {
		t.Errorf("the refusal %q quotes the response body, want authored advice only", err)
	}
}

// cardioService builds the handler dependency set over the fake service.
func cardioService(t *testing.T, script testkit.Script) (*service, *testkit.Server) {
	t.Helper()

	fake := testkit.NewServer(t, script)
	rc, err := client.New(client.Config{
		Hosts:   fake.Hosts(protocol.DomainGlobal),
		Sleeper: client.SleeperFunc(func(context.Context, time.Duration) error { return nil }),
		Jitter:  func() float64 { return 0 },
	})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}

	svc, err := newService(Deps{Client: rc, Caller: cardioCaller{doer: fake.Doer()}})
	if err != nil {
		t.Fatalf("newService() = %v", err)
	}
	return svc, fake
}

// cardioContext carries the principal the way the middleware chain does in production.
func cardioContext(t *testing.T) context.Context {
	t.Helper()

	principal, err := identity.NewPrincipal(cardioPrincipal)
	if err != nil {
		t.Fatalf("identity.NewPrincipal() = %v", err)
	}
	return identity.WithPrincipal(t.Context(), principal)
}

func cardioHeartRatePath() string {
	return client.PathDailyHeartRatePrefix + "/" + cardioDisplayName
}

// cardioRegistrations names every register function this slice adds, so the wiring the
// composition slice must perform is visible from the tests too.
func cardioRegistrations() []registration {
	return []registration{
		{getHeartRatesContract, registerGetHeartRates},
		{getHeartRatesSummaryContract, registerGetHeartRatesSummary},
		{getRestingHeartRateDayContract, registerGetRestingHeartRateDay},
		{getRespirationDataContract, registerGetRespirationData},
		{getRespirationSummaryContract, registerGetRespirationSummary},
		{getSpO2DataContract, registerGetSpO2Data},
		{getSleepSummaryContract, registerGetSleepSummary},
		{getBloodPressureContract, registerGetBloodPressure},
		{getHydrationDataContract, registerGetHydrationData},
		{getLifestyleLoggingDataContract, registerGetLifestyleLoggingData},
	}
}

// assertPublishableResult registers a throwaway tool with the SDK for the result type,
// which is what infers and validates the published output schema.
//
// These tools are not in register.go yet, so nothing else in the build exercises that
// inference for them: a result type the SDK cannot describe would otherwise fail for
// the first time on the day the composition slice wires it in.
func assertPublishableResult[Out any](t *testing.T, name string) {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "schema-check", Version: "0.0.0-schemacheck"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: name},
		func(context.Context, *mcp.CallToolRequest, noArguments) (
			*mcp.CallToolResult, Out, error,
		) {
			var zero Out
			return nil, zero, nil
		})
}

func TestCardioResultsHaveAnInferableOutputSchema(t *testing.T) {
	t.Parallel()

	assertPublishableResult[HeartRates](t, ToolGetHeartRates)
	assertPublishableResult[HeartRateSummary](t, ToolGetHeartRatesSummary)
	assertPublishableResult[RestingHeartRateDay](t, ToolGetRestingHeartRateDay)
	assertPublishableResult[Respiration](t, ToolGetRespirationData)
	assertPublishableResult[RespirationSummary](t, ToolGetRespirationSummary)
	assertPublishableResult[SpO2](t, ToolGetSpO2Data)
	assertPublishableResult[SleepSummary](t, ToolGetSleepSummary)
	assertPublishableResult[BloodPressure](t, ToolGetBloodPressure)
	assertPublishableResult[Hydration](t, ToolGetHydrationData)
	assertPublishableResult[LifestyleLog](t, ToolGetLifestyleLoggingData)
}

func TestCardioContractsAreReadOnlyHealthToolsWithAllFourHints(t *testing.T) {
	t.Parallel()

	for _, entry := range cardioRegistrations() {
		contract := entry.contract()
		t.Run(contract.Spec.Name, func(t *testing.T) {
			t.Parallel()

			spec := contract.Spec
			if spec.Category != categoryHealth {
				t.Errorf("category = %q, want %q", spec.Category, categoryHealth)
			}
			if spec.Description == "" || spec.Title == "" {
				t.Error("a tool without a title or a description was declared")
			}
			if want := readOnlyAnnotations(); spec.Annotations != want {
				t.Errorf("annotations = %+v, want %+v", spec.Annotations, want)
			}
			if entry.register == nil {
				t.Error("the tool declares no register function")
			}
		})
	}
}

func TestCardioSchemasDeclareOnlyDateArguments(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{"date": true, "start_date": true, "end_date": true}
	for _, entry := range cardioRegistrations() {
		contract := entry.contract()
		for _, property := range contract.Schema.Properties() {
			if !allowed[property.Name] {
				t.Errorf("%s declares the argument %q: no tool may accept an account selector",
					contract.Spec.Name, property.Name)
			}
			if property.Format != formatDate || property.Pattern != patternCalendarDate {
				t.Errorf("%s.%s declares no calendar-date format or pattern",
					contract.Spec.Name, property.Name)
			}
			if !property.Required {
				t.Errorf("%s.%s is optional, want the manifest's required argument",
					contract.Spec.Name, property.Name)
			}
		}
	}
}

func TestReadHeartRatesReturnsTheDayAndItsSeries(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript(
		cardioJSON(cardioHeartRatePath(), cardioHeartRateBody)))

	got, err := svc.readHeartRates(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readHeartRates() = %v", err)
	}

	if got.Date != cardioDate {
		t.Errorf("Date = %q, want %q", got.Date, cardioDate)
	}
	if !got.HasData {
		t.Error("HasData = false, want true")
	}
	if got.MaxBPM == nil || *got.MaxBPM != 171 {
		t.Errorf("MaxBPM = %v, want 171", got.MaxBPM)
	}
	if got.MinBPM == nil || *got.MinBPM != 48 {
		t.Errorf("MinBPM = %v, want 48 decoded from a numeric string", got.MinBPM)
	}
	if got.SampleCount != 3 || len(got.Samples) != 3 {
		t.Fatalf("SampleCount = %d, want 3", got.SampleCount)
	}
	if got.Samples[1].HeartRateBPM != nil {
		t.Error("the middle sample carries a reading, want the null gap preserved as absent")
	}
	if got.Truncated {
		t.Error("Truncated = true for a three-point series")
	}
	if calls := len(fake.Requests()); calls != 2 {
		t.Errorf("requests = %d, want 2: one profile read and one heart-rate read", calls)
	}
}

func TestReadHeartRatesRefusesAMalformedDateBeforeAnyCall(t *testing.T) {
	t.Parallel()

	svc, fake := cardioService(t, cardioScript())

	_, err := svc.readHeartRates(cardioContext(t), "31-01-2026")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("readHeartRates() with a malformed date = %v, want ErrInvalidArgument", err)
	}
	if calls := len(fake.Requests()); calls != 0 {
		t.Errorf("requests = %d, want 0: a refused argument costs no Garmin call", calls)
	}
}

func TestReadHeartRatesRefusesAnUnattributedRequest(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript())

	_, err := svc.readHeartRates(t.Context(), cardioDate)
	if !errors.Is(err, identity.ErrNoPrincipal) {
		t.Fatalf("readHeartRates() without a principal = %v, want ErrNoPrincipal", err)
	}
}

func TestReadHeartRatesReportsADriftedSeriesAsUninterpretable(t *testing.T) {
	t.Parallel()

	body := `{"calendarDate":"` + cardioDate + `","heartRateValues":{"unexpected":true}}`
	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioHeartRatePath(), body)))

	_, err := svc.readHeartRates(cardioContext(t), cardioDate)
	if !errors.Is(err, client.ErrUnexpectedResponse) {
		t.Fatalf("readHeartRates() with a drifted series = %v, want ErrUnexpectedResponse", err)
	}
}

func TestReadHeartRatesCutsASeriesAtTheBoundAndReportsIt(t *testing.T) {
	t.Parallel()

	body := `{"calendarDate":"` + cardioDate + `","heartRateValues":` +
		repeatedTuples(DefaultMaxHeartRateSamples+5) + `}`
	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioHeartRatePath(), body)))

	got, err := svc.readHeartRates(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readHeartRates() = %v", err)
	}
	if got.SampleCount != DefaultMaxHeartRateSamples {
		t.Errorf("SampleCount = %d, want the bound %d",
			got.SampleCount, DefaultMaxHeartRateSamples)
	}
	if !got.Truncated {
		t.Error("Truncated = false, want the cut reported")
	}
}

// repeatedTuples renders a series of count identical points.
func repeatedTuples(count int) string {
	tuples := make([]string, 0, count)
	for range count {
		tuples = append(tuples, `[1786689600000,61]`)
	}
	return "[" + strings.Join(tuples, ",") + "]"
}

func TestHeartRatesLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	bpm := 171.0
	got := HeartRates{
		Date:        cardioDate,
		HasData:     true,
		MaxBPM:      &bpm,
		Samples:     []HeartRateSample{{HeartRateBPM: &bpm}},
		SampleCount: 1,
	}

	rendered := got.LogValue().String()
	if strings.Contains(rendered, "171") {
		t.Errorf("the log value %q carries a reading, want shape only", rendered)
	}
	if !strings.Contains(rendered, "samples=1") {
		t.Errorf("the log value %q does not report the sample count", rendered)
	}
}

func TestReadHeartRatesReportsAGarminFailureWithoutThePayload(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioFailure(cardioHeartRatePath())))

	_, err := svc.readHeartRates(cardioContext(t), cardioDate)
	assertSanitizedGarminFailure(t, err)
}
