package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// activityWritePath is the per-activity path every activity write targets.
func activityWritePath() string {
	return client.PathActivityPrefix + "/18446744"
}

func newActivityWrites(t *testing.T, h harness) *api.ActivityWrites {
	t.Helper()

	writes, err := api.NewActivityWrites(h.rc)
	if err != nil {
		t.Fatalf("NewActivityWrites() = %v", err)
	}
	return writes
}

// decodeBody decodes a recorded request body into a generic document.
func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("Unmarshal(body) = %v", err)
	}
	return decoded
}

// nestedObject extracts a nested DTO from a decoded body.
func nestedObject(t *testing.T, body []byte, key string) map[string]any {
	t.Helper()

	nested, ok := decodeBody(t, body)[key].(map[string]any)
	if !ok {
		t.Fatalf("body carries no %s object: %s", key, body)
	}
	return nested
}

// TestActivityWritesSendTheUpstreamPayloads pins the method, the path and the
// body of every simple activity write against the upstream shapes.
func TestActivityWritesSendTheUpstreamPayloads(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		call     func(*testing.T, *api.ActivityWrites, harness) error
		method   string
		wantKeys map[string]any
	}{
		"rename": {
			call: func(t *testing.T, w *api.ActivityWrites, h harness) error {
				_, err := w.SetName(t.Context(), h.session, mustID(t), "Morning Lift")
				return err
			},
			method:   http.MethodPut,
			wantKeys: map[string]any{"activityId": "18446744", "activityName": "Morning Lift"},
		},
		"description": {
			call: func(t *testing.T, w *api.ActivityWrites, h harness) error {
				_, err := w.SetDescription(t.Context(), h.session, mustID(t), "felt strong")
				return err
			},
			method:   http.MethodPut,
			wantKeys: map[string]any{"activityId": "18446744", "description": "felt strong"},
		},
		"delete": {
			call: func(t *testing.T, w *api.ActivityWrites, h harness) error {
				_, err := w.Delete(t.Context(), h.session, mustID(t))
				return err
			},
			method:   http.MethodDelete,
			wantKeys: nil,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(activityWritePath(),
				testkit.JSON(http.StatusOK, `{"activityId":18446744}`))
			h := newHarness(t, script, client.Limits{})

			if err := tc.call(t, newActivityWrites(t, h), h); err != nil {
				t.Fatalf("write = %v", err)
			}

			requests := h.server.Requests()
			if len(requests) != 1 {
				t.Fatalf("the fake received %d requests, want 1", len(requests))
			}
			if requests[0].Method != tc.method {
				t.Errorf("method = %q, want %q", requests[0].Method, tc.method)
			}
			if tc.wantKeys == nil {
				if len(requests[0].Body) != 0 {
					t.Errorf("body = %s, want none", requests[0].Body)
				}
				return
			}
			decoded := decodeBody(t, requests[0].Body)
			for key, value := range tc.wantKeys {
				if decoded[key] != value {
					t.Errorf("body[%q] = %v, want %v", key, decoded[key], value)
				}
			}
		})
	}
}

// TestActivityTypeAndEventTypeWritesCarryTheirDTOs covers the two writes that
// nest their payload under a DTO key.
func TestActivityTypeAndEventTypeWritesCarryTheirDTOs(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(activityWritePath(),
		testkit.JSON(http.StatusOK, `{"activityId":18446744}`))
	h := newHarness(t, script, client.Limits{})
	writes := newActivityWrites(t, h)

	if _, err := writes.SetType(t.Context(), h.session, mustID(t), api.TypeChange{
		TypeID: 13, TypeKey: "strength_training", ParentTypeID: 29,
	}); err != nil {
		t.Fatalf("SetType() = %v", err)
	}
	if _, err := writes.SetEventType(t.Context(), h.session, mustID(t),
		api.EventTypeChange{TypeKey: "training"}); err != nil {
		t.Fatalf("SetEventType() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 2 {
		t.Fatalf("the fake received %d requests, want 2", len(requests))
	}
	for _, key := range api.EventTypeKeys() {
		if _, err := api.ParseEventTypeKey(key); err != nil {
			t.Errorf("ParseEventTypeKey(%q) = %v, want every advertised key accepted", key, err)
		}
	}
	if got := nestedObject(t, requests[0].Body, "activityTypeDTO")["typeKey"]; got != "strength_training" {
		t.Errorf("activityTypeDTO.typeKey = %v, want strength_training", got)
	}
	if got := nestedObject(t, requests[1].Body, "eventTypeDTO")["typeKey"]; got != "training" {
		t.Errorf("eventTypeDTO.typeKey = %v, want training", got)
	}
}

// TestSubjectiveRatingsWriteOnlyTheFieldTheySet proves that setting the feel
// does not clear the perceived effort, and the reverse.
func TestSubjectiveRatingsWriteOnlyTheFieldTheySet(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(activityWritePath(), testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})
	writes := newActivityWrites(t, h)

	if _, err := writes.SetFeel(t.Context(), h.session, mustID(t), api.FeelGood); err != nil {
		t.Fatalf("SetFeel() = %v", err)
	}
	if _, err := writes.SetPerceivedEffort(t.Context(), h.session, mustID(t), 7); err != nil {
		t.Fatalf("SetPerceivedEffort() = %v", err)
	}

	requests := h.server.Requests()
	feel := nestedObject(t, requests[0].Body, "summaryDTO")
	if feel["directWorkoutFeel"] != float64(75) {
		t.Errorf("directWorkoutFeel = %v, want 75", feel["directWorkoutFeel"])
	}
	if _, present := feel["directWorkoutRpe"]; present {
		t.Error("the feel write also sent an RPE, which would clear the other rating")
	}

	effort := nestedObject(t, requests[1].Body, "summaryDTO")
	if effort["directWorkoutRpe"] != float64(70) {
		t.Errorf("directWorkoutRpe = %v, want 70 for RPE 7", effort["directWorkoutRpe"])
	}
	if _, present := effort["directWorkoutFeel"]; present {
		t.Error("the effort write also sent a feel, which would clear the other rating")
	}
}

