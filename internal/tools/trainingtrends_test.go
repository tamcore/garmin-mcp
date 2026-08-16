package tools

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// These tests drive the trend handlers directly rather than over an MCP session,
// because register.go does not yet carry them. Everything below the handler — the
// domain client, the request layer and the fake Garmin service — is the real thing.
const (
	trendStart = "2026-01-29"
	trendMid   = "2026-01-30"
	trendEnd   = "2026-01-31"
)

// trendHarness is a service over a scripted fake Garmin service, plus a context that
// carries a principal the way the middleware would.
type trendHarness struct {
	svc  *service
	ctx  context.Context
	fake *testkit.Server
}

func newTrendHarness(t *testing.T, script testkit.Script) trendHarness {
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
	svc, err := newService(Deps{Client: rc, Caller: harnessCaller{doer: fake.Doer()}})
	if err != nil {
		t.Fatalf("newService() = %v", err)
	}

	principal, err := identity.NewPrincipal(harnessPrincipal)
	if err != nil {
		t.Fatalf("identity.NewPrincipal() = %v", err)
	}
	return trendHarness{
		svc:  svc,
		ctx:  identity.WithPrincipal(t.Context(), principal),
		fake: fake,
	}
}

// dailyScript serves body for every named day under prefix.
func trendDailyScript(prefix string, days []string, body string) testkit.Script {
	script := testkit.NewScript()
	for _, day := range days {
		script = script.With(prefix+"/"+day, testkit.JSON(http.StatusOK, body))
	}
	return script
}

func allTrendDays() []string { return []string{trendStart, trendMid, trendEnd} }

