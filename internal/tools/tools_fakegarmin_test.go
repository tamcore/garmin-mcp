//go:build fakegarmin

package tools_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// TestWholeReadOnlyToolSurfaceAgainstOneFakeAccount drives every registered tool over
// one MCP session against one scripted account, which is the flow a real client
// performs. It is the integration counterpart of the per-tool unit tests: those check
// a mapping, this checks that the whole surface works together over the transport,
// the middleware chain and the request layer.
func TestWholeReadOnlyToolSurfaceAgainstOneFakeAccount(t *testing.T) {
	h := newHarness(t, readScript())

	calls := []struct {
		tool string
		args map[string]any
	}{
		{tools.ToolGetUserProfile, nil},
		{tools.ToolGetFullName, nil},
		{tools.ToolGetUnitSystem, nil},
		{tools.ToolGetActivities, map[string]any{argLimit: 2}},
		{tools.ToolGetActivitiesByDate, map[string]any{
			argStartDate: windowStart, argEndDate: testCalendarDate,
		}},
		{tools.ToolGetSleepData, map[string]any{argDate: testCalendarDate}},
		{tools.ToolGetUserSummary, map[string]any{argDate: testCalendarDate}},
		{tools.ToolGetDevices, nil},
		{tools.ToolGetActivityTypedSplits, map[string]any{argActivityID: testActivityID}},
		{tools.ToolGetActivityExerciseSets, map[string]any{argActivityID: testActivityID}},
	}

	for _, call := range calls {
		t.Run(call.tool, func(t *testing.T) {
			if got := h.call(t, call.tool, call.args); len(got) == 0 {
				t.Errorf("%s returned an empty result", call.tool)
			}
		})
	}

	assertNoCredentialLeftThisProcess(t, h)
}

// assertNoCredentialLeftThisProcess pins the boundary: this package never attaches a
// token. The caller owns the credential, and in these tests there is none.
func assertNoCredentialLeftThisProcess(t *testing.T, h harness) {
	t.Helper()

	recorded := h.fake.Requests()
	if len(recorded) < len(wantToolNames) {
		t.Fatalf("the fake received %d requests, want at least one per tool", len(recorded))
	}
	for _, request := range recorded {
		if request.Header.Get("Authorization") != "" {
			t.Error("a request carried an Authorization header: the caller owns the token")
		}
		if request.Method != http.MethodGet {
			t.Errorf("a read-only tool issued %s, want GET", request.Method)
		}
	}
}

// TestARateLimitedGarminBecomesAnActionableRefusal walks the whole path a 429 takes:
// the request layer classifies it, the domain client wraps it, and the tool turns it
// into advice a model can act on without seeing the response body.
func TestARateLimitedGarminBecomesAnActionableRefusal(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathDevices, repeat(testkit.RateLimited(1), 4)...)
	h := newHarness(t, script)

	text := h.callError(t, tools.ToolGetDevices, nil)

	assertSanitized(t, text)
	if !containsFold(text, "rate-limited") {
		t.Errorf("the refusal %q does not name the rate limit", text)
	}
}
