package tools

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The two synthetic rides the curve scan folds together.
const (
	curveWeakActivity   = 9001
	curveStrongActivity = 9002
	curveWeakWatts      = 200
	curveStrongWatts    = 250
	curveRideSeconds    = 1250
)

// curveListing renders the activity listing the scan walks.
func curveListing(ids ...int64) string {
	entries := make([]string, 0, len(ids))
	for index, id := range ids {
		entries = append(entries, `{"activityId":`+strconv.FormatInt(id, 10)+
			`,"activityName":"Synthetic ride `+strconv.Itoa(index)+`"`+
			`,"startTimeGMT":"2026-01-31 05:12:00"`+
			`,"activityType":{"typeKey":"cycling"}}`)
	}
	return "[" + strings.Join(entries, ",") + "]"
}

// curveScript scripts the listing and both downloads.
func curveScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathActivitySearch,
			testkit.JSON(http.StatusOK, curveListing(curveWeakActivity, curveStrongActivity))).
		With(fitDownloadPath(curveWeakActivity), fitBehavior(fitRide(curveRideSeconds, curveWeakWatts))).
		With(fitDownloadPath(curveStrongActivity), fitBehavior(fitRide(curveRideSeconds, curveStrongWatts)))
}

// TestPowerCurveKeepsTheBestEffortPerDuration is the whole-path test: two rides are
// downloaded and the stronger one owns every season best.
func TestPowerCurveKeepsTheBestEffortPerDuration(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, curveScript(), Bounds{})
	curve, err := h.svc.powerDurationCurve(h.ctx, powerCurveInput{})
	if err != nil {
		t.Fatalf("powerDurationCurve() = %v", err)
	}

	if curve.Analyzed != 2 || curve.Skipped != 0 {
		t.Errorf("analyzed %d skipped %d, want 2 and 0", curve.Analyzed, curve.Skipped)
	}
	if curve.ActivityType != defaultCurveActivityType {
		t.Errorf("activity type = %q, want the default cycling filter", curve.ActivityType)
	}
	if len(curve.SeasonBests) != 6 {
		t.Fatalf("season bests = %+v, want the six windows a 1250 second ride fits", curve.SeasonBests)
	}
	for _, best := range curve.SeasonBests {
		if best.Watts != curveStrongWatts || best.ActivityID != curveStrongActivity {
			t.Errorf("%s best = %+v, want the stronger ride's %d watts", best.Label, best, curveStrongWatts)
		}
		if best.ActivityName == nil || best.StartTime == nil {
			t.Errorf("%s best = %+v, want the activity it came from named", best.Label, best)
		}
	}
}

// TestPowerCurveEstimatesThresholdFromTheTwentyMinuteBest pins the estimate and the
// note that says it is an estimate.
func TestPowerCurveEstimatesThresholdFromTheTwentyMinuteBest(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, curveScript(), Bounds{})
	curve, err := h.svc.powerDurationCurve(h.ctx, powerCurveInput{})
	if err != nil {
		t.Fatalf("powerDurationCurve() = %v", err)
	}

	if curve.FTPEstimate == nil || *curve.FTPEstimate != curveStrongWatts*ftpFactor {
		t.Errorf("ftp estimate = %v, want %v", curve.FTPEstimate, curveStrongWatts*ftpFactor)
	}
	if curve.FTPNote == "" {
		t.Error("the estimate carries no note saying how it was derived")
	}
}

// TestPowerCurveSkipsAFileItCannotRead proves one unreadable activity is counted and
// stepped over rather than failing the whole scan.
func TestPowerCurveSkipsAFileItCannotRead(t *testing.T) {
	t.Parallel()

	script := curveScript().With(fitDownloadPath(curveWeakActivity),
		testkit.JSON(http.StatusNotFound, `{"error":"no such activity"}`))
	h := newFITHarness(t, script, Bounds{})

	curve, err := h.svc.powerDurationCurve(h.ctx, powerCurveInput{})
	if err != nil {
		t.Fatalf("powerDurationCurve() = %v", err)
	}
	if curve.Analyzed != 1 || curve.Skipped != 1 {
		t.Errorf("analyzed %d skipped %d, want 1 and 1", curve.Analyzed, curve.Skipped)
	}
	if len(curve.SeasonBests) == 0 {
		t.Error("season bests are empty, want the readable ride's efforts")
	}
}

// TestPowerCurveStopsOnARejectedSession proves a failure about the whole session
// stops the scan instead of being counted as fifty skips.
func TestPowerCurveStopsOnARejectedSession(t *testing.T) {
	t.Parallel()

	script := curveScript().With(fitDownloadPath(curveWeakActivity),
		testkit.JSON(http.StatusUnauthorized, `{"error":"expired"}`))
	h := newFITHarness(t, script, Bounds{})

	if _, err := h.svc.powerDurationCurve(h.ctx, powerCurveInput{}); !errors.Is(
		err, client.ErrAuthentication) {
		t.Fatalf("powerDurationCurve() = %v, want ErrAuthentication", err)
	}
}