// TestActivityWritesRefuseInvalidInput covers the boundary validation. Nothing is
// dispatched, so the fake receives no request at all.
func TestActivityWritesRefuseInvalidInput(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	writes := newActivityWrites(t, h)

	calls := map[string]func() error{
		"unset id": func() error {
			_, err := writes.SetName(t.Context(), h.session, client.ID{}, "x")
			return err
		},
		"empty name": func() error {
			_, err := writes.SetName(t.Context(), h.session, mustID(t), "")
			return err
		},
		"control characters": func() error {
			_, err := writes.SetDescription(t.Context(), h.session, mustID(t), "bad\x00value")
			return err
		},
		"unknown event type": func() error {
			_, err := writes.SetEventType(t.Context(), h.session, mustID(t),
				api.EventTypeChange{TypeKey: "party"})
			return err
		},
		"feel off the scale": func() error {
			_, err := writes.SetFeel(t.Context(), h.session, mustID(t), api.Feel(37))
			return err
		},
		"effort off the scale": func() error {
			_, err := writes.SetPerceivedEffort(t.Context(), h.session, mustID(t), 11)
			return err
		},
		"type change without ids": func() error {
			_, err := writes.SetType(t.Context(), h.session, mustID(t),
				api.TypeChange{TypeKey: typeKeyRunning})
			return err
		},
		"manual activity without a timezone": func() error {
			_, err := writes.CreateManual(t.Context(), h.session, api.ManualActivity{
				TypeKey: typeKeyRunning, StartLocal: "2026-01-31T09:00:00.000", DurationSeconds: 60,
			})
			return err
		},
		"manual activity with a bad start": func() error {
			_, err := writes.CreateManual(t.Context(), h.session, api.ManualActivity{
				TypeKey: typeKeyRunning, StartLocal: "31/01/2026", TimeZone: "UTC", DurationSeconds: 60,
			})
			return err
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, client.ErrValidation) {
				t.Errorf("call = %v, want ErrValidation", err)
			}
		})
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0: validation runs before dispatch", got)
	}
}

// TestManualActivityCreatesAPrivateActivity pins the create payload and proves
// the identifier comes back through the union decoder.
func TestManualActivityCreatesAPrivateActivity(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivityPrefix,
		testkit.JSON(http.StatusOK, `{"activityId":"991002"}`))
	h := newHarness(t, script, client.Limits{})

	created, err := newActivityWrites(t, h).CreateManual(t.Context(), h.session,
		api.NewManualActivity("resort_skiing", "2026-01-31T09:00:00.000", "Europe/Paris",
			"Piste day", 12.5, 90))
	if err != nil {
		t.Fatalf("CreateManual() = %v", err)
	}

	id, err := created.ID()
	if err != nil {
		t.Fatalf("ID() = %v", err)
	}
	if id.Int64() != 991002 {
		t.Errorf("ID() = %d, want 991002", id.Int64())
	}
	if created.Payload().Len() == 0 {
		t.Error("CreateManual() retained no raw payload")
	}

	request := h.server.Requests()[0]
	if request.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", request.Method)
	}
	if got := nestedObject(t, request.Body, "accessControlRuleDTO")["typeKey"]; got != "private" {
		t.Errorf("accessControlRuleDTO.typeKey = %v, want private", got)
	}

	summary := nestedObject(t, request.Body, "summaryDTO")
	if summary["distance"] != float64(12500) {
		t.Errorf("distance = %v, want 12500 metres for 12.5 km", summary["distance"])
	}
	if summary["duration"] != float64(5400) {
		t.Errorf("duration = %v, want 5400 seconds for 90 minutes", summary["duration"])
	}
}
