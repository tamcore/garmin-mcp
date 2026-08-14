package client_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// tolerantProfile is the shape a domain model takes: optional pointers for fields
// Garmin may omit, and a RawMessage where the shape varies.
type tolerantProfile struct {
	DisplayName *string         `json:"displayName"`
	ProfileID   *int64          `json:"profileId"`
	Location    *string         `json:"location"`
	Extensions  json.RawMessage `json:"userProfileExtensions"`
}

func decodePayload(t *testing.T, body string, out any) error {
	t.Helper()

	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK,
		header: jsonHeader(),
		body:   []byte(body),
	}}}
	payload, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), profileRequest())
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}
	return client.DecodeJSON(payload, out)
}

func TestDecodeJSONIgnoresUnknownFieldsAndKeepsOptionalOnesNil(t *testing.T) {
	t.Parallel()

	body := `{"displayName":"fake-tester","profileId":900001,` +
		`"unknownField":{"nested":true},"userProfileExtensions":[1,2,3]}`

	var profile tolerantProfile
	if err := decodePayload(t, body, &profile); err != nil {
		t.Fatalf("DecodeJSON() = %v, want an unknown field to be ignored", err)
	}
	if profile.DisplayName == nil || *profile.DisplayName != fakeDisplayName {
		t.Errorf("DisplayName = %v, want the decoded value", profile.DisplayName)
	}
	if profile.ProfileID == nil || *profile.ProfileID != 900001 {
		t.Errorf("ProfileID = %v, want 900001", profile.ProfileID)
	}
	if profile.Location != nil {
		t.Errorf("Location = %v, want nil for an absent field", *profile.Location)
	}
	if string(profile.Extensions) != "[1,2,3]" {
		t.Errorf("Extensions = %q, want the raw shape retained", string(profile.Extensions))
	}
}

func TestDecodeJSONReportsMalformedPayloadWithoutEchoingIt(t *testing.T) {
	t.Parallel()

	const secret = "SENTINEL-HEALTH-58bpm"
	body := `{"displayName":"` + secret + `"` // truncated on purpose

	var profile tolerantProfile
	err := decodePayload(t, body, &profile)
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Fatalf("DecodeJSON() = %v, want ErrMalformedPayload", err)
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("the failure is not an *APIError")
	}
	if apiErr.Endpoint != client.EndpointSocialProfile || apiErr.Op != client.OpGetSocialProfile {
		t.Errorf("labels = %q/%q, want the request's labels", apiErr.Op, apiErr.Endpoint)
	}
	if got := err.Error(); strings.Contains(got, secret) {
		t.Errorf("DecodeJSON() = %q leaks the payload it could not parse", got)
	}
}

func TestDecodeJSONLeavesTheTargetUntouchedForNoContent(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusNoContent}}}
	payload, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), profileRequest())
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}

	profile := tolerantProfile{}
	if err := client.DecodeJSON(payload, &profile); err != nil {
		t.Fatalf("DecodeJSON() = %v, want nil for a normalized 204", err)
	}
	if profile.DisplayName != nil {
		t.Error("a 204 must leave the target untouched")
	}
}

func TestNumberToleratesEveryShapeGarminUses(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		json    string
		want    float64
		present bool
	}{
		"number":         {`5`, 5, true},
		"float":          {`58.5`, 58.5, true},
		"numeric string": {`"58"`, 58, true},
		"spaced string":  {`" 58 "`, 58, true},
		jsonNullLiteral:  {jsonNullLiteral, 0, false},
		"empty string":   {`""`, 0, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var number client.Number
			if err := json.Unmarshal([]byte(tc.json), &number); err != nil {
				t.Fatalf("Unmarshal(%s) = %v, want nil", tc.json, err)
			}
			got, ok := number.Float64()
			if ok != tc.present {
				t.Errorf("Float64() presence = %v, want %v", ok, tc.present)
			}
			if ok && got != tc.want {
				t.Errorf("Float64() = %v, want %v", got, tc.want)
			}
		})
	}

	var number client.Number
	if err := json.Unmarshal([]byte(`{"unexpected":true}`), &number); err == nil {
		t.Error("Unmarshal of an object into a Number = nil, want an error")
	}
}

func TestTextToleratesEveryShapeGarminUses(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		json    string
		want    string
		present bool
	}{
		"string":        {`"running"`, "running", true},
		"number":        {`12`, "12", true},
		"boolean":       {`true`, "true", true},
		jsonNullLiteral: {jsonNullLiteral, "", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var text client.Text
			if err := json.Unmarshal([]byte(tc.json), &text); err != nil {
				t.Fatalf("Unmarshal(%s) = %v, want nil", tc.json, err)
			}
			got, ok := text.Value()
			if ok != tc.present {
				t.Errorf("Value() presence = %v, want %v", ok, tc.present)
			}
			if got != tc.want {
				t.Errorf("Value() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestListDecodesASingleObjectAndAnArrayAlike(t *testing.T) {
	t.Parallel()

	type lap struct {
		Distance client.Number `json:"distance"`
	}

	cases := map[string]struct {
		json string
		want int
	}{
		"array":         {`[{"distance":400},{"distance":"800"}]`, 2},
		"single object": {`{"distance":400}`, 1},
		jsonNullLiteral: {jsonNullLiteral, 0},
		"empty array":   {`[]`, 0},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var list client.List[lap]
			if err := json.Unmarshal([]byte(tc.json), &list); err != nil {
				t.Fatalf("Unmarshal(%s) = %v, want nil", tc.json, err)
			}
			if got := len(list.Items()); got != tc.want {
				t.Errorf("len(Items()) = %d, want %d", got, tc.want)
			}
		})
	}
}
