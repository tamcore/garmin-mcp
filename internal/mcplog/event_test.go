package mcplog_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/mcplog"
)

func newTestLogger(t *testing.T, cfg mcplog.Config) (*mcplog.Logger, *bytes.Buffer) {
	t.Helper()

	var buf bytes.Buffer
	logger, err := mcplog.New(&buf, cfg)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return logger, &buf
}

func decodeRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	line := bytes.TrimSpace(buf.Bytes())
	if len(line) == 0 {
		t.Fatal("no record was emitted")
	}
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatalf("record %q is not JSON: %v", line, err)
	}
	return record
}

func fullToolEvent() mcplog.ToolEvent {
	return mcplog.ToolEvent{
		RequestID:   "req-1",
		PrincipalID: "abc123",
		ClientID:    "claude-desktop",
		Category:    "womens-health",
		Tier:        "read-only",
		ToolName:    "get_menstrual_calendar_data",
		Outcome:     mcplog.OutcomeOK,
		Latency:     37 * time.Millisecond,
		Status:      mcplog.StatusSuccess,
	}
}

func TestToolCallEmitsOnlyTheAllowedFields(t *testing.T) {
	t.Parallel()

	logger, buf := newTestLogger(t, mcplog.Config{})
	logger.ToolCall(fullToolEvent())

	record := decodeRecord(t, buf)

	allowed := []string{
		"time", "level", "msg",
		"requestId", "principalId", "clientId",
		"category", "tier", "outcome", "latencyMs", "status",
	}
	for key := range record {
		if !slices.Contains(allowed, key) {
			t.Errorf("record carries unexpected key %q; the field set is closed", key)
		}
	}
	for _, key := range []string{"requestId", "principalId", "clientId", "category", "outcome"} {
		if _, ok := record[key]; !ok {
			t.Errorf("record is missing the required key %q", key)
		}
	}
}

// The headline redaction rule: an exact tool name can itself disclose a sensitive
// domain, so it is withheld unless the operator explicitly opted in.
func TestToolNameIsWithheldByDefault(t *testing.T) {
	t.Parallel()

	logger, buf := newTestLogger(t, mcplog.Config{})
	logger.ToolCall(fullToolEvent())

	if _, ok := decodeRecord(t, buf)["tool"]; ok {
		t.Fatal("the exact tool name must not be logged without the debug policy")
	}
	if strings.Contains(buf.String(), "get_menstrual_calendar_data") {
		t.Fatalf("the record %q leaks the exact tool name", buf.String())
	}
	if !strings.Contains(buf.String(), "womens-health") {
		t.Fatal("the coarse category must still be logged")
	}
}

func TestToolNameIsEmittedOnlyUnderTheDebugPolicy(t *testing.T) {
	t.Parallel()

	logger, buf := newTestLogger(t, mcplog.Config{DebugToolNames: true})
	if !logger.DebugToolNames() {
		t.Fatal("DebugToolNames must report the configured policy")
	}

	logger.ToolCall(fullToolEvent())

	got, ok := decodeRecord(t, buf)["tool"]
	if !ok {
		t.Fatal("the debug policy must emit the exact tool name")
	}
	if got != "get_menstrual_calendar_data" {
		t.Fatalf("tool = %v, want the exact name", got)
	}
}

func TestLatencyIsRecordedInMilliseconds(t *testing.T) {
	t.Parallel()

	logger, buf := newTestLogger(t, mcplog.Config{})
	event := fullToolEvent()
	event.Latency = 1500 * time.Millisecond
	logger.ToolCall(event)

	got, ok := decodeRecord(t, buf)["latencyMs"].(float64)
	if !ok {
		t.Fatal("latencyMs must be a number")
	}
	if got != 1500 {
		t.Fatalf("latencyMs = %v, want 1500", got)
	}
}

func TestOutcomeAndStatusAreCoarseLabels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		outcome mcplog.Outcome
		want    string
	}{
		{mcplog.OutcomeOK, "ok"},
		{mcplog.OutcomeDenied, "denied"},
		{mcplog.OutcomeRateLimited, "rate-limited"},
		{mcplog.OutcomeError, "error"},
		{mcplog.Outcome(""), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.outcome.String(); got != tc.want {
			t.Errorf("Outcome(%q).String() = %q, want %q", string(tc.outcome), got, tc.want)
		}
	}

	statuses := []struct {
		status mcplog.Status
		want   string
	}{
		{mcplog.StatusSuccess, "success"},
		{mcplog.StatusClientError, "client-error"},
		{mcplog.StatusServerError, "server-error"},
		{mcplog.StatusUpstreamError, "upstream-error"},
		{mcplog.Status(""), "none"},
	}
	for _, tc := range statuses {
		if got := tc.status.String(); got != tc.want {
			t.Errorf("Status(%q).String() = %q, want %q", string(tc.status), got, tc.want)
		}
	}
}

