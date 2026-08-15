package tools

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The synthetic ride the FIT tool tests decode.
const (
	fitTestPrincipal = "principal-fit-0001"
	fitTestActivity  = 18446744
	fitTestPower     = 220
	fitTestCadence   = 88
	fitTestHeartRate = 145
)

// fitCaller is the principal-scoped caller for the fake service. No credential is in
// play: testkit's Doer enforces the origin, so no test here can reach Garmin.
type fitCaller struct {
	doer testkit.Doer
}

func (c fitCaller) Do(ctx context.Context, principal string, req *http.Request) (
	*http.Response, error,
) {
	if principal == "" {
		return nil, errors.New("fitCaller: no principal")
	}
	return c.doer.Do(req.WithContext(ctx))
}

// fitHarness drives a handler directly, because these tools are not wired into
// register.go yet: the tier lists are the composition root's, not this file's.
type fitHarness struct {
	svc  *service
	fake *testkit.Server
	ctx  context.Context
}

func newFITHarness(t *testing.T, script testkit.Script, bounds Bounds) fitHarness {
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
	svc, err := newService(Deps{Client: rc, Caller: fitCaller{doer: fake.Doer()}, Bounds: bounds})
	if err != nil {
		t.Fatalf("newService() = %v", err)
	}

	principal, err := identity.NewPrincipal(fitTestPrincipal)
	if err != nil {
		t.Fatalf("identity.NewPrincipal() = %v", err)
	}
	return fitHarness{
		svc:  svc,
		fake: fake,
		ctx:  identity.WithPrincipal(t.Context(), principal),
	}
}

// fitDownloadPath is the path one activity's original file is served from.
func fitDownloadPath(id int64) string {
	return client.PathActivityOriginalDownload + "/" + strconv.FormatInt(id, 10)
}

// fitRide builds the synthetic cycling file the tool tests download.
func fitRide(seconds int, watts int) []byte {
	samples := make([]testkit.FITSample, 0, seconds)
	for second := range seconds {
		samples = append(samples, testkit.FITSample{
			Second:      second,
			Power:       new(watts),
			Cadence:     new(fitTestCadence),
			HeartRate:   new(fitTestHeartRate),
			Altitude:    new(100 + float64(second)),
			Distance:    new(10 * float64(second)),
			Grade:       new(float64(10)),
			Temperature: new(19),
		})
	}
	file := testkit.FITFile{Sport: 2, Session: true, Samples: samples, Shifts: []testkit.FITShiftFixture{
		{Second: 30, RearGear: 7, FrontGear: 2},
	}}
	return testkit.ZipFIT("activity.fit", file.Bytes())
}

// fitBehavior scripts one downloaded activity file.
func fitBehavior(body []byte) testkit.Behavior {
	return testkit.Behavior{
		Status:      http.StatusOK,
		ContentType: downloadMediaTypes()[api.FormatOriginal],
		Body:        string(body),
	}
}

// fitScript scripts the download of the test activity.
func fitScript(body []byte) testkit.Script {
	return testkit.NewScript().With(fitDownloadPath(fitTestActivity), fitBehavior(body))
}

// TestFITDataAnalysesTheDownloadedFile is the whole-path test: the archive is
// downloaded, unpacked, decoded and summarized.
func TestFITDataAnalysesTheDownloadedFile(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, fitScript(fitRide(600, fitTestPower)), Bounds{})
	data, err := h.svc.activityFITData(h.ctx, activityFITInput{ActivityID: int64(fitTestActivity)})
	if err != nil {
		t.Fatalf("activityFITData() = %v", err)
	}

	if data.ActivityID != fitTestActivity {
		t.Errorf("activity id = %d, want %d", data.ActivityID, fitTestActivity)
	}
	if data.Sport == nil || *data.Sport != defaultCurveActivityType {
		t.Errorf("sport = %v, want cycling", data.Sport)
	}
	if data.Overall.AveragePower == nil || *data.Overall.AveragePower != fitTestPower {
		t.Errorf("average power = %v, want %d", data.Overall.AveragePower, fitTestPower)
	}
	if len(data.Sessions) != 1 || len(data.Curve) == 0 || len(data.Climbs) != 1 {
		t.Errorf("sessions %d, curve %d, climbs %d: want one session, a curve and one climb",
			len(data.Sessions), len(data.Curve), len(data.Climbs))
	}
	if data.Shifts.Total != 1 || len(data.Shifts.Events) != 1 {
		t.Errorf("shifts = %+v, want the one gear change", data.Shifts)
	}
	if data.Overall.Dynamics != nil {
		t.Errorf("dynamics = %+v, want none from a file without pedal metrics", data.Overall.Dynamics)
	}
}