// TestPowerCurveSkipsAListingWithoutAnIdentifier keeps a listing entry this server
// cannot address from reaching a URL path.
func TestPowerCurveSkipsAListingWithoutAnIdentifier(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivitySearch,
		testkit.JSON(http.StatusOK, `[{"activityName":"nameless"}]`))
	h := newFITHarness(t, script, Bounds{})

	curve, err := h.svc.powerDurationCurve(h.ctx, powerCurveInput{})
	if err != nil {
		t.Fatalf("powerDurationCurve() = %v", err)
	}
	if curve.Analyzed != 0 || curve.Skipped != 1 {
		t.Errorf("analyzed %d skipped %d, want 0 and 1", curve.Analyzed, curve.Skipped)
	}
}

// TestPowerCurveRefusesAnOutOfRangeCount proves the manifest's maximum is enforced
// before any Garmin call, which is what bounds the fan-out.
func TestPowerCurveRefusesAnOutOfRangeCount(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, curveScript(), Bounds{})
	for name, count := range map[string]int{"zero": 0, "negative": -5, "too many": 51} {
		value := count
		if _, err := h.svc.powerDurationCurve(h.ctx, powerCurveInput{
			NumActivities: &value,
		}); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%s count = %v, want ErrInvalidArgument", name, err)
		}
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestPowerCurveRefusesAnUnusableActivityType keeps an unvalidated filter out of a
// query string.
func TestPowerCurveRefusesAnUnusableActivityType(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, curveScript(), Bounds{})
	_, err := h.svc.powerDurationCurve(h.ctx, powerCurveInput{ActivityType: "Cycling; DROP"})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("powerDurationCurve() = %v, want ErrInvalidArgument", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestResolveCurveCountAppliesTheManifestDefault pins the default the schema
// declares against the one the handler enforces.
func TestResolveCurveCountAppliesTheManifestDefault(t *testing.T) {
	t.Parallel()

	count, err := resolveCurveCount(nil)
	if err != nil || count != defaultCurveActivities {
		t.Errorf("resolveCurveCount(nil) = %d, %v, want %d", count, err, defaultCurveActivities)
	}
}

// TestDurationLabelsMatchTheUpstreamNames pins the labels the upstream description
// uses, since a caller reads the curve by them.
func TestDurationLabelsMatchTheUpstreamNames(t *testing.T) {
	t.Parallel()

	for seconds, want := range map[int]string{
		5: "5s", 30: "30s", 60: "1min", 300: "5min", 1200: "20min", 3600: "60min",
	} {
		if got := durationLabel(seconds); got != want {
			t.Errorf("durationLabel(%d) = %q, want %q", seconds, got, want)
		}
	}
}

// TestPowerCurveContractDeclaresItsBounds pins the published contract: the two
// manifest arguments with their defaults and ranges, and all four hints.
func TestPowerCurveContractDeclaresItsBounds(t *testing.T) {
	t.Parallel()

	contract := getPowerDurationCurveContract()
	if contract.Spec.Name != "get_power_duration_curve" {
		t.Errorf("name = %q, want the upstream compatibility name", contract.Spec.Name)
	}
	hints := contract.Spec.Annotations
	if !hints.ReadOnly || hints.Destructive || !hints.Idempotent || !hints.OpenWorld {
		t.Errorf("annotations = %+v, want a read-only idempotent open-world tool", hints)
	}

	properties, ok := contract.Schema.JSON()[keyProperties].(map[string]any)
	if !ok || len(properties) != 2 {
		t.Fatalf("properties = %v, want num_activities and activity_type", properties)
	}
	count, ok := properties["num_activities"].(map[string]any)
	if !ok {
		t.Fatal("the schema declares no num_activities argument")
	}
	if count[keyDefault] != defaultCurveActivities || count[keyMaximum] != float64(maxCurveActivities) {
		t.Errorf("num_activities = %v, want the manifest default and maximum", count)
	}
	if len(contract.Schema.Required()) != 0 {
		t.Errorf("required = %v, want no required argument", contract.Schema.Required())
	}
}

// TestPowerCurveLogsItsShapeOnly proves the result model logs counts, never watts.
func TestPowerCurveLogsItsShapeOnly(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, curveScript(), Bounds{})
	curve, err := h.svc.powerDurationCurve(h.ctx, powerCurveInput{})
	if err != nil {
		t.Fatalf("powerDurationCurve() = %v", err)
	}
	if rendered := curve.LogValue().String(); contains(rendered, strconv.Itoa(curveStrongWatts)) {
		t.Errorf("log value %q carries a reading", rendered)
	}
}