// A denial or a rate-limit rejection is logged at warn, because it is the signal
// an operator wants; a success stays at info; an error is an error.
func TestOutcomeSelectsTheLevel(t *testing.T) {
	t.Parallel()

	cases := map[mcplog.Outcome]string{
		mcplog.OutcomeOK:          "INFO",
		mcplog.OutcomeDenied:      "WARN",
		mcplog.OutcomeRateLimited: "WARN",
		mcplog.OutcomeError:       "ERROR",
	}

	for outcome, wantLevel := range cases {
		logger, buf := newTestLogger(t, mcplog.Config{})
		event := fullToolEvent()
		event.Outcome = outcome
		logger.ToolCall(event)

		if got := decodeRecord(t, buf)["level"]; got != wantLevel {
			t.Errorf("outcome %q logged at level %v, want %v", outcome, got, wantLevel)
		}
	}
}

// Reason is caller-authored coarse text. It is carried so an operator can see why
// a call was refused, and the policy package guarantees it excludes the tool name.
func TestReasonIsCarriedWhenSet(t *testing.T) {
	t.Parallel()

	logger, buf := newTestLogger(t, mcplog.Config{})
	event := fullToolEvent()
	event.Outcome = mcplog.OutcomeDenied
	event.Reason = "the destructive tier is not enabled for this deployment"
	logger.ToolCall(event)

	record := decodeRecord(t, buf)
	if record["reason"] != event.Reason {
		t.Fatalf("reason = %v, want %q", record["reason"], event.Reason)
	}
}

func TestEmptyFieldsAreOmittedRatherThanLoggedBlank(t *testing.T) {
	t.Parallel()

	logger, buf := newTestLogger(t, mcplog.Config{})
	logger.ToolCall(mcplog.ToolEvent{Category: categoryActivities, Outcome: mcplog.OutcomeOK})

	record := decodeRecord(t, buf)
	for _, key := range []string{"tier", "reason", "status"} {
		if _, ok := record[key]; ok {
			t.Errorf("empty field %q was logged; empty fields must be omitted", key)
		}
	}
}

// There is no API through which a body, a token, an email, a coordinate, or a
// health metric could be logged: ToolEvent's fields are the whole vocabulary.
// This test pins the record to a fixed size so a future field cannot be added
// without a deliberate decision here.
func TestToolEventCarriesNoFreeFormPayloadChannel(t *testing.T) {
	t.Parallel()

	logger, buf := newTestLogger(t, mcplog.Config{DebugToolNames: true})

	logger.ToolCall(mcplog.ToolEvent{
		RequestID:   "req-1",
		PrincipalID: "abc123",
		ClientID:    "claude-desktop",
		Category:    "health",
		Tier:        "read-only",
		ToolName:    "get_sleep_data",
		Outcome:     mcplog.OutcomeOK,
		Status:      mcplog.StatusSuccess,
		Latency:     time.Millisecond,
		Reason:      "",
	})

	// Three slog keys (time, level, msg) plus the nine event fields that are set
	// here: requestId, principalId, clientId, category, tier, tool, outcome,
	// latencyMs, status. Reason is empty and therefore omitted.
	const wantKeys = 12

	record := decodeRecord(t, buf)
	if len(record) != wantKeys {
		t.Fatalf("record has %d keys (%v), want %d; the vocabulary must be fixed",
			len(record), record, wantKeys)
	}
}

func TestLifecycleEmitsOnlyOperationalFields(t *testing.T) {
	t.Parallel()

	logger, buf := newTestLogger(t, mcplog.Config{})
	logger.Lifecycle(mcplog.LifecycleEvent{
		Phase:           phaseStartup,
		Transport:       transportStdio,
		Mode:            "local",
		ProtocolVersion: "2026-07-28",
		ToolCount:       1,
	})

	record := decodeRecord(t, buf)
	allowed := []string{"time", "level", "msg", "phase", "transport", "mode", "protocolVersion", "toolCount"}
	for key := range record {
		if !slices.Contains(allowed, key) {
			t.Errorf("lifecycle record carries unexpected key %q", key)
		}
	}
	if record["phase"] != phaseStartup {
		t.Errorf("phase = %v, want startup", record["phase"])
	}
	if record["toolCount"] != float64(1) {
		t.Errorf("toolCount = %v, want 1", record["toolCount"])
	}
}
