package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func reloadPath() string { return client.PathEpochReloadRequestPrefix + "/" + trendEnd }

func reloadScript(behavior testkit.Behavior) testkit.Script {
	return testkit.NewScript().With(reloadPath(), behavior)
}

func TestRequestReloadPostsTheDayAndAcknowledgesIt(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, reloadScript(testkit.JSON(http.StatusOK, `{"accepted":true}`)))
	out, err := h.svc.requestReload(h.ctx, trendEnd)
	if err != nil {
		t.Fatalf("requestReload() = %v", err)
	}

	if !out.Requested || out.Date != trendEnd || out.Status != http.StatusOK {
		t.Errorf("result = %+v, want an accepted reload for %s", out, trendEnd)
	}
	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if requests[0].Method != http.MethodPost || requests[0].Path != reloadPath() {
		t.Errorf("request = %s %s, want a POST to the epoch reload path",
			requests[0].Method, requests[0].Path)
	}
	if len(requests[0].Body) != 0 {
		t.Errorf("the reload carried %d body bytes, want none", len(requests[0].Body))
	}
}

func TestRequestReloadAcceptsAnEmptyAcknowledgement(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, reloadScript(testkit.Behavior{Status: http.StatusNoContent}))
	out, err := h.svc.requestReload(h.ctx, trendEnd)
	if err != nil {
		t.Fatalf("requestReload() = %v", err)
	}
	if !out.Requested || out.Status != http.StatusNoContent {
		t.Errorf("result = %+v, want a 204 to count as accepted", out)
	}
}

// TestRequestReloadIsRegisteredInTheWriteTier pins the tier and the annotation set:
// the write tier needs transport-specific authorization and no confirmation,
// because the call is not destructive.
func TestRequestReloadIsRegisteredInTheWriteTier(t *testing.T) {
	t.Parallel()

	contract := requestReloadContract()
	if contract.Spec.Tier != policy.TierWrite {
		t.Errorf("tier = %v, want the write tier", contract.Spec.Tier)
	}
	annotations := contract.Spec.Annotations
	if annotations.ReadOnly || annotations.Destructive {
		t.Errorf("annotations = %+v, want read-only false and destructive false", annotations)
	}
	if !annotations.Idempotent {
		t.Error("idempotent = false; a second reload re-triggers the same recompute")
	}
	if !annotations.OpenWorld {
		t.Error("open-world = false; Garmin is an open-world API")
	}
	if contract.Spec.Category != categoryHealth {
		t.Errorf("category = %q, want the health category", contract.Spec.Category)
	}
}

func TestRequestReloadRefusesABadDateAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, reloadScript(testkit.JSON(http.StatusOK, `{}`)))
	if _, err := h.svc.requestReload(h.ctx, "yesterday"); !errors.Is(
		err, ErrInvalidArgument) {
		t.Errorf("a malformed date = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.requestReload(t.Context(), trendEnd); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestRequestReloadReportsGarminsRefusalAsAdvice(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, reloadScript(
		testkit.JSON(http.StatusNotFound, `{"error":"synthetic"}`)))
	_, err := h.svc.requestReload(h.ctx, trendEnd)
	if !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("requestReload() = %v, want ErrNotFound", err)
	}
	if err.Error() != AdviceNoSuchRecord {
		t.Errorf("advice = %q, want the authored no-record advice", err.Error())
	}
}

func TestReloadResultNeverLogsTheDay(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, reloadScript(testkit.JSON(http.StatusOK, `{"accepted":true}`)))
	out, err := h.svc.requestReload(h.ctx, trendEnd)
	if err != nil {
		t.Fatalf("requestReload() = %v", err)
	}
	assertShapeOnly(t, "ReloadRequest", out, trendEnd)
}
