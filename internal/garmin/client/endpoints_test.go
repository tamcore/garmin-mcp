package client_test

import (
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

func TestEndpointLabelsAreSanitized(t *testing.T) {
	t.Parallel()

	for _, endpoint := range client.KnownEndpoints() {
		if !endpoint.IsKnown() {
			t.Errorf("KnownEndpoints() reported %q, which IsKnown() rejects", string(endpoint))
		}
		label := endpoint.String()
		switch {
		case label == "":
			t.Errorf("endpoint %q renders empty", string(endpoint))
		case strings.ContainsAny(label, "/?&:= "):
			t.Errorf("endpoint label %q looks like a URL, not a label", label)
		}
	}
}

func TestEndpointLabelsAreUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[client.Endpoint]struct{})
	for _, endpoint := range client.KnownEndpoints() {
		if _, duplicate := seen[endpoint]; duplicate {
			t.Errorf("endpoint label %q is declared twice; a label must name exactly one endpoint", string(endpoint))
		}
		seen[endpoint] = struct{}{}
	}
}

func TestUnknownEndpointRendersUnknown(t *testing.T) {
	t.Parallel()

	raw := client.Endpoint("https://connectapi.garmin.com/x?token=SENTINEL-TOKEN")
	if raw.IsKnown() {
		t.Fatal("a raw URL must not be a known endpoint label")
	}
	if got := raw.String(); got != labelUnknown {
		t.Errorf("String() = %q, want %q so a URL with a query can never be rendered", got, labelUnknown)
	}
}

func TestSocialProfileEndpointReusesProtocolLabel(t *testing.T) {
	t.Parallel()

	if string(client.EndpointSocialProfile) != string(protocol.EndpointSocialProfile) {
		t.Errorf("EndpointSocialProfile = %q, want the protocol label %q",
			string(client.EndpointSocialProfile), string(protocol.EndpointSocialProfile))
	}
}

func TestOpLabelsAreSanitized(t *testing.T) {
	t.Parallel()

	for _, op := range client.KnownOps() {
		if !op.IsKnown() {
			t.Errorf("KnownOps() reported %q, which IsKnown() rejects", string(op))
		}
		if strings.ContainsAny(op.String(), "/?&:= ") {
			t.Errorf("op label %q looks like a URL, not a label", op.String())
		}
	}
}

func TestOpLabelsAreUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[client.Op]struct{})
	for _, op := range client.KnownOps() {
		if _, duplicate := seen[op]; duplicate {
			t.Errorf("op label %q is declared twice; a label must name exactly one operation", string(op))
		}
		seen[op] = struct{}{}
	}
}

// TestHealthOpsAreRegistered pins the health-and-wellness operations against the
// upstream tool names. Request.Validate refuses an unregistered op, so a tool
// whose op is missing from the allowlist cannot be called at all.
func TestHealthOpsAreRegistered(t *testing.T) {
	t.Parallel()

	health := []client.Op{
		client.OpGetStats, client.OpGetStatsAndBody, client.OpGetBodyComposition,
		client.OpGetStepsData, client.OpGetDailySteps, client.OpGetWeeklySteps,
		client.OpGetWeeklyStress, client.OpGetWeeklyIntensityMinutes,
		client.OpGetTrainingReadiness, client.OpGetMorningTrainingReadiness,
		client.OpGetBodyBattery, client.OpGetBodyBatteryEvents, client.OpGetBloodPressure,
		client.OpGetFloors, client.OpGetRestingHeartRateDay, client.OpGetHeartRates,
		client.OpGetHeartRatesSummary, client.OpGetHydrationData, client.OpGetSleepSummary,
		client.OpGetStressData, client.OpGetStressSummary, client.OpGetAllDayStress,
		client.OpGetAllDayEvents, client.OpGetRespirationData, client.OpGetRespirationSummary,
		client.OpGetSpO2Data, client.OpGetLifestyleLoggingData,
	}
	if len(health) != 27 {
		t.Fatalf("the health tier has %d ops, want the 27 upstream health_wellness tools", len(health))
	}
	for _, op := range health {
		if !op.IsKnown() {
			t.Errorf("op %q is not in the allowlist, so no health tool can use it", string(op))
		}
		if op.IsCredentialSubmission() {
			t.Errorf("op %q must not be treated as a credential submission", string(op))
		}
	}
}

// TestHealthPathsAreTemplates keeps every health path a query-free template, so
// a date, a week count or a display name is always appended as an escaped
// segment by the domain client rather than baked into the constant.
func TestHealthPathsAreTemplates(t *testing.T) {
	t.Parallel()

	paths := []string{
		client.PathDailySummaryChartPrefix, client.PathFloorsChartDailyPrefix,
		client.PathDailyHeartRatePrefix, client.PathDailyStressPrefix,
		client.PathDailyRespirationPrefix, client.PathDailySpO2Prefix,
		client.PathDailyEvents, client.PathBodyBatteryDaily,
		client.PathBodyBatteryEventsPrefix, client.PathDailyHydrationPrefix,
		client.PathDailyStepsStatsPrefix, client.PathWeeklyStepsStatsPrefix,
		client.PathWeeklyStressStatsPrefix, client.PathWeeklyIntensityMinutesStatsPrefix,
		client.PathBodyComposition, client.PathBloodPressureRangePrefix,
		client.PathRestingHeartRatePrefix, client.PathTrainingReadinessPrefix,
		client.PathLifestyleLoggingPrefix,
	}
	seen := make(map[string]struct{})
	for _, path := range paths {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("path %q must be host-relative and start with a slash", path)
		}
		if strings.ContainsAny(path, "?&= {}") || strings.HasSuffix(path, "/") {
			t.Errorf("path %q must be a bare template: no query, no placeholder, no trailing slash", path)
		}
		if _, duplicate := seen[path]; duplicate {
			t.Errorf("path %q is declared twice; one endpoint needs exactly one constant", path)
		}
		seen[path] = struct{}{}
	}
}

func TestUnknownOpRendersUnknown(t *testing.T) {
	t.Parallel()

	if got := client.Op("password=hunter2").String(); got != labelUnknown {
		t.Errorf("String() = %q, want %q", got, labelUnknown)
	}
}

func TestCredentialSubmissionOpsAreRecognized(t *testing.T) {
	t.Parallel()

	credential := []protocol.Op{
		protocol.OpMobileLogin, protocol.OpPortalLogin, protocol.OpWidgetLogin,
		protocol.OpVerifyMFA, protocol.OpRequestMFACode,
	}
	for _, op := range credential {
		converted := client.Op(op)
		if !converted.IsCredentialSubmission() {
			t.Errorf("Op(%q).IsCredentialSubmission() = false, want true", string(op))
		}
		if got := converted.String(); got != string(op) {
			t.Errorf("Op(%q).String() = %q, want the protocol label rendered verbatim", string(op), got)
		}
	}

	if client.OpListActivities.IsCredentialSubmission() {
		t.Error("OpListActivities.IsCredentialSubmission() = true, want false")
	}
}
