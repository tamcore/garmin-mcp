package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

func TestClientReportsItsResolvedLimitsAndRegion(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, client.Limits{MaxPageSize: 7})
	if got := c.Limits().MaxPageSize; got != 7 {
		t.Errorf("Limits().MaxPageSize = %d, want the configured 7", got)
	}
	if got := c.Limits().MaxAttempts; got != client.DefaultMaxAttempts {
		t.Errorf("Limits().MaxAttempts = %d, want the default", got)
	}
	if got := c.Hosts().Domain(); got != protocol.DomainGlobal {
		t.Errorf("Hosts().Domain() = %q, want the global region", got)
	}
}

func TestDefaultSleeperWaitsAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	// A Client built without a Sleeper waits for real, which is what a deployment
	// does; the wait must still end when the context does.
	c, err := client.New(client.Config{Hosts: testHosts(t), Limits: client.Limits{
		MaxAttempts: 2, BaseBackoff: client.MaxBackoffCap, MaxBackoff: client.MaxBackoffCap,
	}})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusServiceUnavailable, header: jsonHeader()}}}
	_, err = c.Do(ctx, mustSession(t, caller), profileRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Do() = %v, want the context deadline to end the backoff wait", err)
	}
}

func TestEffectAndKindLabels(t *testing.T) {
	t.Parallel()

	effects := map[client.Effect]string{
		client.EffectRead:            "read",
		client.EffectIdempotentWrite: "idempotent_write",
		client.EffectUnsafeWrite:     "unsafe_write",
		client.EffectDelete:          "delete",
		client.Effect(99):            labelUnknown,
	}
	for effect, want := range effects {
		if got := effect.String(); got != want {
			t.Errorf("Effect(%d).String() = %q, want %q", effect, got, want)
		}
	}

	if got := client.Kind(99).String(); got == "" {
		t.Error("an out-of-range Kind must still render a label")
	}
}

func TestNumberAndTextRoundTripThroughJSON(t *testing.T) {
	t.Parallel()

	type record struct {
		Count client.Number `json:"count"`
		Label client.Text   `json:"label"`
	}

	encoded, err := json.Marshal(record{Count: client.NewNumber(12), Label: client.NewText("running")})
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	if string(encoded) != `{"count":12,"label":"running"}` {
		t.Errorf("Marshal() = %s, want the plain values", encoded)
	}

	empty, err := json.Marshal(record{})
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	if string(empty) != `{"count":null,"label":null}` {
		t.Errorf("Marshal() of absent fields = %s, want nulls", empty)
	}

	var decoded record
	if err := json.Unmarshal([]byte(`{"count":"12","label":7}`), &decoded); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if count, ok := decoded.Count.Int64(); !ok || count != 12 {
		t.Errorf("Int64() = %d/%v, want 12", count, ok)
	}
	if !decoded.Count.IsSet() || !decoded.Label.IsSet() {
		t.Error("both fields must report present")
	}
	if got, _ := decoded.Label.Value(); got != "7" {
		t.Errorf("Value() = %q, want the number rendered as text", got)
	}
}

func TestListNormalizesToAnArray(t *testing.T) {
	t.Parallel()

	type lap struct {
		Distance client.Number `json:"distance"`
	}

	list := client.NewList(lap{Distance: client.NewNumber(400)})
	if list.Len() != 1 {
		t.Errorf("Len() = %d, want 1", list.Len())
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	if string(encoded) != `[{"distance":400}]` {
		t.Errorf("Marshal() = %s, want a one-element array", encoded)
	}

	var zero client.List[lap]
	empty, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	if string(empty) != `[]` {
		t.Errorf("Marshal() of the zero List = %s, want an empty array", empty)
	}
}

func TestDateAndRangeAccessors(t *testing.T) {
	t.Parallel()

	instant := time.Date(2026, time.January, 31, 18, 45, 0, 0, time.UTC)
	date := client.NewDate(instant)
	if got := date.String(); got != testCalendarDate {
		t.Errorf("NewDate().String() = %q, want %q", got, testCalendarDate)
	}
	if got := date.AddDays(1).String(); got != "2026-02-01" {
		t.Errorf("AddDays(1) = %q, want 2026-02-01", got)
	}
	if !client.NewDate(time.Time{}).IsZero() {
		t.Error("NewDate(zero instant) must be the zero Date")
	}
	if !(client.Date{}).AddDays(1).IsZero() {
		t.Error("AddDays on the zero Date must stay zero")
	}
	if got := (client.Date{}).String(); got != "" {
		t.Errorf("the zero Date renders %q, want the empty string", got)
	}

	span, err := client.NewDateRange(date, date.AddDays(2))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}
	if span.Start().String() != testCalendarDate || span.End().String() != "2026-02-02" {
		t.Errorf("range = %q..%q, want the constructed window", span.Start(), span.End())
	}
	if got := (client.DateRange{}).Days(); got != 0 {
		t.Errorf("the zero DateRange reports %d days, want 0", got)
	}
	if err := (client.Limits{}).ValidateDateRange(client.DateRange{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("ValidateDateRange(zero) = %v, want a validation error", err)
	}
}

func TestZeroIdentifierAndDisplayNameAccessors(t *testing.T) {
	t.Parallel()

	if got := (client.ID{}).String(); got != "" {
		t.Errorf("the zero ID renders %q, want the empty string", got)
	}
	if got := (client.DisplayName{}).Value(); got != "" {
		t.Errorf("the zero DisplayName reports %q, want the empty string", got)
	}
	if !(client.DisplayName{}).IsZero() {
		t.Error("the zero DisplayName must report absent")
	}
}

func TestRequestRejectsAHostileQueryParameter(t *testing.T) {
	t.Parallel()

	cases := map[string]url.Values{
		"newline in a value": {"date": {"2026-01-31\nX-Injected: 1"}},
		"newline in a name":  {"da\nte": {testCalendarDate}},
		"empty name":         {"": {testCalendarDate}},
	}

	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := profileRequest()
			req.Query = query
			caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusOK}}}
			if _, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller),
				req); !errors.Is(err, client.ErrValidation) {
				t.Errorf("Do() = %v, want a validation error", err)
			}
			if caller.calls() != 0 {
				t.Errorf("caller was used %d times, want 0", caller.calls())
			}
		})
	}
}

func TestPayloadContentTypeIsSanitized(t *testing.T) {
	t.Parallel()

	header := make(http.Header, 1)
	header["Content-Type"] = []string{"application/json; charset=UTF-8\r\nX-Injected: 1"}
	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK, header: header, body: []byte(profileBody),
	}}}

	payload, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), profileRequest())
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}
	if got := payload.ContentType(); got != mediaTypeJSON {
		t.Errorf("ContentType() = %q, want the sanitized media type", got)
	}
}