// TestFITDataReportsAnAbandonedAnalysis proves the caller's context reaches past the
// download and into the analysis.
//
// The transfer is not the expensive half of this tool. The analysis is: every retained
// session and lap is summarized against the whole retained sample stream, so its cost is
// set by the file rather than by the request. A deadline that stopped at the download
// would bound the part Garmin controls and then wait out the part an attacker's file
// controls. The refusal is the sanitized tool error, and it names no file and no
// identifier.
func TestFITDataReportsAnAbandonedAnalysis(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, fitScript(fitRide(600, fitTestPower)), Bounds{})
	activity, err := api.ParseFITActivity(h.ctx, fitRide(60, fitTestPower), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}

	ctx, cancel := context.WithCancel(h.ctx)
	cancel()
	if _, err := newFITData(ctx, fitTestActivity, 1, activity, false); err == nil {
		t.Fatal("newFITData() = nil, want the abandoned analysis reported")
	}
}

// TestFITDataOmitsTheSeriesUnlessAsked proves the per-second series is opt-in, which
// is what keeps the default result small.
func TestFITDataOmitsTheSeriesUnlessAsked(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, fitScript(fitRide(120, fitTestPower)), Bounds{})
	without, err := h.svc.activityFITData(h.ctx, activityFITInput{ActivityID: int64(fitTestActivity)})
	if err != nil {
		t.Fatalf("activityFITData() = %v", err)
	}
	if len(without.Records) != 0 || without.RecordsIncluded {
		t.Errorf("records = %d, want none by default", len(without.Records))
	}

	asked := true
	with, err := h.svc.activityFITData(h.ctx, activityFITInput{
		ActivityID: int64(fitTestActivity), IncludeRecords: &asked,
	})
	if err != nil {
		t.Fatalf("activityFITData() = %v", err)
	}
	if len(with.Records) != 120 || !with.RecordsIncluded {
		t.Fatalf("records = %d, want the 120 samples", len(with.Records))
	}
	if with.Records[10].OffsetSecs != 10 || with.Records[10].Power == nil {
		t.Errorf("record = %+v, want the tenth second with its power", with.Records[10])
	}
}

// TestFITRecordViewsAreBounded proves the series is cut at the bound and says so,
// rather than returning a whole ride's worth of samples.
func TestFITRecordViewsAreBounded(t *testing.T) {
	t.Parallel()

	records := make([]api.FITRecord, 0, 10)
	base := time.Date(2026, time.January, 2, 8, 0, 0, 0, time.UTC)
	for second := range 10 {
		records = append(records, api.FITRecord{Time: base.Add(time.Duration(second) * time.Second)})
	}

	views, truncated := newFITRecordViews(records, 4)
	if len(views) != 4 || !truncated {
		t.Errorf("%d views truncated=%v, want 4 and true", len(views), truncated)
	}
	if views, truncated := newFITRecordViews(records, 100); len(views) != 10 || truncated {
		t.Errorf("%d views truncated=%v, want 10 and false", len(views), truncated)
	}
	if views, truncated := newFITRecordViews(nil, 4); len(views) != 0 || truncated {
		t.Errorf("%d views truncated=%v, want none and false", len(views), truncated)
	}
}

// TestFITDataRefusesAFileOverTheDownloadBound proves the transfer bound refuses the
// file rather than analysing a truncated one, which would be a wrong ride.
func TestFITDataRefusesAFileOverTheDownloadBound(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, fitScript(fitRide(600, fitTestPower)), Bounds{MaxDownloadBytes: 128})
	_, err := h.svc.activityFITData(h.ctx, activityFITInput{ActivityID: int64(fitTestActivity)})
	if !errors.Is(err, ErrResultTooLarge) {
		t.Fatalf("activityFITData() = %v, want ErrResultTooLarge", err)
	}
}

