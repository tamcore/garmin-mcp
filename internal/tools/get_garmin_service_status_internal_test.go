package tools

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const siteStatusPage = `<div id="platforms">
<div class="service green"><span class="item">Garmin Connect</span></div>
<div class="service red"><span class="item">Garmin Pay</span>
<div class="status-reasons">Degraded payments</div></div>
</div>
<div id="features">
<div class="service green"><span class="item">Sync</span></div>
<div class="service green"><span class="item">Activities</span></div>
</div>`

// callServiceStatus returns the decoded result, or the error text when the call failed.
func callServiceStatus(t *testing.T, behavior testkit.Behavior) (GarminServiceStatus, string, *testkit.Server) {
	t.Helper()

	svc, fake := newToolsService(t, testkit.NewScript().With(protocol.PathSiteStatus, behavior))
	server := newToolsServer(t, svc, newToolsServerConfig{readOnly: []newToolsRegistration{{
		name: ToolGetGarminServiceStatus, register: registerGetGarminServiceStatus,
	}}})
	result := newToolsRawCall(t, newToolsSession(t, server), ToolGetGarminServiceStatus, nil)
	if result.IsError {
		return GarminServiceStatus{}, newToolsResultText(result), fake
	}

	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshalling structured content: %v", err)
	}
	var status GarminServiceStatus
	if err := json.Unmarshal(encoded, &status); err != nil {
		t.Fatalf("decoding structured content: %v", err)
	}
	return status, "", fake
}

func TestGetGarminServiceStatusReturnsSortedServices(t *testing.T) {
	t.Parallel()

	got, text, _ := callServiceStatus(t, testkit.HTML(http.StatusOK, siteStatusPage))
	if text != "" {
		t.Fatalf("call failed: %s", text)
	}

	want := GarminServiceStatus{
		Platforms: []ServiceState{
			{Name: "Garmin Connect", Status: "up"},
			{Name: "Garmin Pay", Status: "down", Reasons: []string{"Degraded payments"}},
		},
		Features: []ServiceState{
			{Name: "Activities", Status: "up"},
			{Name: "Sync", Status: "up"},
		},
	}
	if !slices.EqualFunc(got.Platforms, want.Platforms, equalServiceState) ||
		!slices.EqualFunc(got.Features, want.Features, equalServiceState) {
		t.Errorf("status = %+v, want %+v", got, want)
	}
}

func equalServiceState(a, b ServiceState) bool {
	return a.Name == b.Name && a.Status == b.Status && slices.Equal(a.Reasons, b.Reasons)
}

func TestGetGarminServiceStatusSendsNoCredential(t *testing.T) {
	t.Parallel()

	_, text, fake := callServiceStatus(t, testkit.HTML(http.StatusOK, siteStatusPage))
	if text != "" {
		t.Fatalf("call failed: %s", text)
	}
	requests := fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if requests[0].Header.Get("Authorization") != "" || requests[0].Header.Get("Cookie") != "" {
		t.Error("the status request carried a credential header")
	}
}

func TestGetGarminServiceStatusFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		behavior testkit.Behavior
		want     string
	}{
		{"layout drift", testkit.HTML(http.StatusOK, "<p>maintenance</p>"), "upstream drift"},
		{"unavailable", testkit.HTML(http.StatusServiceUnavailable, "down"), "could not be read"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, text, _ := callServiceStatus(t, tc.behavior)
			if !strings.Contains(text, tc.want) {
				t.Errorf("error = %q, want it to mention %q", text, tc.want)
			}
			if strings.Contains(text, "http") {
				t.Errorf("error %q leaks a URL", text)
			}
		})
	}
}

func TestGarminServiceStatusLogValueReportsCountsOnly(t *testing.T) {
	t.Parallel()

	status := GarminServiceStatus{Platforms: []ServiceState{{
		Name: "Garmin Pay", Status: "down", Reasons: []string{"Degraded payments"},
	}}}
	rendered := status.LogValue().String()
	if strings.Contains(rendered, "Garmin Pay") || strings.Contains(rendered, "Degraded") {
		t.Errorf("LogValue() = %q, want counts only", rendered)
	}
}