// TestResolveTrendWindowRefusesAWindowPastTheToolsBound proves the day bound is
// enforced before any Garmin call, which is the whole point of having one.
func TestResolveTrendWindowRefusesAWindowPastTheToolsBound(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, testkit.NewScript())
	_, err := h.svc.resolveTrendWindow(h.ctx, "2026-01-01", "2026-03-01", MaxHRVTrendDays)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("resolveTrendWindow() = %v, want ErrInvalidArgument", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestResolveTrendWindowRefusesAReversedOrMalformedWindow(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, testkit.NewScript())
	cases := map[string][2]string{
		"reversed":  {trendEnd, trendStart},
		"malformed": {"31-01-2026", trendEnd},
		"unreal":    {"2026-02-31", trendEnd},
	}

	for name, window := range cases {
		if _, err := h.svc.resolveTrendWindow(h.ctx, window[0], window[1],
			MaxHRVTrendDays); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%s window = %v, want ErrInvalidArgument", name, err)
		}
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestWalkTrendDaysMarksAPartialResult is the partial-answer rule: a failed day is
// counted and named, the walk continues, and the result says it is incomplete.
func TestWalkTrendDaysMarksAPartialResult(t *testing.T) {
	t.Parallel()

	span := trendSpan(t)
	read := func(_ context.Context, day client.Date) (bool, error) {
		if day.String() == trendMid {
			return false, client.ErrServer
		}
		return true, nil
	}

	coverage, err := walkTrendDays(t.Context(), span, read)
	if err != nil {
		t.Fatalf("walkTrendDays() = %v", err)
	}
	if coverage.Complete {
		t.Error("coverage reports complete, but one day failed")
	}
	if coverage.DaysWithData != 2 || coverage.DaysFailed != 1 {
		t.Errorf("coverage = %+v, want 2 read and 1 failed", coverage)
	}
	if len(coverage.Failures) != 1 || coverage.Failures[0].Date != trendMid {
		t.Fatalf("failures = %+v, want the failing day named", coverage.Failures)
	}
	if coverage.Failures[0].Advice == "" {
		t.Error("the failing day carries no advice")
	}
}

// TestWalkTrendDaysCountsDaysGarminHoldsNothingFor keeps "no data" apart from
// "failed", which is what stops a short trend from looking complete.
func TestWalkTrendDaysCountsDaysGarminHoldsNothingFor(t *testing.T) {
	t.Parallel()

	span := trendSpan(t)
	read := func(_ context.Context, day client.Date) (bool, error) {
		if day.String() == trendStart {
			return false, client.ErrNotFound
		}
		return day.String() == trendEnd, nil
	}

	coverage, err := walkTrendDays(t.Context(), span, read)
	if err != nil {
		t.Fatalf("walkTrendDays() = %v", err)
	}
	if !coverage.Complete {
		t.Error("a window with no readings but no failures is complete")
	}
	if coverage.DaysWithoutData != 2 || coverage.DaysWithData != 1 {
		t.Errorf("coverage = %+v, want 2 without data and 1 with", coverage)
	}
	if len(coverage.Failures) != 0 {
		t.Errorf("failures = %+v, want none", coverage.Failures)
	}
}

// TestWalkTrendDaysStopsOnARateLimit proves the walk does not spend the rest of the
// window collecting the same refusal.
func TestWalkTrendDaysStopsOnARateLimit(t *testing.T) {
	t.Parallel()

	span := trendSpan(t)
	calls := 0
	read := func(_ context.Context, day client.Date) (bool, error) {
		calls++
		if day.String() == trendMid {
			return false, client.ErrRateLimited
		}
		return true, nil
	}

	coverage, err := walkTrendDays(t.Context(), span, read)
	if err != nil {
		t.Fatalf("walkTrendDays() = %v", err)
	}
	if calls != 2 {
		t.Errorf("the walk made %d reads, want it to stop at the rate limit", calls)
	}
	if !coverage.StoppedEarly || coverage.Complete {
		t.Errorf("coverage = %+v, want stopped early and incomplete", coverage)
	}
	if coverage.StopReason == "" {
		t.Error("a stopped walk reports no reason")
	}
}

// TestWalkTrendDaysFailsWhenNothingCouldBeRead refuses to answer with an empty trend
// that would read as "the account has no data".
func TestWalkTrendDaysFailsWhenNothingCouldBeRead(t *testing.T) {
	t.Parallel()

	span := trendSpan(t)
	read := func(context.Context, client.Date) (bool, error) { return false, client.ErrServer }

	if _, err := walkTrendDays(t.Context(), span, read); !errors.Is(err, client.ErrServer) {
		t.Fatalf("walkTrendDays() = %v, want the underlying failure", err)
	}
}

// TestWalkTrendDaysAbortsOnCancellation ends the call rather than reporting a partial
// answer nobody is waiting for any more.
func TestWalkTrendDaysAbortsOnCancellation(t *testing.T) {
	t.Parallel()

	span := trendSpan(t)
	read := func(context.Context, client.Date) (bool, error) { return false, context.Canceled }

	if _, err := walkTrendDays(t.Context(), span, read); !errors.Is(err, context.Canceled) {
		t.Fatalf("walkTrendDays() = %v, want context.Canceled", err)
	}
}

func TestMeanOfReportsNothingForAnEmptySeries(t *testing.T) {
	t.Parallel()

	if got := meanOf(nil); got != nil {
		t.Errorf("meanOf(nil) = %v, want nil", *got)
	}
	got := meanOf([]float64{1, 2, 3})
	if got == nil || *got != 2 {
		t.Errorf("meanOf([1 2 3]) = %v, want 2", got)
	}
}

// assertShapeOnly proves a result reports its shape to a log sink and never a reading.
func assertShapeOnly(t *testing.T, name string, value slog.LogValuer, needles ...string) {
	t.Helper()

	// The timestamp is dropped before matching: a wall clock contains arbitrary
	// digits, and a needle that matched one would be a false alarm rather than a leak.
	var logged strings.Builder
	options := &slog.HandlerOptions{ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}}
	slog.New(slog.NewTextHandler(&logged, options)).Info("tool result", "result", value)

	rendered := logged.String()
	for _, needle := range needles {
		if strings.Contains(rendered, needle) {
			t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
		}
	}
	if !strings.Contains(rendered, "model") {
		t.Errorf("logging %s produced no model group: %s", name, rendered)
	}
}

// trendSpan is the three-day window every walker test uses.
func trendSpan(t *testing.T) client.DateRange {
	t.Helper()

	start, end := trendStart, trendEnd
	from, err := client.ParseDate(start)
	if err != nil {
		t.Fatalf("ParseDate(%q) = %v", start, err)
	}
	to, err := client.ParseDate(end)
	if err != nil {
		t.Fatalf("ParseDate(%q) = %v", end, err)
	}
	span, err := client.NewDateRange(from, to)
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}
	return span
}