// TestFITDataSanitizesADecodeFailure proves a body this server cannot decode comes
// back as authored advice, never as the bytes Garmin sent.
func TestFITDataSanitizesADecodeFailure(t *testing.T) {
	t.Parallel()

	const secret = "NOT-A-FIT-FILE-abc123"
	script := testkit.NewScript().With(fitDownloadPath(fitTestActivity), testkit.Behavior{
		Status:      http.StatusOK,
		ContentType: downloadMediaTypes()[api.FormatOriginal],
		Body:        secret,
	})
	h := newFITHarness(t, script, Bounds{})

	_, err := h.svc.activityFITData(h.ctx, activityFITInput{ActivityID: int64(fitTestActivity)})
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Fatalf("activityFITData() = %v, want ErrMalformedPayload", err)
	}
	if got := err.Error(); got == "" || contains(got, secret) {
		t.Errorf("advice %q reveals the response body", got)
	}
}

// contains reports whether text carries the needle.
func contains(text, needle string) bool {
	return len(needle) > 0 && len(text) >= len(needle) && indexOf(text, needle) >= 0
}

func indexOf(text, needle string) int {
	for index := 0; index+len(needle) <= len(text); index++ {
		if text[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}

// TestFITDataRefusesABadIdentifierBeforeAnyCall proves the argument is validated
// before Garmin is asked for anything.
func TestFITDataRefusesABadIdentifierBeforeAnyCall(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, testkit.NewScript(), Bounds{})
	for name, value := range map[string]any{
		"empty":    nil,
		"negative": float64(-1),
		"text":     "../../etc/passwd",
	} {
		if _, err := h.svc.activityFITData(h.ctx, activityFITInput{ActivityID: value}); !errors.Is(
			err, ErrInvalidArgument) {
			t.Errorf("%s identifier = %v, want ErrInvalidArgument", name, err)
		}
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestFITDataNeedsAPrincipal proves the account comes from the request context and
// that a request without one is refused before any Garmin call.
func TestFITDataNeedsAPrincipal(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, fitScript(fitRide(60, fitTestPower)), Bounds{})
	_, err := h.svc.activityFITData(context.Background(), activityFITInput{
		ActivityID: int64(fitTestActivity),
	})
	if !errors.Is(err, identity.ErrNoPrincipal) {
		t.Fatalf("activityFITData() without a principal = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestFITDataReportsAMissingActivity keeps the upstream failure classes distinct
// through the download and decode path.
func TestFITDataReportsAMissingActivity(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(fitDownloadPath(fitTestActivity),
		testkit.JSON(http.StatusNotFound, `{"error":"no such activity"}`))
	h := newFITHarness(t, script, Bounds{})

	_, err := h.svc.activityFITData(h.ctx, activityFITInput{ActivityID: int64(fitTestActivity)})
	if !errors.Is(err, client.ErrNotFound) {
		t.Errorf("activityFITData() = %v, want ErrNotFound", err)
	}
}

// TestFITDataContractDeclaresItsBounds pins the published contract: the manifest's
// two arguments, the read-only tier, and all four annotation hints.
func TestFITDataContractDeclaresItsBounds(t *testing.T) {
	t.Parallel()

	contract := getActivityFITDataContract()
	if contract.Spec.Name != "get_activity_fit_data" {
		t.Errorf("name = %q, want the upstream compatibility name", contract.Spec.Name)
	}
	hints := contract.Spec.Annotations
	if !hints.ReadOnly || hints.Destructive || !hints.Idempotent || !hints.OpenWorld {
		t.Errorf("annotations = %+v, want a read-only idempotent open-world tool", hints)
	}

	schema := contract.Schema.JSON()
	properties, ok := schema[keyProperties].(map[string]any)
	if !ok || len(properties) != 2 {
		t.Fatalf("properties = %v, want activity_id and include_records", schema[keyProperties])
	}
	if _, ok := properties["include_records"]; !ok {
		t.Error("the schema declares no include_records argument")
	}
	wantRequired := activityIDProperty().Name
	if required, _ := schema[keyRequired].([]string); len(required) != 1 || required[0] != wantRequired {
		t.Errorf("required = %v, want activity_id alone", schema[keyRequired])
	}
	if schema[keyAdditionalProperties] != false {
		t.Error("the schema accepts properties it does not declare")
	}
}

// TestTheFITToolsDeclareTheirRegistrationFunctions keeps the two register functions
// reachable while register.go is not yet wired: a tool file owns its registration
// function, and the composition root adds the call in tier order.
func TestTheFITToolsDeclareTheirRegistrationFunctions(t *testing.T) {
	t.Parallel()

	registrations := []func(*mcpserver.Registry, *service) error{
		registerGetActivityFITData,
		registerGetPowerDurationCurve,
	}
	for index, register := range registrations {
		if register == nil {
			t.Errorf("registration %d is nil", index)
		}
	}
}
