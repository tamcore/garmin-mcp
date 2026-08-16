package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// balanceBody is synthetic and deliberately covers all three band placements: the low
// band is above its range, the high band below its own, and the anaerobic band inside.
const balanceBody = `{"mostRecentTrainingLoadBalance":{"metricsTrainingLoadBalanceDTOMap":{` +
	`"3001":{"calendarDate":"2026-01-31","primaryTrainingDevice":true,` +
	`"trainingBalanceFeedbackPhrase":"AEROBIC_HIGH_SHORTAGE",` +
	`"monthlyLoadAerobicLow":1500.5,"monthlyLoadAerobicLowTargetMin":800.0,` +
	`"monthlyLoadAerobicLowTargetMax":1200.0,"monthlyLoadAerobicHigh":120.5,` +
	`"monthlyLoadAerobicHighTargetMin":200.0,"monthlyLoadAerobicHighTargetMax":400.0,` +
	`"monthlyLoadAnaerobic":90.5,"monthlyLoadAnaerobicTargetMin":50.0,` +
	`"monthlyLoadAnaerobicTargetMax":150.0}}}}`

func balanceScript(body string) testkit.Script {
	return testkit.NewScript().With(client.PathTrainingStatusPrefix+"/"+trendEnd,
		testkit.JSON(http.StatusOK, body))
}

func TestTrainingLoadBalancePlacesEachBandAgainstItsTargetRange(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, balanceScript(balanceBody))
	out, err := h.svc.readTrainingLoadBalance(h.ctx, trendEnd)
	if err != nil {
		t.Fatalf("readTrainingLoadBalance() = %v", err)
	}

	if !out.HasData || out.Feedback != "AEROBIC_HIGH_SHORTAGE" {
		t.Fatalf("result = %+v, want data and Garmin's feedback phrase", out)
	}
	if out.AerobicLow == nil || out.AerobicLow.Status != bandAbove {
		t.Errorf("aerobic_low = %+v, want above its range", out.AerobicLow)
	}
	if out.AerobicHigh == nil || out.AerobicHigh.Status != bandBelow {
		t.Errorf("aerobic_high = %+v, want below its range", out.AerobicHigh)
	}
	if out.Anaerobic == nil || out.Anaerobic.Status != bandWithin {
		t.Errorf("anaerobic = %+v, want within its range", out.Anaerobic)
	}
	if out.Date != trendEnd {
		t.Errorf("date = %q, want Garmin's own calendar date", out.Date)
	}
}

// TestTrainingLoadBalanceLeavesTheStatusUnsetWithoutBothEdges keeps the derived
// placement from being guessed against a missing bound.
func TestTrainingLoadBalanceLeavesTheStatusUnsetWithoutBothEdges(t *testing.T) {
	t.Parallel()

	body := `{"mostRecentTrainingLoadBalance":{"metricsTrainingLoadBalanceDTOMap":{` +
		`"3001":{"monthlyLoadAerobicLow":1500.5,"monthlyLoadAerobicLowTargetMin":800.0}}}}`
	h := newTrendHarness(t, balanceScript(body))

	out, err := h.svc.readTrainingLoadBalance(h.ctx, trendEnd)
	if err != nil {
		t.Fatalf("readTrainingLoadBalance() = %v", err)
	}
	if out.AerobicLow == nil || out.AerobicLow.Status != "" {
		t.Errorf("aerobic_low = %+v, want no status without both edges", out.AerobicLow)
	}
	if out.AerobicHigh != nil || out.Anaerobic != nil {
		t.Error("a band Garmin sent nothing for was reported anyway")
	}
}

func TestTrainingLoadBalanceReportsADayWithNoBalance(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, balanceScript(`{"mostRecentTrainingLoadBalance":null}`))
	out, err := h.svc.readTrainingLoadBalance(h.ctx, trendEnd)
	if err != nil {
		t.Fatalf("readTrainingLoadBalance() = %v", err)
	}
	if out.HasData || out.AerobicLow != nil {
		t.Errorf("result = %+v, want has_data false", out)
	}
}

func TestTrainingLoadBalanceRefusesABadDateAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, balanceScript(balanceBody))
	if _, err := h.svc.readTrainingLoadBalance(h.ctx, "2026-13-01"); !errors.Is(
		err, ErrInvalidArgument) {
		t.Errorf("an unreal date = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.readTrainingLoadBalance(t.Context(), trendEnd); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestTrainingLoadBalanceResultNeverLogsALoad(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, balanceScript(balanceBody))
	out, err := h.svc.readTrainingLoadBalance(h.ctx, trendEnd)
	if err != nil {
		t.Fatalf("readTrainingLoadBalance() = %v", err)
	}
	assertShapeOnly(t, "TrainingLoadBalance", out,
		"1500.5", "120.5", "90.5", "AEROBIC_HIGH_SHORTAGE")
}
